#!/bin/sh
# Install the random sleep screen rotation on a reMarkable. Run ON THE DEVICE as
# root from this folder (deploy.sh in the repo root does that for you over SSH).
#
# - Pictures: the images in the library folder "Screensaver" (converted on the
#   device by sleepscreen-library), kept in sync with that folder.
# - Sets xochitl's custom sleep screen (SleepScreenPath) to
#   /home/root/sleepscreens/.current.png. This RESTARTS the reMarkable UI once.
# - With xovi + qt-resource-rebuilder: the passcode entry on wake shows the current
#   picture as its background (sleepscreen-rotate.qmd).
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

# 1. Pictures come only from the library folder "Screensaver" (sleepscreen-library
#    converts them into $DIR/library/). PNGs left from older versions are removed.
mkdir -p "$DIR" /home/root/bin
rm -f "$DIR"/*.png "$DIR/.last"
[ -f "$DIR/.current.png" ] || cp /usr/share/remarkable/suspended.png "$DIR/.current.png"
cp "$HERE/sleepscreen-rotate.sh" /home/root/bin/sleepscreen-rotate.sh
chmod +x /home/root/bin/sleepscreen-rotate.sh
cp "$HERE/sleepscreen-library" /home/root/bin/sleepscreen-library.new
chmod +x /home/root/bin/sleepscreen-library.new
mv -f /home/root/bin/sleepscreen-library.new /home/root/bin/sleepscreen-library

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

# 3. Passcode screen patch (xovi qt-resource-rebuilder, optional): shows the
#    current picture behind the passcode entry on wake.
QRR=/home/root/xovi/exthome/qt-resource-rebuilder
XOVI_ACTIVE=""; [ -f /etc/systemd/system/xochitl.service.d/00-xovi.conf ] && XOVI_ACTIVE=1
PATCH_CHANGED=""
if [ -d "$QRR" ]; then
    cmp -s "$HERE/sleepscreen-rotate.qmd" "$QRR/sleepscreen-rotate.qmd" || PATCH_CHANGED=1
    cp "$HERE/sleepscreen-rotate.qmd" "$QRR/"
else
    echo "xovi qt-resource-rebuilder not found: the passcode screen keeps its stock look."
fi

# 4. Point xochitl at the rotating picture (xochitl must be stopped while editing)
# With a passcode, xochitl reads the setting from /data/xochitl.conf (outside the
# encrypted user data) until the first unlock, so it's set there too.
INSECURE=/data/xochitl.conf
conf_ok() { grep -qx "SleepScreenPath=$DIR/.current.png" "$1" && grep -qx "ShowSleepScreenCarousel=false" "$1"; }
set_conf() {
    awk -v p="$DIR/.current.png" '
        /^(SleepScreenPath|ShowSleepScreenCarousel)=/ { next }
        { print }
        /^\[General\]$/ && !done { print "ShowSleepScreenCarousel=false"; print "SleepScreenPath=" p; done = 1 }
    ' "$1" > "$1.new" && mv "$1.new" "$1"
}
if ! conf_ok "$CONF" || { [ -f "$INSECURE" ] && ! conf_ok "$INSECURE"; }; then
    [ -f "$CONF.bak-before-sleepscreen" ] || cp "$CONF" "$CONF.bak-before-sleepscreen"
    echo "Restarting the reMarkable UI to apply the sleep screen setting..."
    systemctl stop xochitl
    set_conf "$CONF"
    if [ -f "$INSECURE" ]; then
        [ -f "$INSECURE.bak-before-sleepscreen" ] || cp "$INSECURE" "$INSECURE.bak-before-sleepscreen"
        set_conf "$INSECURE"
    fi
    systemctl reset-failed xochitl 2>/dev/null || true
    systemctl start xochitl
elif [ -n "$PATCH_CHANGED" ] && [ -n "$XOVI_ACTIVE" ]; then
    echo "Restarting the reMarkable UI to load the passcode screen patch..."
    systemctl reset-failed xochitl 2>/dev/null || true
    systemctl restart xochitl
fi

# 5. Start rotating
systemctl restart "$UNIT"
sleep 2
systemctl is-active --quiet "$UNIT" && echo "Installed and running." || { echo "Service failed to start:"; journalctl -u "$UNIT" -n 10 --no-pager; exit 1; }
echo "Pictures: the images in the library folder \"Screensaver\" (checked at every wake)"
echo "Log: journalctl -u $UNIT -f"
