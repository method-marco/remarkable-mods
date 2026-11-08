#!/bin/bash
# Random sleep screen rotation for the reMarkable.
# xochitl's SleepScreenPath points at $DIR/.current.png; this swaps in a random
# picture at startup and every time the display wakes up, so the *next* sleep
# shows a new picture.
# Pictures: only the images in the library folder "Screensaver" (converted by
# sleepscreen-library into $DIR/library/, kept in sync with the folder). Without
# pictures the stock sleep screen is shown.
# Wake is detected from xochitl's own log line:
#   "Changing display state from DeepSleep to Normal"

DIR="${DIR:-/home/root/sleepscreens}"
LIBRARY_TOOL=/home/root/bin/sleepscreen-library
CUR="$DIR/.current.png"
STATE="$DIR/.last"

CONVERT_PID=""

# convert new documents in the background (a few seconds each), one run at a time
convert_new() {
    [ -x "$LIBRARY_TOOL" ] || return
    if [ -n "$CONVERT_PID" ] && kill -0 "$CONVERT_PID" 2>/dev/null; then
        return
    fi
    OUT_DIR="$DIR/library" "$LIBRARY_TOOL" sync &
    CONVERT_PID=$!
}

pick() {
    shopt -s nullglob
    # first drop pictures whose documents left the folder (instant)...
    [ -x "$LIBRARY_TOOL" ] && OUT_DIR="$DIR/library" "$LIBRARY_TOOL" remove
    local imgs=("$DIR"/library/*.png) last="" choice
    if [ ${#imgs[@]} -eq 0 ]; then
        if ! cmp -s /usr/share/remarkable/suspended.png "$CUR"; then
            cp -f /usr/share/remarkable/suspended.png "$CUR.tmp" && mv -f "$CUR.tmp" "$CUR"
            rm -f "$STATE"
            echo "no pictures in the library folder \"Screensaver\": stock sleep screen"
        fi
        return
    fi
    [ -f "$STATE" ] && last="$(cat "$STATE")"
    choice="${imgs[RANDOM % ${#imgs[@]}]}"
    if [ ${#imgs[@]} -gt 1 ]; then
        while [ "$choice" = "$last" ]; do choice="${imgs[RANDOM % ${#imgs[@]}]}"; done
    fi
    # Atomic replace so xochitl never reads a half-written file.
    cp -f "$choice" "$CUR.tmp" && mv -f "$CUR.tmp" "$CUR" && echo "$choice" > "$STATE"
    echo "sleep screen -> $(basename "$choice")"
}

# on startup and every wake: pick from the ready pictures, then convert new ones
# (they join the rotation from the next wake on)
on_wake() {
    pick
    convert_new
}

on_wake
while IFS= read -r line; do
    case "$line" in
        *"display state from DeepSleep to Normal"*) on_wake ;;
    esac
done < <(journalctl -f -n 0 -o cat -u xochitl)
