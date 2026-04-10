#!/bin/bash
# capabilities: beads
# description: List ready/claimable beads (TSV: id, status, blockers, assignee, updated, title)
# Usage: list_ready_beads.sh --mount <mount>
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
    echo "usage: list_ready_beads.sh --mount <mount>" >&2
    exit 1
fi

cat "$BEADS/$MOUNT/ready" 2>/dev/null || echo "no ready beads"
