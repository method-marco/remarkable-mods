# sleepscreen-rotate

Your own pictures as the reMarkable sleep screen, a random one each time the
device sleeps (never the same one twice in a row).

**The pictures are the images in the library folder "Screensaver"**, and only those.
Create a folder with that name in the reMarkable library and put images in it, e.g. with
the reMarkable app on your phone or computer. The rotation stays in sync with the folder:
images you delete or move out are dropped at the next wake, images you add are converted
in the background after the next wake and used from then on. Without the folder (or with
an empty one) the stock sleep screen is shown.

## How it works

- Uses xochitl's built-in custom sleep screen setting (`SleepScreenPath` in
  `~/.config/remarkable/xochitl.conf`, plus `ShowSleepScreenCarousel=false`), pointed
  at `/home/root/sleepscreens/.current.png`. No system files are replaced.
- A small service (`sleepscreen-rotate.service`) follows xochitl's log. Each time the
  display wakes ("display state from DeepSleep to Normal") it drops pictures of documents
  that left the folder, copies a random ready picture over `.current.png` (well under a
  second, so even a quick wake and sleep gets a new picture), and then converts new
  documents in the background. The picture is chosen at wake rather than at sleep
  because the reMarkable shows the sleep screen and freezes all programs at once.
- `sleepscreen-library` (a small program, Go standard library only, source in
  [`library/`](library/)) does the syncing and converting. The library stores imported
  images as PDFs: it takes the photo out of the PDF (JPEG, or raw images), turns it
  upright (phone photos are often stored sideways with an EXIF rotation), center-crops it
  to the screen's aspect ratio and resizes it. The results are kept in
  `/home/root/sleepscreens/library/` (one PNG per document) and only redone when a
  document changes. A new full-size photo takes a few seconds to convert. Documents
  without an image (notebooks, text PDFs) and subfolders are ignored.
- On newer OS versions `/etc` is a RAM overlay, so the installer writes the service onto
  the real root filesystem to survive reboots.

## Install

From the repo root: `./deploy.sh sleepscreen-rotate`. **The reMarkable UI restarts once**
(closes open apps like KOReader). Then create the "Screensaver" folder in the library.

`./build.sh` rebuilds `sleepscreen-library` in Docker (only needed after changing
`library/`; the repository contains a prebuilt binary).

## Notes

- **After a reMarkable OS update** the service is gone (the root filesystem is replaced);
  your pictures and the setting are kept, so you'd see the last picture until you re-run
  the installer.
- Requires an OS version with custom sleep screen support; the installer checks for it.
- Even sleeps of a few seconds get a new picture. The wake detection relies on
  xochitl's log message, which could change in a future OS version.
- Log: `journalctl -u sleepscreen-rotate -f` (added/removed pictures and each pick).
- Uninstall (restores the stock sleep screen): `./deploy.sh sleepscreen-rotate --uninstall`.
  The library folder and its documents are never touched.
- Your original settings are backed up once to
  `~/.config/remarkable/xochitl.conf.bak-before-sleepscreen`.
