#!/bin/sh
# Mirror EPUBs from the reMarkable (xochitl) library into a KOReader home folder.
#
# - Books are HARDLINKED (no extra disk space, KOReader sees regular files).
# - reMarkable folder hierarchy and visible titles are preserved.
# - Trashed/deleted books are removed from the mirror; renamed/moved books
#   keep their KOReader progress (.sdr sidecar is moved along).
# - Only files this script created are ever removed (tracked in a manifest),
#   so anything you put into the books folder yourself is left alone.

XOCHITL="${XOCHITL:-/home/root/.local/share/remarkable/xochitl}"
BOOKS="${BOOKS:-/home/root/books}"
EXTS="${EXTS:-epub}"            # e.g. EXTS="epub pdf" to also mirror PDFs
MANIFEST="$BOOKS/.rm-sync-manifest"
LOG="${LOG:-/tmp/rm-library-sync.log}"

exec >"$LOG" 2>&1
echo "rm-library-sync: $(date)"

mkdir -p "$BOOKS" || exit 1
[ -d "$XOCHITL" ] || { echo "no xochitl dir"; exit 1; }

# Read a top-level string field from a .metadata JSON file.
field() {
    sed -n "s/^ *\"$2\": *\"\(.*\)\",\{0,1\} *\$/\1/p" "$1" | sed -n 1p |
        sed 's/\\"/"/g; s/\\\\/\\/g'
}

# Make a string safe as a single path component.
clean() {
    printf '%s' "$1" | tr '/\000-\037' '_' | sed 's/^\.*//; s/[ .]*$//' | cut -c1-200
}

# Resolve a folder uuid to a relative path ("" for root, "TRASH" if trashed).
folder_path() {
    _id="$1"; _p=""; _depth=0
    while [ -n "$_id" ] && [ $_depth -lt 32 ]; do
        [ "$_id" = "trash" ] && { echo TRASH; return; }
        _m="$XOCHITL/$_id.metadata"
        [ -f "$_m" ] || break
        grep -q '"deleted": *true' "$_m" && { echo TRASH; return; }
        _n="$(clean "$(field "$_m" visibleName)")"
        [ -n "$_n" ] || _n="$_id"
        _p="$_n${_p:+/$_p}"
        _id="$(field "$_m" parent)"
        _depth=$((_depth + 1))
    done
    echo "$_p"
}

NEW="$MANIFEST.new"
: >"$NEW"
seen_paths="$BOOKS/.rm-sync-seen"
: >"$seen_paths"

for ext in $EXTS; do
    for src in "$XOCHITL"/*."$ext"; do
        [ -f "$src" ] || continue
        uuid="$(basename "$src" ".$ext")"
        meta="$XOCHITL/$uuid.metadata"
        [ -f "$meta" ] || continue
        grep -q '"deleted": *true' "$meta" && continue

        dir="$(folder_path "$(field "$meta" parent)")"
        [ "$dir" = "TRASH" ] && continue

        name="$(clean "$(field "$meta" visibleName)")"
        [ -n "$name" ] || name="$uuid"
        rel="${dir:+$dir/}$name.$ext"
        # Disambiguate duplicate titles in the same folder.
        if grep -qxF "$rel" "$seen_paths"; then
            rel="${dir:+$dir/}$name ($(echo "$uuid" | cut -c1-8)).$ext"
        fi
        echo "$rel" >>"$seen_paths"
        dst="$BOOKS/$rel"

        # Book was renamed/moved since the last sync: carry KOReader sidecar along.
        old="$(grep "^$uuid	" "$MANIFEST" 2>/dev/null | cut -f2- | sed -n 1p)"
        if [ -n "$old" ] && [ "$old" != "$rel" ]; then
            oldsdr="$BOOKS/${old%.*}.sdr"
            newsdr="${dst%.*}.sdr"
            if [ -d "$oldsdr" ] && [ ! -e "$newsdr" ]; then
                mkdir -p "$(dirname "$newsdr")"
                mv "$oldsdr" "$newsdr" && echo "moved sidecar: $old -> $rel"
            fi
        fi

        mkdir -p "$(dirname "$dst")"
        if [ ! "$src" -ef "$dst" ]; then
            rm -f "$dst"
            ln "$src" "$dst" 2>/dev/null || cp -p "$src" "$dst"
            echo "linked: $rel"
        fi
        printf '%s\t%s\n' "$uuid" "$rel" >>"$NEW"
    done
done

# Remove books we created earlier that no longer exist in the library.
if [ -f "$MANIFEST" ]; then
    cut -f2- "$MANIFEST" | while IFS= read -r rel; do
        [ -n "$rel" ] || continue
        cut -f2- "$NEW" | grep -qxF "$rel" && continue
        rm -f "$BOOKS/$rel" && echo "removed: $rel"
    done
fi
mv "$NEW" "$MANIFEST"
rm -f "$seen_paths"

# Prune empty folders (never sidecars or the root).
find "$BOOKS" -mindepth 1 -depth -type d -empty ! -name '*.sdr' -exec rmdir {} \; 2>/dev/null

echo "done: $(wc -l <"$MANIFEST") book(s) mirrored into $BOOKS"
exit 0
