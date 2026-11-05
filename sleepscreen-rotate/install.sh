#!/bin/sh
# Install the random sleep screen rotation on a reMarkable. Run ON THE DEVICE as
# root from this folder (deploy.sh in the repo root does that for you over SSH).
#
# - Pictures: any PNGs in ./images/ are copied to /home/root/sleepscreens/.
#   They should match the screen size (see prepare-images.sh).
# - Sets xochitl's custom sleep screen (SleepScreenPath) to
#   /home/root/sleepscreens/.current.png. This RESTARTS the reMarkable UI once.
# - Installs a systemd service on the real root filesystem so it survives
#   reboots. A reMarkable OS update removes it again: just re-run this script.
set -e
HERE="$(cd "$(dirname "$0")" && pwd)"
DIR=/home/root/sleepscreens
CONF=/home/root/.config/remarkable/xochitl.conf
UNIT=sleepscreen-rotate.service

if ! strings /usr/bin/xochitl 2>/dev/null | grep -qx SleepScreenPath; then
    echo "This reMarkable OS has no custom sleep screen support (SleepScreenPath). Aborting."
    exit 1
fi

# 1. Pictures: mirror ./images/ into $DIR. ./images/ is the only source:
#    identical pictures are left alone, missing/changed ones copied, all others deleted.
#    Without ./images/ the pictures already on the device are left untouched.
mkdir -p "$DIR" /home/root/bin
if ls "$HERE"/images/*.png >/dev/null 2>&1; then
    added=0; kept=0; removed=0
    for f in "$HERE"/images/*.png; do
        dst="$DIR/$(basename "$f")"
        if [ -f "$dst" ] && cmp -s "$f" "$dst"; then
            kept=$((kept + 1))
        else
            cp "$f" "$dst.tmp" && mv -f "$dst.tmp" "$dst"
            echo "add    $(basename "$f")"; added=$((added + 1))
        fi
    done
    for p in "$DIR"/*.png; do
        [ -f "$p" ] || continue
        if [ ! -f "$HERE/images/$(basename "$p")" ]; then
            rm -f "$p"; echo "delete $(basename "$p")"; removed=$((removed + 1))
        fi
    done
    # Forget the last pick if that picture was deleted.
    [ -f "$DIR/.last" ] && [ ! -f "$(cat "$DIR/.last")" ] && rm -f "$DIR/.last"
    echo "Pictures: $added added, $kept unchanged, $removed deleted"
else
    echo "No ./images/ to install (run prepare-images.sh first) - pictures on the device left as they are."
fi
ls "$DIR"/*.png >/dev/null 2>&1 || echo "WARNING: no PNGs in $DIR yet - add some, rotation starts once there are pictures."
[ -f "$DIR/.current.png" ] || cp /usr/share/remarkable/suspended.png "$DIR/.current.png"
cp "$HERE/sleepscreen-rotate.sh" /home/root/bin/sleepscreen-rotate.sh
chmod +x /home/root/bin/sleepscreen-rotate.sh

# 2. systemd unit on the real rootfs (on newer OS versions /etc is a RAM overlay,
#    so writing to /etc directly would be lost at reboot)
ROOT=/tmp/rootfs-sleepscreen
mkdir -p "$ROOT"
mount -o remount,rw /
mount --bind / "$ROOT"
cp "$HERE/$UNIT" "$ROOT/etc/systemd/system/$UNIT"
mkdir -p "$ROOT/etc/systemd/system/multi-user.target.wants"
ln -sf "/etc/systemd/system/$UNIT" "$ROOT/etc/systemd/system/multi-user.target.wants/$UNIT"
sync
umount "$ROOT"; rmdir "$ROOT"
mount -o remount,ro / 2>/dev/null || true
# A running overlay doesn't see files added underneath it: add them for this boot too.
if [ ! -e "/etc/systemd/system/$UNIT" ]; then
    cp "$HERE/$UNIT" "/etc/systemd/system/$UNIT"
    ln -sf "/etc/systemd/system/$UNIT" "/etc/systemd/system/multi-user.target.wants/$UNIT"
fi
systemctl daemon-reload

# 3. Point xochitl at the rotating picture (xochitl must be stopped while editing)
if ! grep -qx "SleepScreenPath=$DIR/.current.png" "$CONF" || ! grep -qx "ShowSleepScreenCarousel=false" "$CONF"; then
    [ -f "$CONF.bak-before-sleepscreen" ] || cp "$CONF" "$CONF.bak-before-sleepscreen"
    echo "Restarting the reMarkable UI to apply the sleep screen setting..."
    systemctl stop xochitl
    awk -v p="$DIR/.current.png" '
        /^(SleepScreenPath|ShowSleepScreenCarousel)=/ { next }
        { print }
        /^\[General\]$/ && !done { print "ShowSleepScreenCarousel=false"; print "SleepScreenPath=" p; done = 1 }
    ' "$CONF" > "$CONF.new" && mv "$CONF.new" "$CONF"
    systemctl start xochitl
fi

# 4. Start rotating
systemctl restart "$UNIT"
sleep 2
systemctl is-active --quiet "$UNIT" && echo "Installed and running." || { echo "Service failed to start:"; journalctl -u "$UNIT" -n 10 --no-pager; exit 1; }
echo "Pictures: $DIR  (add/remove PNGs any time)"
