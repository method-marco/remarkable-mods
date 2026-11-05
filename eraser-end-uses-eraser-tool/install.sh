#!/bin/sh
# Make the eraser end of the Marker use the Eraser tool settings. Run ON THE DEVICE as root from
# this folder (deploy.sh in the repo root does that). Needs xovi + qt-resource-rebuilder.
# Restarts the reMarkable UI if xovi is running.
set -e
HERE="$(cd "$(dirname "$0")" && pwd)"
QRR=/home/root/xovi/exthome/qt-resource-rebuilder
[ -d "$QRR" ] || { echo "qt-resource-rebuilder (xovi) not found."; exit 1; }
cp "$HERE/eraser-end-uses-eraser-tool.qmd" "$QRR/"
if [ -f /etc/systemd/system/xochitl.service.d/00-xovi.conf ]; then
    echo "Restarting the reMarkable UI to load the patch..."
    systemctl reset-failed xochitl 2>/dev/null || true
    systemctl restart xochitl
    echo "Installed."
else
    echo "Installed. xovi is not running right now: the patch loads the next time you start xovi."
fi
