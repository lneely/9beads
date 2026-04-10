#!/bin/bash
# capabilities: beads
# description: Search beads by id or title content
# Usage: search_beads.sh --mount <mount> --query <query>
#        search_beads.sh --mount <mount> --id <bead-id>
set -euo pipefail

BEADS="${BEADS_9MOUNT:-$HOME/mnt/beads}"
MOUNT=""
QUERY=""
BEAD_ID=""

while [[ $# -gt 0 ]]; do
    case "$1" in
        --mount) MOUNT="$2"; shift 2 ;;
        --query) QUERY="$2"; shift 2 ;;
        --id)    BEAD_ID="$2"; shift 2 ;;
        *) echo "unknown argument: $1" >&2; exit 1 ;;
    esac
done

if [ -z "$MOUNT" ]; then
    echo "usage: search_beads.sh --mount <mount> --query <query>" >&2
    echo "       search_beads.sh --mount <mount> --id <bead-id>" >&2
    exit 1
fi

# Direct ID lookup
if [ -n "$BEAD_ID" ]; then
    cat "$BEADS/$MOUNT/$BEAD_ID" 2>/dev/null || echo "bead not found: $BEAD_ID"
    exit 0
fi

if [ -z "$QUERY" ]; then
    echo "usage: search_beads.sh --mount <mount> --query <query>" >&2
    exit 1
fi

# Search list TSV (case-insensitive grep across all fields)
grep -i "$QUERY" "$BEADS/$MOUNT/list" 2>/dev/null || echo "no matches"
