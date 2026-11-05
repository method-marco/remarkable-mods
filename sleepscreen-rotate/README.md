# sleepscreen-rotate

Your own pictures as the reMarkable sleep screen, a random one each time the
device sleeps (never the same one twice in a row).

## How it works

- Uses xochitl's built-in custom sleep screen setting (`SleepScreenPath` in
  `~/.config/remarkable/xochitl.conf`, plus `ShowSleepScreenCarousel=false`), pointed
  at `/home/root/sleepscreens/.current.png`. No system files are replaced.
- A small service (`sleepscreen-rotate.service`) follows xochitl's log and, each time
  the display wakes ("display state from DeepSleep to Normal"), copies a random PNG from
  `/home/root/sleepscreens/` over `.current.png`, ready for the next sleep.
- On newer OS versions `/etc` is a RAM overlay, so the installer writes the service onto
  the real root filesystem to survive reboots.

## Install

1. Prepare pictures on your computer (center-crops to the screen size, outputs PNGs into
   `images/`, which is git-ignored):

   ```sh
   ./prepare-images.sh ~/Pictures/*.jpg                # Paper Pro Move (954x1696)
   ./prepare-images.sh 1620x2160 ~/Pictures/*.jpg      # Paper Pro
   ./prepare-images.sh 1404x1872 ~/Pictures/*.jpg      # reMarkable 2
   ```

2. From the repo root: `./deploy.sh sleepscreen-rotate`. **The reMarkable UI restarts
   once** (closes open apps like KOReader).

Add or remove pictures any time by copying PNGs into `/home/root/sleepscreens/` on the
device; no reinstall needed.

## Notes

- **After a reMarkable OS update** the service is gone (the root filesystem is replaced);
  your pictures and the setting are kept, so you'd see the last picture until you re-run
  the installer.
- Requires an OS version with custom sleep screen support; the installer checks for it.
- Even sleeps of a few seconds get a new picture. The wake detection relies on
  xochitl's log message, which could change in a future OS version.
- Uninstall (restores the stock sleep screen): `./deploy.sh sleepscreen-rotate --uninstall`
  (`--purge` on the device also deletes the pictures).
- Your original settings are backed up once to
  `~/.config/remarkable/xochitl.conf.bak-before-sleepscreen`.
