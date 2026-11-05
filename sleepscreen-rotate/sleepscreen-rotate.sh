#!/bin/bash
# Random sleep screen rotation for the reMarkable.
# xochitl's SleepScreenPath points at $DIR/.current.png; this swaps in a random
# picture from $DIR/*.png at startup and every time the display wakes up,
# so the *next* sleep shows a new picture.
# Wake is detected from xochitl's own log line:
#   "Changing display state from DeepSleep to Normal"

DIR="${DIR:-/home/root/sleepscreens}"
CUR="$DIR/.current.png"
STATE="$DIR/.last"

pick() {
    shopt -s nullglob
    local imgs=("$DIR"/*.png) last="" choice
    [ ${#imgs[@]} -gt 0 ] || return
    [ -f "$STATE" ] && last="$(cat "$STATE")"
    choice="${imgs[RANDOM % ${#imgs[@]}]}"
    if [ ${#imgs[@]} -gt 1 ]; then
        while [ "$choice" = "$last" ]; do choice="${imgs[RANDOM % ${#imgs[@]}]}"; done
    fi
    # Atomic replace so xochitl never reads a half-written file.
    cp -f "$choice" "$CUR.tmp" && mv -f "$CUR.tmp" "$CUR" && echo "$choice" > "$STATE"
    echo "sleep screen -> $(basename "$choice")"
}

pick
journalctl -f -n 0 -o cat -u xochitl | while IFS= read -r line; do
    case "$line" in
        *"display state from DeepSleep to Normal"*) pick ;;
    esac
done
