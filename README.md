# remarkable-mods

Small mods for a reMarkable in developer mode (tested on a **reMarkable Paper Pro Move**,
OS 3.29, with xovi + AppLoad + KOReader). Each mod is a self-contained folder with its own
`install.sh` / `uninstall.sh` that run **on the device**, so each can later become its own
Vellum package.

| Mod | What it does |
| --- | --- |
| [`koreader-library-sync/`](koreader-library-sync/) | Makes every EPUB in your reMarkable library show up in KOReader, synced on each KOReader launch. |
| [`sleepscreen-rotate/`](sleepscreen-rotate/) | Uses your own pictures as the sleep screen, a random one each time. |

## Install on any reMarkable

From your computer, with the device connected (USB is `10.11.99.1`):

```sh
./deploy.sh koreader-library-sync              # or: ./deploy.sh <mod> root@<device-ip>
./deploy.sh sleepscreen-rotate
./deploy.sh sleepscreen-rotate --uninstall
```

`deploy.sh` copies the folder over SSH and runs its installer (you'll be asked for the
SSH password once; it's shown on the device under *Settings > General > About >
Copyrights and licenses*). You can also copy a folder to the device yourself and run
`sh install.sh` there.
