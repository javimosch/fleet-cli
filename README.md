# fleet-cli

A minimal, agent-first orchestrator for *fleets*: engineered graphs of loops that run as scripts, produce events, and gate outbound actions through human approval.

It is inspired by `am-fleet` and is meant to become the standardized way to build, deploy and monitor new fleets without recreating the same moving pieces each time.

## Quick start

```bash
go build -o fleet ./cmd/fleet
./fleet init --name machin-growth --repo javimosch/machin
./fleet validate machin-growth
./fleet run machin-growth observe
./fleet run machin-growth prospect
./fleet run machin-growth draft
./fleet queue machin-growth
```

## Commands

| Command | Purpose |
|---|---|
| `init` | Scaffold a new fleet from a template |
| `validate <fleet>` | Parse and check `fleet.yml` |
| `plan <fleet>` | Show schedules and next expected runs |
| `run <fleet> <loop> [--dry-run]` | Execute one loop now |
| `status <fleet>` | Show state and HITL counts |
| `queue <fleet>` | List all proposals |
| `approve <fleet> <proposal-id>` | Approve an outbound proposal |
| `reject <fleet> <proposal-id>` | Reject an outbound proposal |
| `state <fleet> get/set/all` | Inspect or mutate state |
| `install <fleet> [--system] [--no-start]` | Render and enable systemd timers |
| `uninstall <fleet> [--system]` | Remove systemd timers and services |

## Systemd timers

```bash
./fleet install machin-growth
systemctl --user list-timers | grep fleet-
```

Units are written to `~/.config/systemd/user/` and start on the next user session. To start them immediately, omit `--no-start` or run `systemctl --user start fleet-<fleet>-<loop>.timer`.

## Fleet graph

A fleet is declared in `fleet.yml`:

```yaml
version: 1
name: machin-growth
repo: javimosch/machin

budgets:
  outbound_per_day: 10
  outbound_per_hour: 2

hitl:
  mode: required
  expire_after: 48h

loops:
  - name: observe
    schedule: "daily@09:00"
    command: ./loops/observe.sh
    outbound: false

  - name: dispatch
    schedule: "hourly"
    command: ./loops/dispatch.sh
    outbound: true
    requires_approval: true
    rate:
      max_per_run: 2
      max_per_day: 10
```

Loops are ordinary executable files. `fleet-cli` injects environment variables and reads their outputs from `FLEET_RUN_DIR`:

- `result.json` — events and state mutations
- `proposals.jsonl` — outbound actions to queue for HITL
- `state.json` — direct state mutations (overwrites keys)

## State and HITL

State is stored in `~/.local/share/fleet-cli/<fleet>.json`. HITL proposals live in `~/.local/share/fleet-cli/<fleet>-proposals.jsonl`.

Every outbound loop must set `requires_approval: true`. The `dispatch` loop only executes approved proposals, respects rate limits, and records dispatched IDs to avoid repeats.

## Test fleet: machin-growth

The included `fleets/machin-growth` is a dry-run, ToS-safe star-growth fleet for `javimosch/machin`.

- `observe` records the current star count from the GitHub API.
- `prospect` searches GitHub issues for relevant single/static binary discussions.
- `draft` turns the highest-scored prospects into comment proposals.
- `dispatch` (when run with `--dry-run`) prints what it would post; in live mode it would require manual HITL approval first.

It never posts, stars, or messages anyone automatically. All outbound actions must be approved through `fleet approve`.

## Design notes

- Go binary stays small and dumb; domain logic lives in `loops/*.sh`.
- JSON-by-default output, agent-friendly exit codes.
- Per-loop budgets and per-target cooldowns are enforced in `fleet.yml`; the dispatch loop also tracks dispatched IDs in state.
- See `docs/design.md` for the long-term plan.
