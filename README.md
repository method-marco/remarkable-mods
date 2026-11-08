# remarkable-mods

Small mods for a reMarkable in developer mode (tested on a **reMarkable Paper Pro Move**,
OS 3.29, with xovi + AppLoad + KOReader). Each mod is a self-contained folder with its own
`install.sh` / `uninstall.sh` that run **on the device**, so each can later become its own
Vellum package. Every folder has a README with details.

| Mod | What it does | Needs |
| --- | --- | --- |
| [`koreader-library-sync/`](koreader-library-sync/) | Every EPUB in your reMarkable library shows up in KOReader, synced on each KOReader launch. | KOReader |
| [`sleepscreen-rotate/`](sleepscreen-rotate/) | Your own pictures as the sleep screen, a random one each time: the images in the library folder "Screensaver". | – |
| [`secondary-pen-toolbar/`](secondary-pen-toolbar/) | The built-in second pen in the notes toolbar, right after the first pen. | xovi |
| [`eraser-end-uses-eraser-tool/`](eraser-end-uses-eraser-tool/) | The Marker's eraser end erases with the Eraser tool's type and size. | xovi |
| [`kosync-native-reader/`](kosync-native-reader/) | KOReader progress sync in the built-in EPUB reader: sync panel, auto pull on open / push on close. Standalone (no KOReader needed). | xovi |

## Before you start

1. **Developer mode** on the reMarkable (*Settings > General > Software > Advanced*). The SSH
   password is shown under *Settings > General > About > Copyrights and licenses*.
2. For the **xovi** mods: [xovi](https://github.com/asivery/xovi) with its
   **qt-resource-rebuilder** extension (and AppLoad for KOReader), e.g. via
   [Vellum](https://github.com/vellum-dev/vellum). xovi has to be **running** for these
   patches to load: on this setup it's started by triple-pressing the power button
   (xovi-tripletap) after every reboot, or with `/home/root/xovi/start`.
3. For `koreader-library-sync`: KOReader installed (e.g. as an AppLoad app).
4. For `kosync-native-reader`: after installing, log in to your sync server in the built-in
   reader (open an EPUB > three dots > *Progress sync*).

## Install

From your computer, with the device connected over USB (`10.11.99.1`) or on the same
Wi-Fi (`root@<device-ip>`):

```sh
./deploy.sh <mod>                       # install, e.g. ./deploy.sh secondary-pen-toolbar
./deploy.sh <mod> root@192.168.1.21     # install over Wi-Fi
./deploy.sh <mod> --uninstall           # remove again
./deploy.sh                             # list the mods
```

`deploy.sh` copies the folder over SSH and runs its installer (asks for the SSH password
once). Alternatively copy a folder to the device yourself and run `sh install.sh` there.

### Everything, in a good order

```sh
./deploy.sh koreader-library-sync         # then restart KOReader once
./deploy.sh sleepscreen-rotate            # restarts the reMarkable UI once
./deploy.sh secondary-pen-toolbar         # restarts the UI (if xovi is running)
./deploy.sh eraser-end-uses-eraser-tool   # restarts the UI (if xovi is running)
./deploy.sh kosync-native-reader          # restarts the UI (if xovi is running)
```

- If you also read in KOReader on the reMarkable: with `koreader-library-sync`, KOReader and
  the built-in reader open the very same EPUB files, so they sync with each other too.
- Installing a xovi mod while xovi isn't running is fine: the patch loads the next time
  you start xovi.
- `sleepscreen-rotate`: create a folder "Screensaver" in the reMarkable library and add
  images to it (e.g. with the reMarkable app). The rotation follows that folder (images
  added or deleted), checked each time the reMarkable wakes up.

## After a reMarkable OS update

An OS update replaces the system partition. Afterwards:

- **Re-run** `./deploy.sh sleepscreen-rotate` and `./deploy.sh kosync-native-reader`
  (their background services live on the system partition). Your pictures, books and
  settings are kept.
- The **xovi** patches are written against OS 3.29. xovi itself usually needs updating
  for a new OS; if a patch no longer fits the new OS, xochitl simply runs without it.
- `koreader-library-sync` lives entirely in KOReader and `/home`, nothing to redo.

## If something goes wrong

- **Stock interface**: reboot without starting xovi (or run `/home/root/xovi/stock`) - all
  xovi patches are then inactive.
- **Remove a mod**: `./deploy.sh <mod> --uninstall`.
- **Logs**: `journalctl -u xochitl` (patches: lines with `qmldiff` / `.qml:`),
  `journalctl -u kosync-bridge`, `journalctl -u sleepscreen-rotate`.
- xochitl allows only ~4 UI restarts within 10 minutes, after that the device reboots
  itself. The installers reset that counter, but avoid installing many xovi mods in a
  rapid loop by hand.
