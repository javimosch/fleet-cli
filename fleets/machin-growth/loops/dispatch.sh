#!/usr/bin/env bash
# dispatch.sh — execute approved outbound proposals (or log in dry-run).
set -euo pipefail

state_file="${FLEET_STATE_FILE:-}"
queue_file="${FLEET_QUEUE_FILE:-}"
run_dir="${FLEET_RUN_DIR:-/tmp/fleet-run}"
mkdir -p "$run_dir"

dry="${FLEET_DRY_RUN:-false}"
max_per_run=2

if [[ -z "$queue_file" || ! -f "$queue_file" ]]; then
  echo '[]' > "$run_dir/planned.jsonl"
  exit 0
fi

# Select approved proposals not already dispatched today.
today=$(date -u +%Y-%m-%d)
dispatched_today=$(jq -r --arg today "$today" '(.dispatched // {})[$today] // []' "$state_file" 2>/dev/null || echo '[]')

candidates=$(jq -s --argjson dispatched "$dispatched_today" --argjson max "$max_per_run" '
  [.[] | select(.status == "approved" and .dispatched_at == null)] |
  [.[0:$max][] | select(.id as $id | $dispatched | index($id) | not)]
' "$queue_file")

if [[ "$dry" == "true" ]]; then
  echo "$candidates" | jq -r '.[] | "DRY-RUN would post to \(.target):\n\(.body)\n---"' > "$run_dir/planned.jsonl" || true
  echo "$candidates" | jq -r '.[] | "[DRY-RUN] proposal \(.id) -> \(.target)"' >&2
fi

# Record dispatched IDs in state so we don't retry the same proposals.
ids=$(echo "$candidates" | jq -r '[.[].id // empty]')
count=$(echo "$ids" | jq 'length')

cat > "$run_dir/state.json" <<EOF
{
  "dispatched": {
    "$today": $ids
  }
}
EOF

cat > "$run_dir/result.json" <<EOF
{
  "events": [{"name": "proposal.dispatched", "data": {"count": $count, "dry_run": $dry}}]
}
EOF
