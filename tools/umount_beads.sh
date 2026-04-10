#!/bin/bash
# capabilities: beads
# description: Unmount a beads project
# Usage: umount_beads.sh --mount <name>
set -euo pipefail

BEADS="${BEADS_9MOUNT:-$HOME/mnt/beads}"

MOUNT=""

while [[ $# -gt 0 ]]; do
    case "$1" in
        --mount) MOUNT="$2"; shift 2 ;;
        *) echo "unknown argument: $1" >&2; exit 1 ;;
    esac
done

if [ -z "$MOUNT" ]; then
    echo "usage: umount_beads.sh --mount <name>" >&2
    exit 1
fi

echo "umount $MOUNT" > "$BEADS/ctl"
echo "unmounted $MOUNT"
