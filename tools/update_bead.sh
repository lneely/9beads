#!/bin/bash
# capabilities: beads
# description: Update a bead field directly
# Usage: update_bead.sh --mount <mount> --id <bead-id> --field <field> --value <value>
set -euo pipefail

BEADS="${BEADS_9MOUNT:-$HOME/mnt/beads}"
MOUNT=""
BEAD_ID=""
FIELD=""
VALUE=""

while [[ $# -gt 0 ]]; do
    case "$1" in
        --mount) MOUNT="$2";   shift 2 ;;
        --id)    BEAD_ID="$2"; shift 2 ;;
        --field) FIELD="$2";   shift 2 ;;
        --value) VALUE="$2";   shift 2 ;;
        *) echo "unknown argument: $1" >&2; exit 1 ;;
    esac
done

if [ -z "$MOUNT" ] || [ -z "$BEAD_ID" ] || [ -z "$FIELD" ]; then
    echo "usage: update_bead.sh --mount <mount> --id <bead-id> --field <field> --value <value>" >&2
    exit 1
fi

if [ "$FIELD" = "parent" ]; then
    # Reparent: extract title/desc from YAML frontmatter bead file
    BEAD_FILE="$BEADS/$MOUNT/$BEAD_ID"
    TITLE=$(awk '/^title:/{print substr($0, 8)}' "$BEAD_FILE" 2>/dev/null)
    DESC=$(awk '/^---$/{n++; next} n==1{next} {print}' "$BEAD_FILE" 2>/dev/null)

    printf "new '%s' '%s' %s\n" "$TITLE" "$DESC" "$VALUE" > "$BEADS/$MOUNT/ctl"
    printf "delete %s\n" "$BEAD_ID" > "$BEADS/$MOUNT/ctl"
    echo "reparented $BEAD_ID under $VALUE"
else
    printf "update %s %s '%s'\n" "$BEAD_ID" "$FIELD" "$VALUE" > "$BEADS/$MOUNT/ctl"
    echo "updated $BEAD_ID.$FIELD: $VALUE"
fi
