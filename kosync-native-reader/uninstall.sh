#!/bin/sh
# Remove KOReader progress sync for the built-in reader. Run ON THE DEVICE as root.
# Restarts the reMarkable UI if xovi is running (to unload the reader patch).
# Pass --purge to also delete the login and sync state (/home/root/.local/share/kosync-bridge).
UNIT=kosync-bridge.service
QRR=/home/root/xovi/exthome/qt-resource-rebuilder

systemctl disable --now "$UNIT" 2>/dev/null
rm -f "/etc/systemd/system/$UNIT" "/etc/systemd/system/multi-user.target.wants/$UNIT"
ROOT=/tmp/rootfs-kosync-bridge
mkdir -p "$ROOT"
mount -o remount,rw / && mount --bind / "$ROOT" && {
    rm -f "$ROOT/etc/systemd/system/$UNIT" "$ROOT/etc/systemd/system/multi-user.target.wants/$UNIT"
    sync; umount "$ROOT"
}
rmdir "$ROOT" 2>/dev/null
mount -o remount,ro / 2>/dev/null
systemctl daemon-reload

rm -f /home/root/bin/kosync-bridge /home/root/bin/kosync-bridge.lua
for d in /home/root/xovi/exthome/appload/koreader /home/root/koreader /opt/koreader; do
    rm -f "$d/patches/2-kosync-native-reader-percent.lua"
done
if [ "$1" = "--purge" ]; then
    rm -rf /home/root/.local/share/kosync-bridge
else
    rm -rf /home/root/.local/share/kosync-bridge/cmd /home/root/.local/share/kosync-bridge/result \
           /home/root/.local/share/kosync-bridge/pull /home/root/.local/share/kosync-bridge/cr3cache
fi

if [ -f "$QRR/kosync-native-reader.qmd" ]; then
    rm -f "$QRR/kosync-native-reader.qmd"
    if [ -f /etc/systemd/system/xochitl.service.d/00-xovi.conf ]; then
        systemctl reset-failed xochitl 2>/dev/null
        systemctl restart xochitl
    fi
fi
echo "Removed."
