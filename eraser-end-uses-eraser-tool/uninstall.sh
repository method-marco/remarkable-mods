#!/bin/sh
# Remove the eraser-end patch. Run ON THE DEVICE as root.
QRR=/home/root/xovi/exthome/qt-resource-rebuilder
rm -f "$QRR/eraser-end-uses-eraser-tool.qmd"
if [ -f /etc/systemd/system/xochitl.service.d/00-xovi.conf ]; then
    systemctl reset-failed xochitl 2>/dev/null
    systemctl restart xochitl
fi
echo "Removed."
