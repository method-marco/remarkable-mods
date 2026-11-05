#!/bin/sh
# Mirror a folder of photos into full-screen sleep screen PNGs in ./images/
# (picked up by install.sh): center-crop to the screen's aspect ratio, resize, PNG.
# Run on your COMPUTER (macOS: uses sips; Linux: ImageMagick).
#
#   ./prepare-images.sh [WIDTHxHEIGHT] <folder>
#
# The source folder is the only source of truth:
#   - already converted and unchanged  -> left alone
#   - new or changed photo             -> converted
#   - PNG whose photo is gone          -> deleted
# Takes .jpg .jpeg .png .heic (any case) directly inside <folder>.
# Screen sizes (portrait):
#   reMarkable Paper Pro Move   954x1696   (default)
#   reMarkable Paper Pro       1620x2160
#   reMarkable 2               1404x1872
# Not sure? On the device: the size of /usr/share/remarkable/suspended.png.
set -e
HERE="$(cd "$(dirname "$0")" && pwd)"
OUT="$HERE/images"
SIZE=954x1696

usage() { sed -n 2,17p "$0" | sed 's/^# \{0,1\}//'; exit 1; }
case "$1" in [0-9]*x[0-9]*) SIZE="$1"; shift ;; esac
[ $# -eq 1 ] || usage
SRC="${1%/}"
[ -d "$SRC" ] || { echo "Not a folder: $SRC"; echo; usage; }
W=${SIZE%x*}; H=${SIZE#*x}

convert_one() {  # $1 = source photo, $2 = output png
    tmp="$2.tmp.png"
    if command -v sips >/dev/null 2>&1; then
        iw=$(sips -g pixelWidth "$1" | awk '/pixelWidth/{print $2}')
        ih=$(sips -g pixelHeight "$1" | awk '/pixelHeight/{print $2}')
        # largest crop box with the screen's aspect ratio
        if [ $((iw * H)) -gt $((ih * W)) ]; then ch=$ih; cw=$((ih * W / H)); else cw=$iw; ch=$((iw * H / W)); fi
        sips -s format png --cropToHeightWidth "$ch" "$cw" "$1" --out "$tmp" >/dev/null
        sips -z "$H" "$W" "$tmp" >/dev/null
    elif command -v magick >/dev/null 2>&1; then
        magick "$1" -auto-orient -resize "${W}x${H}^" -gravity center -extent "${W}x${H}" "$tmp"
    elif command -v convert >/dev/null 2>&1; then
        convert "$1" -auto-orient -resize "${W}x${H}^" -gravity center -extent "${W}x${H}" "$tmp"
    else
        echo "Need sips (macOS) or ImageMagick."; exit 1
    fi
    mv -f "$tmp" "$2"
}

mkdir -p "$OUT"
# A different screen size invalidates every existing PNG.
if [ "$(cat "$OUT/.size" 2>/dev/null)" != "$SIZE" ]; then
    rm -f "$OUT"/*.png
    echo "$SIZE" > "$OUT/.size"
fi

KEEP="$OUT/.keep.$$"; : > "$KEEP"
trap 'rm -f "$KEEP"' EXIT
added=0; kept=0; removed=0

for f in "$SRC"/*; do
    [ -f "$f" ] || continue
    case "$(printf '%s' "$f" | tr '[:upper:]' '[:lower:]')" in
        *.jpg|*.jpeg|*.png|*.heic) ;;
        *) continue ;;
    esac
    name="$(basename "${f%.*}").png"
    if grep -qxF "$name" "$KEEP"; then
        echo "SKIP $(basename "$f"): another photo already produces $name (rename one of them)"
        continue
    fi
    echo "$name" >> "$KEEP"
    if [ -f "$OUT/$name" ] && ! [ "$f" -nt "$OUT/$name" ]; then
        kept=$((kept + 1))
    else
        convert_one "$f" "$OUT/$name"
        echo "add    $name"
        added=$((added + 1))
    fi
done

for p in "$OUT"/*.png; do
    [ -f "$p" ] || continue
    if ! grep -qxF "$(basename "$p")" "$KEEP"; then
        rm -f "$p"; echo "delete $(basename "$p")"
        removed=$((removed + 1))
    fi
done

total=$(grep -c . "$KEEP" || true)
[ "$total" -gt 0 ] || { echo "No photos (.jpg .jpeg .png .heic) found in $SRC"; exit 1; }
echo "Done: $total picture(s) at ${W}x${H} ($added added, $kept unchanged, $removed deleted) in $OUT"
echo "Next: ./deploy.sh sleepscreen-rotate"
