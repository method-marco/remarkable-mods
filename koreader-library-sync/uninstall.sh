#!/bin/sh
# Remove the KOReader library sync. Run ON THE DEVICE as root.
# Keeps /home/root/books (your KOReader progress lives there); pass --purge to
# also delete the mirrored books (the originals in the reMarkable library are untouched).
for d in "$KOREADER_DIR" /home/root/xovi/exthome/appload/koreader /home/root/koreader /opt/koreader; do
    [ -n "$d" ] && rm -f "$d/patches/1-sync-remarkable-library.lua"
done
rm -f /home/root/bin/rm-library-sync.sh /tmp/rm-library-sync.log

if [ "$1" = "--purge" ]; then
    rm -rf /home/root/books
    echo "Removed, including /home/root/books."
else
    echo "Removed. /home/root/books was kept (use --purge to delete it)."
fi
echo "KOReader's home folder setting still points to /home/root/books; change it in KOReader if needed."
