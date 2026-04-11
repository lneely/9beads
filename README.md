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
├── mount            # write-only: echo /path/to/project > mount
├── umount           # write-only: echo <name|path> > umount
├── mtab             # mount table: <name>\t<cwd>
├── ready            # ready beads across all mounts
├── deferred         # deferred beads across all mounts
├── closed           # closed beads across all mounts
├── events           # event stream (JSON, blocking read)
└── <mount>/
    ├── ctl          # mount control (fallback for all commands)
    ├── cwd          # working directory for this mount
    ├── list         # all open beads
    ├── ready        # ready beads (open, unblocked)
    ├── deferred     # deferred beads
    ├── closed       # closed beads
    ├── new          # read: empty bead template; write: create new bead
    ├── claim        # write-only: echo <id> [assignee=<name>]
    ├── unclaim      # write-only: echo <id>
    ├── open         # write-only: echo <id>
    ├── defer        # write-only: echo <id> [until=<RFC3339>]
    ├── complete     # write-only: echo <id>
    ├── fail         # write-only: echo <id> [reason=<text>]
    ├── label        # write-only: echo "<id> key:value"
    ├── unlabel      # write-only: echo "<id> key:value"
    ├── dep          # write-only: echo "<id> <parent-id>"
    ├── undep        # write-only: echo "<id> <parent-id>"
    ├── relate       # write-only: echo "<id1> <id2>"
    ├── init         # write-only: echo <prefix>
    ├── delete       # write-only: echo <id>
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

To create a new bead, read `<mount>/new` for a blank template, fill it in, and write it back:

```sh
cat $bdir/new > /tmp/bead.md
# edit /tmp/bead.md
cat /tmp/bead.md > $bdir/new
```

The ID is auto-generated. If `parent:` is set, the parent-child relationship is established automatically. `labels` and `blockers` are also wired up on creation.

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
echo /home/user/myproject > $mnt/mount

# unmount by name or path
echo myproject > $mnt/umount
echo /home/user/myproject > $mnt/umount

# list open beads
cat $bdir/list

# read a bead
cat $bdir/bd-a1b2

# edit a bead (title, status, blockers, description)
$EDITOR $bdir/bd-a1b2

# create a bead via the new endpoint
cat $bdir/new > /tmp/bead.md
# edit /tmp/bead.md, then:
cat /tmp/bead.md > $bdir/new

# create a bead via ctl (legacy)
echo "new 'Fix login bug' 'OAuth token not refreshed'" > $bdir/ctl

# claim / complete / fail
echo bd-a1b2 > $bdir/claim
echo bd-a1b2 > $bdir/complete
echo "bd-a1b2 reason=broken upstream" > $bdir/fail

# defer until a date
echo "bd-a1b2 until=2026-06-01T00:00:00Z" > $bdir/defer

# add labels (key:value format)
echo "bd-a1b2 capability:high" > $bdir/label
echo "bd-a1b2 area:auth" > $bdir/label
echo "bd-a1b2 capability:high" > $bdir/unlabel

# link beads
echo "bd-c3d4 bd-a1b2" > $bdir/dep      # c3d4 depends on a1b2
echo "bd-c3d4 bd-a1b2" > $bdir/undep
echo "bd-a1b2 bd-c3d4" > $bdir/relate

# delete a bead
echo bd-a1b2 > $bdir/delete

# set ID prefix for new beads
echo myproj > $bdir/init

# add and read comments
echo "comment bd-a1b2 'Fixed in commit abc123'" > $bdir/ctl
cat $bdir/comments/bd-a1b2
```

## Control Commands

Most commands are available as direct write-only files in `<mount>/` (preferred) or via `<mount>/ctl` (fallback). Direct files accept `<id> [key=value ...]`; `ctl` uses the legacy command-prefix format. Arguments support single/double quotes and backslash escaping (including unicode).

### Labels

Labels are `key:value` strings (e.g. `capability:high`, `area:auth`). `set-capability` in `ctl` is just a convenience wrapper around `label`/`unlabel` — prefer `label` directly.

### Direct file endpoints (`<mount>/<command>`)

| File | Input format | Description |
|------|-------------|-------------|
| `new` | *(frontmatter file)* | Create bead — see [Bead files](#bead-files) |
| `claim` | `<id> [assignee=<name>]` | Claim bead (sets assignee + in_progress) |
| `unclaim` | `<id>` | Release claim (resets to open) |
| `open` | `<id>` | Promote deferred bead to open |
| `defer` | `<id> [until=<RFC3339>]` | Defer bead |
| `complete` | `<id>` | Mark completed |
| `fail` | `<id> [reason=<text>]` | Mark failed |
| `label` | `<id> <key:value>` | Add label |
| `unlabel` | `<id> <key:value>` | Remove label |
| `dep` | `<id> <parent-id>` | Add blocking dependency |
| `undep` | `<id> <parent-id>` | Remove dependency |
| `relate` | `<id1> <id2>` | Add relates-to link |
| `init` | `<prefix>` | Set ID prefix (default: bd) |
| `delete` | `<id>` | Delete bead |

### `ctl` commands (`<mount>/ctl`)

| Command | Format | Description |
|---------|--------|-------------|
| `new` | `new 'title' ['desc'] [parent-id] [capability=low\|standard\|high] [scope=<s>] [blockers=id,...]` | Create bead |
| `claim` | `claim <id> [assignee]` | Claim bead |
| `unclaim` | `unclaim <id>` | Release claim |
| `open` | `open <id>` | Promote deferred bead to open |
| `defer` | `defer <id> [until <RFC3339>]` | Defer bead |
| `reopen` | `reopen <id>` | Reopen closed bead |
| `complete` | `complete <id>` | Mark completed |
| `fail` | `fail <id> 'reason'` | Mark failed |
| `update` | `update <id> <field> 'value'` | Update a field (not status/assignee) |
| `delete` | `delete <id>` | Delete bead |
| `comment` | `comment <id> 'text'` | Add comment |
| `label` | `label <id> 'key:value'` | Add label |
| `unlabel` | `unlabel <id> 'key:value'` | Remove label |
| `set-capability` | `set-capability <id> low\|standard\|high` | Shorthand for `label`/`unlabel capability:*` |
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

The `mount` and `umount` files accept the argument directly, without a command prefix:

```sh
echo /path/to/project > $mnt/mount
echo myproject > $mnt/umount
echo /path/to/project > $mnt/umount
```

## See Also

- [steveyegge/beads](https://github.com/steveyegge/beads)
- [Dolt](https://github.com/dolthub/dolt)
