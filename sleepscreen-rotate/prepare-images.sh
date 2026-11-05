#!/bin/sh
# Turn photos into full-screen sleep screen PNGs: center-crop to the screen's
# aspect ratio, resize, save as PNG into ./images/ (picked up by install.sh).
# Run on your COMPUTER (macOS: uses sips; Linux: ImageMagick).
#
#   ./prepare-images.sh [WIDTHxHEIGHT] photo1.jpg photo2.jpg ...
#
# Screen sizes (portrait):
#   reMarkable Paper Pro Move   954x1696   (default)
#   reMarkable Paper Pro       1620x2160
#   reMarkable 2               1404x1872
# Not sure? On the device: the size of /usr/share/remarkable/suspended.png.
set -e
HERE="$(cd "$(dirname "$0")" && pwd)"
OUT="$HERE/images"
SIZE=954x1696
case "$1" in *x*) [ -f "$1" ] || { SIZE="$1"; shift; } ;; esac
[ $# -gt 0 ] || { sed -n 2,13p "$0" | sed 's/^# \{0,1\}//'; exit 1; }
W=${SIZE%x*}; H=${SIZE#*x}
mkdir -p "$OUT"

for f in "$@"; do
    name="$(basename "${f%.*}").png"
    if command -v sips >/dev/null 2>&1; then
        iw=$(sips -g pixelWidth "$f" | awk '/pixelWidth/{print $2}')
        ih=$(sips -g pixelHeight "$f" | awk '/pixelHeight/{print $2}')
        # crop box with the screen's aspect ratio, as large as possible
        if [ $((iw * H)) -gt $((ih * W)) ]; then ch=$ih; cw=$((ih * W / H)); else cw=$iw; ch=$((iw * H / W)); fi
        sips -s format png --cropToHeightWidth "$ch" "$cw" "$f" --out "$OUT/$name" >/dev/null
        sips -z "$H" "$W" "$OUT/$name" >/dev/null
    elif command -v magick >/dev/null 2>&1; then
        magick "$f" -auto-orient -resize "${W}x${H}^" -gravity center -extent "${W}x${H}" "$OUT/$name"
    elif command -v convert >/dev/null 2>&1; then
        convert "$f" -auto-orient -resize "${W}x${H}^" -gravity center -extent "${W}x${H}" "$OUT/$name"
    else
        echo "Need sips (macOS) or ImageMagick."; exit 1
    fi
    echo "$f -> images/$name"
done
echo "Done. Check the crops in $OUT, then deploy."
