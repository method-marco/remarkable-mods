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
# With a passcode, the passcode screen shows the current picture (patch
# sleepscreen-rotate.qmd), so the picture is kept while it's up: going back to
# sleep without unlocking shows the same picture again. The next picture is
# picked once the device is unlocked. xochitl logs "lock changed: true" just
# after the wake line, so a wake is only handled after a short wait.

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

# After xochitl starts with a passcode set, it shows the passcode screen without
# logging "lock changed: true": treat a (re)start as locked.
started_locked() { [ -s /data/passcode ] && echo 1; }
LOCKED="$(started_locked)"  # passcode screen is up
WOKE=""          # woke while locked: pick at the unlock
WAKE_AT=""       # wake not handled yet (time of the wake)
LOCK_WAIT=2      # seconds to wait for the lock after a wake

on_wake
while :; do
    IFS= read -r -t 1 line; rc=$?
    [ $rc -ne 0 ] && [ $rc -le 128 ] && break   # journalctl ended
    [ $rc -ne 0 ] && line=""                     # timeout
    case "$line" in
        *"display state from DeepSleep to Normal"*)
            WAKE_AT=$SECONDS ;;
        *"Started reMarkable main application"*)
            LOCKED="$(started_locked)"; WOKE="$LOCKED"; WAKE_AT="" ;;
        *"lock changed: true"*)
            LOCKED=1
            [ -n "$WAKE_AT" ] && { WAKE_AT=""; WOKE=1; convert_new; } ;;
        *"lock changed: false"*)
            LOCKED=""
            [ -n "$WOKE" ] && { WOKE=""; on_wake; } ;;
    esac
    if [ -n "$WAKE_AT" ] && [ $((SECONDS - WAKE_AT)) -ge $LOCK_WAIT ]; then
        WAKE_AT=""
        if [ -n "$LOCKED" ]; then WOKE=1; convert_new; else on_wake; fi
    fi
done < <(journalctl -f -n 0 -o cat -u xochitl)
