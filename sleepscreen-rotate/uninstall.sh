#!/bin/sh
# Remove the sleep screen rotation and restore the stock sleep screen.
# Run ON THE DEVICE as root. Restarts the reMarkable UI once.
# Pass --purge to also delete /home/root/sleepscreens (converted copies only).
UNIT=sleepscreen-rotate.service
CONF=/home/root/.config/remarkable/xochitl.conf

systemctl disable --now "$UNIT" 2>/dev/null
rm -f "/etc/systemd/system/$UNIT" "/etc/systemd/system/multi-user.target.wants/$UNIT"

ROOT=/tmp/rootfs-sleepscreen
mkdir -p "$ROOT"
mount -o remount,rw / && mount --bind / "$ROOT" && {
    rm -f "$ROOT/etc/systemd/system/$UNIT" "$ROOT/etc/systemd/system/multi-user.target.wants/$UNIT"
    sync; umount "$ROOT"
}
rmdir "$ROOT" 2>/dev/null
mount -o remount,ro / 2>/dev/null
systemctl daemon-reload
rm -f /home/root/bin/sleepscreen-rotate.sh /home/root/bin/sleepscreen-library
rm -rf /home/root/sleepscreens/library  # converted copies only; the library documents stay
QMD=/home/root/xovi/exthome/qt-resource-rebuilder/sleepscreen-rotate.qmd
PATCH_REMOVED=""; [ -f "$QMD" ] && { rm -f "$QMD"; PATCH_REMOVED=1; }

INSECURE=/data/xochitl.conf  # read instead of $CONF until the first unlock (passcode)
if grep -q "^SleepScreenPath=" "$CONF" "$INSECURE" 2>/dev/null; then
    echo "Restarting the reMarkable UI to restore the stock sleep screen..."
    systemctl stop xochitl
    for f in "$CONF" "$INSECURE"; do
        [ -f "$f" ] || continue
        awk '!/^(SleepScreenPath|ShowSleepScreenCarousel)=/' "$f" > "$f.new" && mv "$f.new" "$f"
    done
    systemctl start xochitl
elif [ -n "$PATCH_REMOVED" ] && [ -f /etc/systemd/system/xochitl.service.d/00-xovi.conf ]; then
    systemctl reset-failed xochitl 2>/dev/null
    systemctl restart xochitl
fi

[ "$1" = "--purge" ] && rm -rf /home/root/sleepscreens && echo "Pictures deleted."
echo "Removed."
