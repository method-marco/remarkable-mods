#!/bin/sh
# Remove the sleep screen rotation and restore the stock sleep screen.
# Run ON THE DEVICE as root. Restarts the reMarkable UI once.
# Pass --purge to also delete /home/root/sleepscreens (your pictures).
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
rm -f /home/root/bin/sleepscreen-rotate.sh

if grep -q "^SleepScreenPath=" "$CONF"; then
    echo "Restarting the reMarkable UI to restore the stock sleep screen..."
    systemctl stop xochitl
    awk '!/^(SleepScreenPath|ShowSleepScreenCarousel)=/' "$CONF" > "$CONF.new" && mv "$CONF.new" "$CONF"
    systemctl start xochitl
fi

[ "$1" = "--purge" ] && rm -rf /home/root/sleepscreens && echo "Pictures deleted."
echo "Removed."
