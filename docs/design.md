# fleet-cli design

## Purpose

`fleet-cli` turns the patterns learned from `am-fleet` into a reusable framework. A fleet is an engineered graph of loops: nodes are scripts, edges are events, and outbound edges are gated by human approval.

## Architecture

The Go binary is a thin scheduler + ledger + gate:

- `config` parses and validates `fleet.yml`.
- `state` persists a per-fleet JSON key/value store and run log.
- `hitl` manages the proposal queue.
- `runner` executes loop scripts and ingests their outputs.

All domain logic lives in `loops/*.sh`, so fleets can be written in any language and inspected by humans.

## Why start with `machin-growth`?

The GitHub star-growth fleet is a safe test case:

- Read-only loops (`observe`, `prospect`) exercise the config, state and event system.
- The `draft` loop generates outbound *proposals* but never posts.
- The `dispatch` loop, even without `--dry-run`, requires an approved proposal before it acts.
- Rate limits and per-target cooldowns are built in.

It demonstrates the framework without violating GitHub ToS or being spammy.

## Roadmap

- <s>`fleet install` / `fleet uninstall` — render systemd user timers.</s> Done.
- `fleet report` — publish a hart dashboard.
- `fleet emit` — trigger event edges manually.
- Event-driven scheduling: a loop with `on: [event]` runs when the event fires.
- Token-bucket rate limiter in the binary, not just the loop scripts.
- SQLite backend for production use.
- Merge into `am` / `am-cloud` as `am fleet`.
