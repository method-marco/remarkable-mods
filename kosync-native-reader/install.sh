#!/bin/sh
# Install KOReader progress sync (kosync) for the reMarkable's built-in EPUB reader.
# Run ON THE DEVICE as root from this folder (deploy.sh in the repo root does that).
#
# Standalone: kosync-bridge is a single static binary (Go, no dependencies). KOReader
# is not needed. Needs xovi with qt-resource-rebuilder for the reader patch (sync
# panel, auto pull on open / push on close).
set -e
HERE="$(cd "$(dirname "$0")" && pwd)"
UNIT=kosync-bridge.service
QRR=/home/root/xovi/exthome/qt-resource-rebuilder
STATE=/home/root/.local/share/kosync-bridge

[ -x "$HERE/kosync-bridge" ] || chmod +x "$HERE/kosync-bridge" 2>/dev/null || true
"$HERE/kosync-bridge" --help >/dev/null 2>&1 || { echo "kosync-bridge binary doesn't run on this device (needs aarch64)."; exit 1; }

# 1. Binary
mkdir -p /home/root/bin "$STATE"
systemctl stop "$UNIT" 2>/dev/null || true
cp "$HERE/kosync-bridge" /home/root/bin/kosync-bridge.new
chmod +x /home/root/bin/kosync-bridge.new
mv /home/root/bin/kosync-bridge.new /home/root/bin/kosync-bridge

# Clean up the earlier KOReader-based version (Lua bridge, KOReader patch, caches)
rm -f /home/root/bin/kosync-bridge.lua
for d in /home/root/xovi/exthome/appload/koreader /home/root/koreader /opt/koreader; do
    rm -f "$d/patches/2-kosync-native-reader-percent.lua"
done
rm -rf "$STATE/cr3cache"

# One-time convenience: reuse an existing KOReader Progress sync login, if there is
# one and the bridge has none yet. (Not needed afterwards; log in in the reader.)
if [ ! -f "$STATE/config.json" ]; then
    for f in /home/root/xovi/exthome/appload/koreader/settings/kosync.lua /home/root/koreader/settings/kosync.lua; do
        [ -f "$f" ] || continue
        get() { sed -n "s/.*\[\"$1\"\] *= *\"\([^\"]*\)\".*/\1/p" "$f" | head -n 1; }
        user="$(get username)"; key="$(get userkey)"; server="$(get custom_server)"
        if [ -n "$user" ] && [ -n "$key" ]; then
            printf '{"server":"%s","username":"%s","userkey":"%s"}\n' \
                "${server:-https://sync.koreader.rocks}" "$user" "$key" > "$STATE/config.json"
            echo "Imported the Progress sync login ($user) from KOReader."
        fi
        break
    done
fi

# 2. systemd service on the real rootfs (on newer OS versions /etc is a RAM overlay)
UNIT_TEXT="[Unit]
Description=KOReader progress sync for the built-in EPUB reader
After=xochitl.service

[Service]
Type=simple
ExecStart=/home/root/bin/kosync-bridge
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
printf '%s\n' "$UNIT_TEXT" > "/etc/systemd/system/$UNIT"
ln -sf "/etc/systemd/system/$UNIT" "/etc/systemd/system/multi-user.target.wants/$UNIT"
systemctl daemon-reload
systemctl restart "$UNIT"

# 3. Reader patch (applied by xovi's qt-resource-rebuilder when the UI starts)
if [ -d "$QRR" ]; then
    if ! cmp -s "$HERE/kosync-native-reader.qmd" "$QRR/kosync-native-reader.qmd"; then
        cp "$HERE/kosync-native-reader.qmd" "$QRR/"
        if [ -f /etc/systemd/system/xochitl.service.d/00-xovi.conf ]; then
            echo "Restarting the reMarkable UI to load the reader patch..."
            systemctl reset-failed xochitl 2>/dev/null || true
            systemctl restart xochitl
        else
            echo "xovi is not running right now: the reader patch loads the next time you start xovi."
        fi
    fi
else
    echo "WARNING: qt-resource-rebuilder (xovi) not found - background sync works, but there is"
    echo "no sync panel and books don't jump to synced positions when opened."
fi

sleep 2
if systemctl is-active --quiet "$UNIT"; then
    echo "Installed. Log in in the built-in reader: open an EPUB > three dots > Progress sync."
    echo "Log: journalctl -u $UNIT -f"
else
    echo "Bridge failed to start:"; journalctl -u "$UNIT" -n 10 --no-pager; exit 1
fi
