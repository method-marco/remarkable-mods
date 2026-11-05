# kosync-native-reader

KOReader progress sync ([kosync](https://github.com/koreader/koreader-sync-server)) for
the reMarkable's **built-in** EPUB reader. Reading positions are shared with any kosync
app: KOReader on other devices, Readest, ... **Standalone**: KOReader is not needed on the
reMarkable. Requires xovi with qt-resource-rebuilder for the reader integration.

## In the built-in reader

- **Automatic**: opening an EPUB pulls (jumps to a newer position from another device),
  closing it pushes the current position. Page changes are also pushed in the background.
- **Sync panel**: in an EPUB, open the toolbar's three-dots menu (*Ebook settings*) and tap
  **Progress sync**: **Push** / **Pull** for the open book, the login status, and **Server
  settings** (server, username, password, Log in / Register / Log out). Logins are checked
  against the server. Only the md5 key is stored, never the password itself: when logged
  in, the password field shows a hidden placeholder; tap it to type a new password, or
  press Log in as-is to keep the saved login.
- **Errors** (no Wi-Fi, server unreachable, login rejected, bridge not running) are shown
  in a short notice at the bottom of the screen. A push that fails is retried
  automatically once the server is reachable again.

## How it works

| Piece | Installed to | Does |
| --- | --- | --- |
| `kosync-bridge` | `/home/root/bin/` + `kosync-bridge.service` | Background service: one static binary (Go, standard library only, source in [`bridge/`](bridge/)). Talks to the sync server, maps positions, pushes page changes, fetches newer positions every 2 minutes. |
| `kosync-native-reader.qmd` | xovi `qt-resource-rebuilder` | Sync panel (three-dots menu), auto pull on open / push on close, notices. Talks to the bridge through JSON files in `/home/root/.local/share/kosync-bridge/{cmd,result}/`. |

- **Matching books**: same partial MD5 as KOReader's "binary" document matching, so the
  same EPUB file is recognized on every device.
- **Positions are matched by text**, so all apps show the same place even though their
  layouts differ. The built-in reader shows an EPUB as a converted PDF with a text layer
  (one PDF page per reader page); KOReader positions are paths into the EPUB's XHTML
  (crengine "xpointers"). The bridge reads both itself:
  - built-in page -> KOReader: the text at the top of the page is located in the EPUB and
    turned into the exact KOReader position (plus a percentage for other apps);
  - KOReader -> built-in page: the text at KOReader's position is searched in the PDF
    pages around the expected page. The reader's pages overlap by a line, so the page
    where the text is nearest the top wins.
  - Apps that only send a percentage (e.g. Readest) are mapped by text share. Pages with
    almost no text (comics, image-only pages) fall back to the page percentage.
  - Checked against KOReader's own engines (MuPDF, crengine) on real books: PDF text is
    identical on every page, and KOReader positions are read and produced identically
    (`./build.sh` + `kosync-bridge verify`, see `bridge/verify.go`).
- **Pulling**: a server position counts as newer only if it's newer than the last time the
  book was opened or pushed on the reMarkable.
- First run: the current page of each book is taken as-is (nothing is pushed) so older
  local positions never overwrite the server.

## Files on the device

`/home/root/.local/share/kosync-bridge/`: `config.json` (server, username, md5 key),
`state.json` (device id, per-book sync state), `status.json`, `cmd/`, `result/`, `pull/`.

## Install

```sh
./deploy.sh kosync-native-reader       # restarts the reMarkable UI if xovi is running
./deploy.sh kosync-native-reader --uninstall
```

Then log in: open an EPUB > three dots > **Progress sync** > Server settings. (If KOReader
was set up for Progress sync before, the installer takes over that login once.)

Log: `journalctl -u kosync-bridge -f`. Self-test: `kosync-bridge --check` lists every EPUB
with its digest and the position of its current page.

## Build

`./build.sh` rebuilds `kosync-bridge` in Docker (golang image, no network, no third-party
code). The repository contains a prebuilt binary, so building is only needed after
changing `bridge/`.

## Notes

- The first sync of a book takes a moment (the EPUB and its PDF are read once).
- The service lives on the root filesystem: after a reMarkable OS update, re-run the
  installer. Written against OS 3.29 (Paper Pro Move).
