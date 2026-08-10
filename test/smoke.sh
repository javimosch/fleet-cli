#!/usr/bin/env bash
# smoke.sh — basic end-to-end smoke tests for fleet-cli.
# Uses a temp state directory so it does not touch prod state.
set -euo pipefail

cd "$(dirname "$0")/.."

BIN=$(mktemp)
trap 'rm -f "$BIN"' EXIT

go build -o "$BIN" ./cmd/fleet

STATE_DIR=$(mktemp -d)
trap 'rm -rf "$STATE_DIR"' EXIT
export FLEET_STATE_DIR="$STATE_DIR"
export FLEET_DIR="$(pwd)/fleets/machin-growth"

fleet() { "$BIN" "$@"; }

assert_eq() {
  local expected="$1" actual="$2" msg="$3"
  if [[ "$expected" != "$actual" ]]; then
    echo "FAIL: $msg (expected $expected, got $actual)"
    exit 1
  fi
}

echo "== validate =="
fleet validate machin-growth

echo "== plan =="
fleet plan machin-growth | jq -e 'length > 0'

echo "== run observe =="
fleet run machin-growth observe --no-chain | jq -e '.status == "ok"'

echo "== run prospect (dry, no chain) =="
fleet run machin-growth prospect --dry-run --no-chain | jq -e '.dry_run == true'

echo "== emit prospect.found (dry) =="
fleet emit machin-growth prospect.found --dry-run | jq -e '.chain | length == 1'

echo "== run prospect (live chain) =="
fleet run machin-growth prospect | jq -e '.status == "ok" and (.chain | length == 1)'

echo "== queue should have at least one proposal =="
COUNT=$(fleet queue machin-growth | jq 'length')
if [[ "$COUNT" -lt 1 ]]; then
  echo "FAIL: queue is empty but should have proposals"
  exit 1
fi
PID=$(fleet queue machin-growth | jq -r '.[0].id')

echo "== approve and reject the proposal =="
fleet approve machin-growth "$PID" --reason "smoke approved"
fleet reject machin-growth "$PID" --reason "smoke rejected"

echo "== dispatch (dry) =="
fleet run machin-growth dispatch --dry-run | jq -e '.status == "ok"'

echo "== execute (dry, no chain) =="
fleet run machin-growth execute --no-chain | jq -e '.status == "ok"'

echo "== status =="
fleet status machin-growth | jq -e '.fleet == "machin-growth"'

echo "== state has costs ledger =="
fleet state machin-growth get costs | jq -e 'length > 0'

echo "== all smoke tests passed =="
