#!/bin/sh
# Install the KOReader library sync on a reMarkable. Run ON THE DEVICE as root
# from this folder (deploy.sh in the repo root does that for you over SSH).
#
#   KOREADER_DIR=/path/to/koreader sh install.sh   # override auto-detection
set -e
HERE="$(cd "$(dirname "$0")" && pwd)"

find_koreader() {
    for d in "$KOREADER_DIR" \
             /home/root/xovi/exthome/appload/koreader \
             /home/root/koreader \
             /opt/koreader; do
        [ -n "$d" ] && [ -f "$d/reader.lua" ] && { echo "$d"; return; }
    done
    d="$(find /home /opt -maxdepth 6 -name reader.lua -path '*koreader*' 2>/dev/null | sed -n 1p)"
    [ -n "$d" ] && dirname "$d"
}

KO="$(find_koreader)"
[ -n "$KO" ] || { echo "KOReader not found. Set KOREADER_DIR=/path/to/koreader"; exit 1; }
echo "KOReader: $KO"

mkdir -p /home/root/bin "$KO/patches"
cp "$HERE/rm-library-sync.sh" /home/root/bin/rm-library-sync.sh
chmod +x /home/root/bin/rm-library-sync.sh
cp "$HERE/1-sync-remarkable-library.lua" "$KO/patches/1-sync-remarkable-library.lua"

echo "Running a first sync..."
sh /home/root/bin/rm-library-sync.sh || true
tail -n 1 /tmp/rm-library-sync.log

cat <<EOF

Installed.
  sync script : /home/root/bin/rm-library-sync.sh
  KOReader hook: $KO/patches/1-sync-remarkable-library.lua
  books folder : /home/root/books

Fully exit and restart KOReader once: from then on the library syncs on every
launch and /home/root/books becomes KOReader's home folder (if none is set).
EOF
