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

1. Prepare pictures on your computer from a folder of photos (`.jpg`, `.jpeg`, `.png`,
   `.heic` directly inside it). They're center-cropped to the screen size and saved as
   PNGs in `images/` (git-ignored):

   ```sh
   ./prepare-images.sh ~/Pictures/Sleepscreens                # Paper Pro Move (954x1696)
   ./prepare-images.sh 1620x2160 ~/Pictures/Sleepscreens      # Paper Pro
   ./prepare-images.sh 1404x1872 ~/Pictures/Sleepscreens      # reMarkable 2
   ```

2. From the repo root: `./deploy.sh sleepscreen-rotate`. **The reMarkable UI restarts
   once** (closes open apps like KOReader).

### The source folder is the only source of truth

Both steps mirror it exactly: pictures already there and unchanged are left alone,
missing or changed ones are added, and everything else is deleted.

- `prepare-images.sh`: source folder -> `images/` (only new/changed photos are converted;
  a different screen size rebuilds everything).
- `install.sh` (via `deploy.sh`): `images/` -> `/home/root/sleepscreens/` on the device
  (compared by content). Without an `images/` folder, the device's pictures are left as
  they are.

To change the rotation, edit the source folder and run both commands again:

```sh
./sleepscreen-rotate/prepare-images.sh ~/Pictures/Sleepscreens && ./deploy.sh sleepscreen-rotate
```

PNGs copied onto the device by hand are removed by the next deploy.

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
