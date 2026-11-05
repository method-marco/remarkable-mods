#!/bin/sh
# Install KOReader progress sync for the reMarkable's built-in EPUB reader.
# Run ON THE DEVICE as root from this folder (deploy.sh in the repo root does that).
#
# Needs: KOReader with a Progress sync login (Tools > Progress sync), and xovi with
# qt-resource-rebuilder for the reader patch.
#
#   KOREADER_DIR=/path/to/koreader sh install.sh   # override auto-detection
set -e
HERE="$(cd "$(dirname "$0")" && pwd)"
UNIT=kosync-bridge.service
QRR=/home/root/xovi/exthome/qt-resource-rebuilder

find_koreader() {
    for d in "$KOREADER_DIR" /home/root/xovi/exthome/appload/koreader /home/root/koreader /opt/koreader; do
        [ -n "$d" ] && [ -f "$d/reader.lua" ] && { echo "$d"; return; }
    done
    d="$(find /home /opt -maxdepth 6 -name reader.lua -path '*koreader*' 2>/dev/null | sed -n 1p)"
    [ -n "$d" ] && dirname "$d"
}
KO="$(find_koreader)"
[ -n "$KO" ] || { echo "KOReader not found. Set KOREADER_DIR=/path/to/koreader"; exit 1; }
echo "KOReader: $KO"
grep -q '"userkey"' "$KO/settings/kosync.lua" 2>/dev/null \
    || echo "NOTE: KOReader has no Progress sync login yet - log in under Tools > Progress sync; the bridge waits until then."

# 1. Bridge + KOReader patch
mkdir -p /home/root/bin "$KO/patches"
cp "$HERE/kosync-bridge.lua" /home/root/bin/kosync-bridge.lua
cp "$HERE/2-kosync-native-reader-percent.lua" "$KO/patches/"

# 2. systemd service on the real rootfs (on newer OS versions /etc is a RAM overlay)
UNIT_TEXT="[Unit]
Description=KOReader progress sync for the built-in EPUB reader
After=xochitl.service

[Service]
Type=simple
WorkingDirectory=$KO
ExecStart=$KO/luajit /home/root/bin/kosync-bridge.lua
Restart=always
RestartSec=30

[Install]
WantedBy=multi-user.target"
ROOT=/tmp/rootfs-kosync-bridge
mkdir -p "$ROOT"
mount -o remount,rw /
mount --bind / "$ROOT"
printf '%s\n' "$UNIT_TEXT" > "$ROOT/etc/systemd/system/$UNIT"
mkdir -p "$ROOT/etc/systemd/system/multi-user.target.wants"
ln -sf "/etc/systemd/system/$UNIT" "$ROOT/etc/systemd/system/multi-user.target.wants/$UNIT"
sync; umount "$ROOT"; rmdir "$ROOT"
mount -o remount,ro / 2>/dev/null || true
# A running /etc overlay doesn't see files added underneath it: add them for this boot.
if ! cmp -s "/etc/systemd/system/$UNIT" - <<EOF
$UNIT_TEXT
EOF
then
    printf '%s\n' "$UNIT_TEXT" > "/etc/systemd/system/$UNIT"
    ln -sf "/etc/systemd/system/$UNIT" "/etc/systemd/system/multi-user.target.wants/$UNIT"
fi
systemctl daemon-reload
systemctl restart "$UNIT"

# 3. Reader patch (applied by xovi's qt-resource-rebuilder when the UI starts)
if [ -d "$QRR" ]; then
    cp "$HERE/kosync-native-reader.qmd" "$QRR/"
    if [ -f /etc/systemd/system/xochitl.service.d/00-xovi.conf ]; then
        echo "Restarting the reMarkable UI to load the reader patch..."
        systemctl reset-failed xochitl 2>/dev/null || true
        systemctl restart xochitl
    else
        echo "xovi is not running right now: the reader patch loads the next time you start xovi."
    fi
else
    echo "WARNING: qt-resource-rebuilder not found - pushing works, but books won't open at synced positions."
fi

sleep 2
systemctl is-active --quiet "$UNIT" && echo "Installed. Bridge log: journalctl -u $UNIT -f" \
    || { echo "Bridge failed to start:"; journalctl -u "$UNIT" -n 10 --no-pager; exit 1; }
echo "Restart KOReader once to load its patch."
