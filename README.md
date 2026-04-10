# 9beads

Standalone 9P server for [steveyegge/beads](https://github.com/steveyegge/beads) task tracking.

## Overview

Beads provides persistent, structured task memory for coding agents. Tasks persist across crashes, enabling agents to resume work and coordinate through dependency graphs.

**Storage:** Dolt (version-controlled SQL database) provides MVCC, ACID transactions, and cell-level diffs.

## Dependencies

- [plan9port](https://github.com/lneely/plan9port) (wayland-9pfuse-truncate branch required for `9pfuse` truncate fix)

## Usage

```sh
9beads start       # background
9beads fgstart     # foreground
9beads status
9beads stop
```

On startup, the server mounts at `$BEADS_9MOUNT` (default: `~/mnt/beads`) via 9pfuse. **Use ordinary file operations to interact with it.**

## Filesystem Structure

```
~/mnt/beads/
├── ctl                    # global control (mount, umount)
├── mtab                   # mount table: <name>\t<cwd>
├── ready                  # ready beads across all mounts
├── deferred               # deferred beads across all mounts
├── closed                 # last 100 closed beads across all mounts
├── events                 # event stream (JSON, blocking read)
└── <mount>/
    ├── ctl                # mount control (create, claim, complete, etc.)
    ├── cwd                # working directory for this mount
    ├── list               # all open beads
    ├── list/<n>           # all open beads, limit n
    ├── ready              # ready beads (unblocked, open)
    ├── ready/<n>          # ready beads, limit n
    ├── deferred           # deferred beads
    ├── closed             # last 100 closed beads
    ├── blocked            # blocked beads
    ├── stale              # beads not updated in 30+ days
    ├── search/<query>     # text search results
    ├── by-ref/<ref>       # bead by external reference
    ├── batch/<id,...>     # batch lookup by IDs
    ├── label/<label>      # beads with label
    ├── children/<id>      # direct children of parent
    └── <bead-id>          # bead file (markdown + YAML frontmatter)
```

### Bead files

Each bead is a plain text file with YAML frontmatter:

```
---
id: bd-a1b2
title: Fix login bug
status: deferred
parent:
blockers: []
---
Add OAuth token refresh logic in auth/token.go.
```

Read and write it like any file. On write, the frontmatter is parsed and the store is updated.

### List format

`list`, `ready`, `deferred`, and `closed` are tab-separated plain text:

```
<id>\t<status>\t<blockers-count>\t<assignee>\t<title>
```

`-` is used for zero/empty fields.

## Examples

```sh
mnt=~/mnt/beads
mount=$(awk '$2=="/home/user/myproject"{print $1}' $mnt/mtab)
bdir=$mnt/$mount

# mount a project
echo "mount /home/user/myproject" > $mnt/ctl

# list open beads
cat $bdir/list

# read a bead
cat $bdir/bd-a1b2

# edit a bead (title, status, blockers, description)
$EDITOR $bdir/bd-a1b2

# create a bead
echo "new 'Fix login bug' 'OAuth token not refreshed'" > $bdir/ctl

# claim / complete
echo "claim bd-a1b2" > $bdir/ctl
echo "complete bd-a1b2" > $bdir/ctl

# find beads assigned to an agent
grep myagent $bdir/ready

# update title without temp file (sed -i unsupported on 9P)
sed "s/^title: .*/title: New title/" $bdir/bd-a1b2 > $bdir/bd-a1b2
```

## Control Commands

Written to `<mount>/ctl`. Arguments support single/double quotes and backslash escaping (including unicode).

| Command | Format | Description |
|---------|--------|-------------|
| `new` | `new 'title' ['desc'] [parent-id] [capability=low\|standard\|high] [scope=<s>] [blockers=id,...]` | Create bead |
| `claim` | `claim <id> [assignee]` | Claim bead (sets assignee + in_progress) |
| `unclaim` | `unclaim <id>` | Release claim (resets to open) |
| `open` | `open <id>` | Promote deferred bead to open |
| `defer` | `defer <id> [until <RFC3339>]` | Defer bead |
| `reopen` | `reopen <id>` | Reopen closed bead |
| `complete` | `complete <id>` | Mark completed |
| `fail` | `fail <id> 'reason'` | Mark failed |
| `update` | `update <id> <field> 'value'` | Update a field (not status/assignee) |
| `delete` | `delete <id>` | Delete bead |
| `comment` | `comment <id> 'text'` | Add comment |
| `label` | `label <id> 'label'` | Add label |
| `unlabel` | `unlabel <id> 'label'` | Remove label |
| `set-capability` | `set-capability <id> low\|standard\|high` | Set capability level |
| `dep` | `dep <child-id> <parent-id>` | Add blocking dependency |
| `undep` | `undep <child-id> <parent-id>` | Remove dependency |
| `relate` | `relate <id1> <id2>` | Add relates-to link |
| `init` | `init [prefix]` | Set ID prefix (default: bd) |
| `batch-create` | `batch-create <json-array>` | Create multiple beads |

Written to global `ctl`:

| Command | Format | Description |
|---------|--------|-------------|
| `mount` | `mount <cwd> [name]` | Mount a project |
| `umount` | `umount <name\|cwd>` | Unmount a project |

## See Also

- [steveyegge/beads](https://github.com/steveyegge/beads)
- [Dolt](https://github.com/dolthub/dolt)
