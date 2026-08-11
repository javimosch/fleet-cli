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
./fleet guide
```

## Commands

| Command | Purpose |
|---|---|
| `init` | Scaffold a new fleet from a template |
| `validate <fleet>` | Parse and check `fleet.yml` |
| `plan <fleet>` | Show schedules and next expected runs |
| `run <fleet> <loop> [--dry-run] [--no-chain]` | Execute one loop now; by default it chains to `on` event listeners |
| `emit <fleet> <event> [--data <json>] [--dry-run]` | Manually trigger an event and dispatch its listeners |
| `status <fleet>` | Show state and HITL counts |
| `queue <fleet>` | List all proposals |
| `approve <fleet> <proposal-id>` | Approve an outbound proposal |
| `reject <fleet> <proposal-id>` | Reject an outbound proposal |
| `state <fleet> get/set/all` | Inspect or mutate state |
| `run <fleet> execute` | Execute approved actions (live requires `FLEET_LIVE=1`) |
| `install <fleet> [--system] [--no-start]` | Render and enable systemd timers |
| `uninstall <fleet> [--system]` | Remove systemd timers and services |
| `update [--check\|--force]` | Verify, smoke-test, and atomically install a newer release |

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
- `prospect` searches GitHub issues for relevant single/static binary discussions and checks engaged threads for follow-up comments.
- `draft` turns the highest-scored prospects into comment proposals.
- `dispatch` emits `action.approved` events and queues them in `pending_actions`.
- `execute` runs queued actions through `handlers/<kind>.sh` so the fleet stays generic.

It never posts, stars, or messages anyone automatically. All outbound actions must be approved through `fleet approve` and executed through `fleet run <fleet> execute` with `FLEET_LIVE=1`.

## Event-driven loops

Loops can declare `on: [event.name]`. When a run emits one of those events, `fleet-cli` runs the listeners in a single BFS chain.

```bash
./fleet run machin-growth prospect
# runs prospect, then automatically runs draft because draft is on: [prospect.found]

./fleet run machin-growth prospect --no-chain
# runs only prospect

./fleet emit machin-growth prospect.found --dry-run
# manually fire the event and see which loops would run
```

The output includes the initial run plus a `chain` array of all downstream runs.

## Cost tracking

Every run now reports a `cost` object. For `machin-growth` this is the number of GitHub API calls (`gh` invocations) made by that run, grouped by command.

```bash
./fleet run machin-growth observe
# cost.gh_calls = 1

./fleet run machin-growth prospect
# cost.gh_calls = 5  (4 search + 1 issue view)
```

Rate-limit notes are attached to each cost record. GitHub's public API is free in USD/EUR; the real budget is API quota. Agentic loops that consume LLM tokens can write an explicit `cost.json` to `FLEET_RUN_DIR` and the runner will include `tokens_in`, `tokens_out`, or `usd` in the same `cost` field.

Run costs are also accumulated in state under `costs`, so `./fleet state machin-growth get costs` returns a ledger.

## Cuzz channel integration

`fleet.yml` can declare `channels` with `kind: cuzz`. When configured, the runner sends:

- `ops` channel: every loop completion, HITL proposals waiting for approval, approve/reject decisions
- `alerts` channel: loop failures and non-ok statuses

The cuzz binary must be on `PATH` or set via `FLEET_CUZZ_BIN`. The cuzz relay is configured through the normal cuzz environment (`CUZZ_URL`, `CUZZ_TOKEN`, `CUZZ_AGENT`). The send has a 5-second timeout so a slow relay can never block a loop.

```yaml
channels:
  ops:
    kind: cuzz
    channel: machin-growth
  alerts:
    kind: cuzz
    channel: machin-alerts
```

## Long-running daemon loops

A loop can run as a systemd service and be restarted on a `runtime_max` cadence (e.g. every 1h) to limit memory growth:

```yaml
loops:
  - name: agent
    mode: daemon
    runtime_max: 1h
    command: ./loops/agent.sh
    outbound: false
```

`fleet install` writes a `Type=simple` service with `RuntimeMaxSec=3600`, `Restart=always`, and `RestartSec=10`. The runner uses `runtime_max` as the default timeout for `fleet run <fleet> agent`, so the process exits cleanly before systemd would hard-kill it.

## Agent-First CLI contract

`fleet-cli` follows the Agent-First CLI conventions for a pure command-line tool:

- `fleet help-json` exposes the machine-readable command catalog, environment variables, and semantic exit-code ranges.
- `fleet guide` emits the embedded operating model as JSON; `fleet guide --human` renders a concise Markdown guide without network access.
- Successful command data is JSON on stdout. Progress, loop stderr, and feedback delivery notes go to stderr.
- Typed errors use semantic exit codes: `80–89` input, `90–99` resource/precondition, `100–109` external/integration, `110–119` internal.
- `fleet feedback "..." --kind bug` is a relay-only, best-effort report for this pure CLI. It always exits successfully after generating one idempotency key; set `FEEDBACK_RELAY=off` to disable delivery.
- `fleet update [--check|--force]` adopts the cli-update-spec update-command subset: it hashes the running binary, fetches a `FLEET_UPDATE_URL` manifest, verifies short/full SHA-256, smoke-tests `fleet version`, then atomically swaps the executable while retaining a timestamped `.bak`.
- Passive update nudges, the standalone installer, and `install`/`uninstall` self-relocation are intentionally not part of this iteration; the existing release workflow remains the first-install path.
- The manifest defaults to the GitHub release asset `version-<GOOS>-<GOARCH>.json`; it contains `{ "ok": true, "version": "<sha256[:12]>", "download": "fleet-<GOOS>-<GOARCH>", "sha256": "<full hash>" }`.
- `fleet update --check` exits `5` when a newer artifact is available and never downloads; `--force` repairs a matching/corrupt install. Updates resolve symlinks, serialize concurrent callers in-process, and retain a timestamped rollback backup.
- Telemetry is intentionally not enabled: fleet-cli is infrastructure tooling and does not need an adoption signal.

```sh
fleet help-json | jq '.commands | keys'
fleet guide | jq '.guide.loop'
FEEDBACK_RELAY=off fleet feedback "the loop failed" --kind bug
fleet update --check
```

Tagged releases publish static binaries for Linux and macOS on amd64 and arm64.

## Design notes

- Go binary stays small and dumb; domain logic lives in `loops/*.sh`.
- Outbound execution is fleet-specific: `execute` dispatches to `handlers/<kind>.sh`.
- JSON-by-default output, agent-friendly exit codes.
- Per-loop budgets and per-target cooldowns are enforced in `fleet.yml`; the dispatch loop also tracks dispatched IDs in state.
- See `docs/design.md` for the long-term plan.
