# 9beads

Standalone 9P server for [steveyegge/beads](https://github.com/steveyegge/beads) task tracking.

## Overview

Beads provides persistent, structured task memory for coding agents. Tasks persist across crashes, enabling agents to resume work and coordinate through dependency graphs.

**Storage:** Dolt (version-controlled SQL database) provides MVCC, ACID transactions, and cell-level diffs.

## Dependencies

- [plan9port](https://github.com/lneely/plan9port) (wayland-9pfuse-truncate branch required for `9pfuse` truncate fix)

## Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `BEADS_9MOUNT` | `~/mnt/beads` | FUSE mount path |
| `BEADS_PROJECT_DIRS` | `~/src:~/prj` | Colon-separated list of project parent directories (see below) |

### Project directories

`BEADS_PROJECT_DIRS` tells 9beads where your projects live. Mount paths must be at most 1 level deep from one of these directories. If a path is 2 levels deep, it resolves up to the parent project automatically.

This prevents a common foot gun: accidentally creating separate bead databases for different worktrees or subdirectories of the same project. Three layouts are supported:

- **Repo-based** (`~/src/mycoolapp`) — mounts directly as `mycoolapp`
- **Worktree-based** (`~/src/mycoolapp/branch-a`) — resolves up to `mycoolapp`, so all worktrees share one bead database
- **Workspace-based** (`~/src/my-monorepo-workspace/app1`) — resolves up to `my-monorepo-workspace`, so sub-projects (`app1`, `lib2`, `lib3`, ...) share one bead database instead of each getting their own

Git worktrees (where `.git` is a file, not a directory) are rejected outright — mount the base repo instead.

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
├── ctl              # global control (mount, umount)
├── mtab             # mount table: <name>\t<cwd>
├── ready            # ready beads across all mounts
├── deferred         # deferred beads across all mounts
├── closed           # closed beads across all mounts
├── events           # event stream (JSON, blocking read)
└── <mount>/
    ├── ctl          # mount control (new, claim, complete, etc.)
    ├── cwd          # working directory for this mount
    ├── list         # all open beads
    ├── ready        # ready beads (open, unblocked)
    ├── deferred     # deferred beads
    ├── closed       # closed beads
    ├── comments/    # per-bead comments
    │   └── <bead-id>    # all comments (separated by ---)
    └── <bead-id>    # bead file (markdown + YAML frontmatter)
```

### Bead files

Each bead is a plain text file with YAML frontmatter. Only open beads appear in directory listings, but closed beads are still accessible by ID — like hidden files:

```sh
cat $bdir/bd-a1b2          # works even if bd-a1b2 is closed
grep "bd-a1b2" $bdir/closed # find it in the closed list first
```

Format:

```
---
id: bd-a1b2
title: Fix login bug
status: deferred
updated: 2026-04-01
parent:
labels: []
blockers: []
---
Add OAuth token refresh logic in auth/token.go.
```

Read and write it like any file. On write, the frontmatter is parsed and the store is updated.

> Note: `sed -i` is not supported on 9P filesystems. Pipe through sed instead:
> `sed "s/^title: .*/title: New title/" $bdir/bd-a1b2 > $bdir/bd-a1b2`

### List format

All list views (`list`, `ready`, `deferred`, `closed`) are tab-separated plain text:

```
<id>\t<status>\t<blockers-count>\t<assignee>\t<updated>\t<title>
```

`-` for zero/empty fields. `<updated>` is `YYYY-MM-DD`.

### Comments

Comments are accessed via `<mount>/comments/<bead-id>`:

```
author1	2026-04-01 14:30
First comment text.

---
author2	2026-04-02 09:15
Second comment text.
```

All beads with comments are listed in `<mount>/comments/` regardless of status.

### Querying with standard tools

```sh
bdir=~/mnt/beads/$mount

# blocked beads (blockers-count != -)
awk -F'\t' '$3 != "-"' $bdir/list

# stale beads (not updated in 30+ days)
awk -F'\t' -v d="$(date -d '30 days ago' +%Y-%m-%d)" '$5 < d' $bdir/list

# beads assigned to a specific agent
grep -F "myagent" $bdir/ready

# beads by external reference (e.g. Jira key)
grep "bd-a1b2" $bdir/list

# child beads of a parent
grep "^bd-a1b2\." $bdir/list

# beads with a specific label
grep -rl "capability:high" $bdir/

# batch lookup
grep -F "bd-a1b2\|bd-c3d4" $bdir/list
```

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

# add and read comments
echo "comment bd-a1b2 'Fixed in commit abc123'" > $bdir/ctl
cat $bdir/comments/bd-a1b2
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
