# Events System

9beads provides a real-time event stream at `beads/events` using [github.com/simonfxr/pubsub](https://github.com/simonfxr/pubsub).

## Reading Events

```sh
9p read beads/events
```

Blocks and streams JSON events as they occur, one per line.

## Event Format

```json
{"id":"uuid","ts":1708598530,"source":"beads/myproject","type":"BeadReady","data":{"id":"bd-abc","title":"...","status":"open",...}}
{"id":"uuid","ts":1708598535,"source":"beads/myproject","type":"BeadClaimed","data":{"bead_id":"bd-abc","assignee":"agent-1","mount":"myproject"}}
```

## Event Types

- `BeadReady` - A bead transitioned to open/ready; `data` is full bead JSON including comments
- `BeadClaimed` - A bead was claimed; `data` is `{"bead_id","assignee","mount"}`

## Consuming Events

```sh
# Filter ready beads
9p read beads/events | jq 'select(.type == "BeadReady")'

# Desktop notifications
9p read beads/events | jq -r 'select(.type == "BeadReady") | "\(.data.id): \(.data.title)"' | \
  while read msg; do notify-send "Bead Ready" "$msg"; done
```
