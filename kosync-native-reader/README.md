# kosync-native-reader

KOReader progress sync ([kosync](https://github.com/koreader/koreader-sync-server)) for
the reMarkable's **built-in** EPUB reader. Reading positions are shared with KOReader on
the reMarkable and with any other kosync app (KOReader on other devices, Readest, ...).
Uses the server and login you set up in KOReader (Tools > Progress sync).

## In the built-in reader

- **Automatic**: opening an EPUB pulls (jumps to a newer position from another device),
  closing it pushes the current position.
- **Sync panel**: in an EPUB, open the toolbar's three-dots menu (*Ebook settings*) and tap
  **Progress sync**. It has **Push** / **Pull** for the open book, the login status, and
  **Server settings** (server, username, password, Log in / Register / Log out). Logins are
  checked against the server and saved in KOReader's Progress sync settings, so both apps
  use the same account. Only an md5 key is stored, never the password itself: when logged
  in, the password field shows a hidden placeholder; tap it to type a new password, or
  press Log in as-is to keep the saved login.
- **Errors** (no Wi-Fi, server unreachable, login rejected, bridge not running) are shown
  in a short notice at the bottom of the screen. A push that fails is retried
  automatically once the server is reachable again.

## How it works

| Piece | Installed to | Does |
| --- | --- | --- |
| `kosync-bridge.lua` | `/home/root/bin/` + `kosync-bridge.service` | Background service (runs on KOReader's LuaJIT). Pushes the built-in reader's position when it changes; every 2 minutes it fetches newer positions from the server. |
| `kosync-native-reader.qmd` | xovi `qt-resource-rebuilder` | Sync panel (three-dots menu), auto pull on open / push on close, notices. Talks to the bridge through JSON files in `/home/root/.local/share/kosync-bridge/{cmd,result}/`. |
| `2-kosync-native-reader-percent.lua` | `<koreader>/patches/` | KOReader on this device jumps to the exact percentage of positions from the built-in reader. |

- **Matching books**: same partial MD5 as KOReader's "binary" document matching. Books from
  the reMarkable library match the copies in KOReader (`koreader-library-sync` hardlinks
  them) and the same EPUB file on other devices.
- **Positions**: the built-in reader shows an EPUB as converted pages, so its position is
  `page / pageCount`. Pushed as that percentage plus a KOReader position at the start of
  the matching chapter (estimated from the chapter's size). Other KOReader devices land at
  that chapter start; KOReader on this device (with the patch) and percentage-based apps
  (e.g. Readest) land at the percentage.
- **Pulling**: a server position counts as newer only if it's newer than the last time the
  book was opened or pushed on the reMarkable. The bridge leaves a note in
  `/home/root/.local/share/kosync-bridge/pull/`; the reader patch applies it when the book
  is opened (not while it's already open), then clears it.
- First run: the current page of each book is taken as-is (nothing is pushed) so older
  local positions never overwrite the server.

## Install

```sh
./deploy.sh kosync-native-reader       # restarts the reMarkable UI if xovi is running
./deploy.sh kosync-native-reader --uninstall
```

Then restart KOReader once (for its patch). Log: `journalctl -u kosync-bridge -f`.
Offline self-test (book digests and chapter mapping):
`cd <koreader> && ./luajit /home/root/bin/kosync-bridge.lua --check`

## Notes

- Positions are approximate: the built-in reader's page layout differs from KOReader's.
- The service lives on the root filesystem: after a reMarkable OS update, re-run the
  installer. Written against OS 3.29 (Paper Pro Move).
