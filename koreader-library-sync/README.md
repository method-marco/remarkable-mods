# koreader-library-sync

Mirrors all EPUBs from the reMarkable library into KOReader's home folder
(`/home/root/books`), every time KOReader starts.

- **Hardlinks, not copies**: no extra disk space, and KOReader treats them as normal
  files, so reading progress (`.sdr` folders) is stored in `/home/root/books`, never in
  the reMarkable's own data folder.
- **Keeps your structure**: reMarkable folders and visible titles are reproduced
  (`books/Library/The Wool Trilogy.epub`).
- **Follows changes**: deleted/trashed books disappear, renamed or moved books keep
  their KOReader progress. Only files the sync created are ever removed (tracked in
  `books/.rm-sync-manifest`), so your own files in `books/` are safe. Empty folders
  are pruned.
- **Update-proof hook**: runs from a KOReader userpatch
  (`koreader/patches/1-sync-remarkable-library.lua`), which KOReader updates don't
  overwrite (unlike `koreader.sh`). On first run it also sets KOReader's home folder to
  `/home/root/books` if none is set.

## Files

| File | Installed to |
| --- | --- |
| `rm-library-sync.sh` | `/home/root/bin/rm-library-sync.sh` |
| `1-sync-remarkable-library.lua` | `<koreader>/patches/` |

The installer auto-detects KOReader (AppLoad/xovi location, `/home/root/koreader`,
`/opt/koreader`); override with `KOREADER_DIR=/path sh install.sh`.

## Usage

- Restart KOReader once after installing. Then new books appear on every launch, or
  use **Menu > Exit > Restart KOReader** to sync immediately.
- Manual run: `sh /home/root/bin/rm-library-sync.sh` (log: `/tmp/rm-library-sync.log`).
- Also mirror PDFs: run with `EXTS="epub pdf"`, or edit the default in the script.
- Uninstall: `sh uninstall.sh` (keeps `/home/root/books`), `sh uninstall.sh --purge`
  (also deletes the mirror; the originals in the reMarkable library are untouched).
