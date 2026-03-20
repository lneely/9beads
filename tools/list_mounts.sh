#!/bin/bash
# capabilities: beads
# description: List mounted beads projects
set -euo pipefail

BEADS="${BEADS_9MOUNT:-$HOME/mnt/beads}"


cat "$BEADS"/mtab 2>/dev/null || echo "No mounted projects"
