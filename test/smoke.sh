#!/usr/bin/env bash
# smoke.sh — basic end-to-end and Agent-First CLI contract tests.
# Uses temp state and binaries so it does not touch prod state.
set -euo pipefail

cd "$(dirname "$0")/.."

BIN=$(mktemp)
STATE_DIR=$(mktemp -d)
OUT=$(mktemp)
ERR=$(mktemp)
trap 'rm -f "$BIN" "$OUT" "$ERR"; rm -rf "$STATE_DIR"' EXIT

go build -o "$BIN" ./cmd/fleet
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

echo "== agent-first discovery =="
fleet help-json | jq -e '.commands.guide and .commands.feedback and .commands.version and .exit_codes["80"]' >/dev/null
fleet guide | jq -e '.guide.loop and .guide.concepts and .guide.gotchas' >/dev/null
fleet guide --human | grep -q '^# fleet guide'
fleet version | jq -e '.ok == true and .version == "0.1.0"' >/dev/null

set +e
fleet unknown-command >"$OUT" 2>"$ERR"
RC=$?
set -e
assert_eq 80 "$RC" "unknown command exit code"
test ! -s "$OUT"
jq -e '.ok == false and .error.code == 80 and .error.type == "unknown_command" and .error.recoverable == false' "$ERR" >/dev/null

FEEDBACK_RELAY=off fleet feedback "local smoke" --kind bug >"$OUT" 2>"$ERR"
jq -e '.ok == true and .data.stored == 0 and .data.relayed == 0 and (.data.id | length) == 32' "$OUT" >/dev/null
test ! -s "$ERR" || grep -q 'FEEDBACK_RELAY=off' "$ERR"

echo "== validate =="
fleet validate machin-growth | jq -e '.ok == true and .valid == true'

echo "== plan =="
fleet plan machin-growth | jq -e 'length > 0'
echo "== run observe =="
fleet run machin-growth observe --no-chain | jq -e '.status == "ok"'
echo "== run prospect (dry, no chain) =="
RUNS_BEFORE=$(fleet state machin-growth get runs | jq 'length // 0')
fleet run machin-growth prospect --dry-run --no-chain | jq -e '.dry_run == true'
RUNS_AFTER=$(fleet state machin-growth get runs | jq 'length // 0')
assert_eq "$RUNS_BEFORE" "$RUNS_AFTER" "dry-run does not record run history"
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
fleet approve machin-growth "$PID" --reason "smoke approved" | jq -e '.ok == true'
fleet reject machin-growth "$PID" --reason "smoke rejected" | jq -e '.ok == true'
echo "== dispatch (dry) =="
fleet run machin-growth dispatch --dry-run | jq -e '.status == "ok"'
echo "== execute (dry, no chain) =="
fleet run machin-growth execute --no-chain | jq -e '.status == "ok"'
echo "== status =="
fleet status machin-growth | jq -e '.fleet == "machin-growth"'
echo "== state has costs ledger =="
fleet state machin-growth get costs | jq -e 'length > 0'
echo "== all smoke tests passed =="
