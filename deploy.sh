#!/bin/sh
# Copy a mod folder to a reMarkable over SSH and run its installer (or uninstaller).
#
#   ./deploy.sh <mod> [host]              install   (host default: root@10.11.99.1 = USB)
#   ./deploy.sh <mod> [host] --uninstall  uninstall
#
# The SSH password is shown on the device under
# Settings > General > About > Copyrights and licenses (developer mode).
set -e
cd "$(dirname "$0")"
MOD="${1%/}"; HOST="${2:-root@10.11.99.1}"; ACTION=install.sh
[ "$2" = "--uninstall" ] && { HOST=root@10.11.99.1; ACTION=uninstall.sh; }
[ "$3" = "--uninstall" ] && ACTION=uninstall.sh

if [ -z "$MOD" ] || [ ! -f "$MOD/install.sh" ]; then
    echo "usage: $0 <mod> [host] [--uninstall]"; echo "mods:"
    for d in */install.sh; do echo "  ${d%/install.sh}"; done
    exit 1
fi

# One SSH connection for both steps, so the password is only asked once.
SOCK="${TMPDIR:-/tmp}/rm-deploy-$$"
trap 'ssh -o ControlPath="$SOCK" -O exit "$HOST" 2>/dev/null; rm -f "$SOCK"' EXIT
ssh -o ControlMaster=yes -o ControlPath="$SOCK" -o ControlPersist=60 -fN "$HOST"

echo "Copying $MOD to $HOST..."
ssh -o ControlPath="$SOCK" "$HOST" "rm -rf /tmp/rm-mod-$MOD && mkdir -p /tmp/rm-mod-$MOD"
COPYFILE_DISABLE=1 tar -C "$MOD" -cf - . | ssh -o ControlPath="$SOCK" "$HOST" "tar -C /tmp/rm-mod-$MOD -xf -"
ssh -o ControlPath="$SOCK" "$HOST" "cd /tmp/rm-mod-$MOD && sh ./$ACTION; rc=\$?; rm -rf /tmp/rm-mod-$MOD; exit \$rc"
