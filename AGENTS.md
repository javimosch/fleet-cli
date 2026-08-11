# AGENTS.md — fleet-cli

Agent guide for adding and operating fleets with fleet-cli.

## Quick start: add a new fleet

1. Create `fleets/<name>/` with:
   - `fleet.yml` (version, name, repo, loops)
   - `loops/*.sh` loop scripts
   - `handlers/<kind>.sh` for outbound actions
   - optional `bin/` for command wrappers
2. Validate: `fleet validate <name>`
3. Install units: `fleet install <name> --system`
4. Run manually: `fleet run <name> <loop>`
5. Approve proposals: `fleet approve <name> <id>`

Outbound loops must set `outbound: true` and `requires_approval: true`.

## Graph concepts

fleet-cli is an event-driven graph of loops:
- **Fleet**: directory with config, loops, handlers, optional bin.
- **Loop**: a script run by the runner with env vars like `FLEET_DIR`, `FLEET_STATE_FILE`, `FLEET_QUEUE_FILE`, `FLEET_RUN_DIR`.
- **State**: `~/.local/share/fleet-cli/<fleet>.json`
- **Queue**: `~/.local/share/fleet-cli/<fleet>-proposals.jsonl`
- **Proposal**: compact JSONL line in `proposals.jsonl` for HITL.
- **Event**: emitted in `result.json`; other loops listen via `on`.

Loops write `result.json` and `proposals.jsonl` (compact JSONL, one line per proposal).

## Loop modes

- **Scheduled**: `schedule: "daily@09:00"`, `hourly`, `every 15m`, etc. Renders a systemd timer.
- **Event-driven**: `on: [prospect.found]`. Runner BFS-triggers listeners.
- **Daemon**: `mode: daemon` + `runtime_max: 1h`. Renders `Type=simple`, `RuntimeMaxSec`, `Restart=always`. The script loops internally and exits before the limit; systemd respawns it.

## HITL flow

1. Loop writes compact `proposals.jsonl`.
2. Runner queues them, mints a relais inbox, and sends a cuzz notification with one-tap approve/reject URLs.
3. Human taps the `Approve:` or `Reject:` URL, or runs `fleet approve <fleet> <id>` / `fleet reject <fleet> <id>`.
4. The `relais_poll` loop (or `fleet relais-poll <fleet>`) reads the inbox and updates the queue.
5. `dispatch` loop emits `action.approved` and appends `pending_actions`.
6. `execute` loop calls `handlers/<kind>.sh` with `FLEET_LIVE=1` to actually post.

## Relais HITL details

- Each proposal creates one relais inbox on `relais.intrane.fr`.
- The cuzz message contains `Approve:` and `Reject:` capability URLs; opening one captures the decision.
- The `relais_poll` loop (scheduled `@every 1m`) polls relais and updates the queue.
- The relais inbox token is stored in fleet state (`relais_tokens.<proposal_id>`), never in the queue.
- Free inboxes are one-hour TTL. Set `RELAIS_PEAGE_WALLET` in `/etc/default/fleet-cli` for persistent inboxes.

## Daemon pattern

- Run multiple cycles with sleeps (e.g., 4 x 15 min).
- Track `proposed_targets` to avoid duplicates.
- Call sub-loops in child `FLEET_RUN_DIR` to avoid shadowing main output.
- Write compact `proposals.jsonl` with `jq -c -n`.
- Exit before `runtime_max` so systemd does not SIGKILL before queuing.
- Example: `fleets/machin-growth/loops/growth_agent.sh`.

## LLM calls

For non-interactive `devin`:
```sh
devin --model <model> --print \
  --permission-mode auto \
  --respect-workspace-trust false \
  --prompt-file <file>
```

## Common pitfalls

- **Missing HOME in systemd**: causes relative `FLEET_STATE_FILE`. Fix by setting `HOME` in the unit or `/etc/default/fleet-cli`.
- **Multi-line `proposals.jsonl`**: use `jq -c -n`.
- **Handler target parsing**: support both `owner/repo#46` and full GitHub URLs.
- **`execute` number parsing**: use `meta.number` first, then split target on `#`.
- **Do not rely on `os.UserHomeDir()`** when `HOME` is unset.

## Reference memgraph slugs

For the full, searchable graph of project knowledge, use memgraph in the fleet-cli project:
- `[fleet-cli-graph-concepts]`
- `[fleet-add-new-fleet]`
- `[fleet-loop-modes]`
- `[fleet-hitl-flow]`
- `[fleet-daemon-pattern]`
- `[fleet-growth-agent]`
- `[fleet-common-pitfalls]`
- `[fleet-cli-commands]`
- `[fleet-event-graph]`
- `[rbm21-growth-fleets]`
