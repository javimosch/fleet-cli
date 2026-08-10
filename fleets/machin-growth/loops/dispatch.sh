#!/usr/bin/env bash
# dispatch.sh — emit approved actions so an agent or human can execute them.
set -euo pipefail

state_file="${FLEET_STATE_FILE:-}"
queue_file="${FLEET_QUEUE_FILE:-}"
run_dir="${FLEET_RUN_DIR:-/tmp/fleet-run}"
mkdir -p "$run_dir"

dry="${FLEET_DRY_RUN:-false}"
max_per_run=2

if [[ -z "$queue_file" || ! -f "$queue_file" ]]; then
  cat > "$run_dir/result.json" <<'EOF'
{ "events": [{"name": "proposal.dispatched", "data": {"count": 0, "dry_run": false}}] }
EOF
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
  echo "$candidates" | jq -r '.[] | "[DRY-RUN] action.approved: \(.id) -> \(.target)"' >&2
fi

# Build action.approved events for each candidate.
events=$(echo "$candidates" | jq --arg dry "$dry" -c '
  [
    {"name": "proposal.dispatched", "data": {"count": length, "dry_run": ($dry == "true")}}
  ]
  + map({
    "name": "action.approved",
    "data": {
      "id": .id,
      "kind": .kind,
      "target": .target,
      "body": .body,
      "meta": .meta,
      "approved_at": .approved_at,
      "approver": .approver
    }
  })
')

# Record dispatched IDs in state so we don't re-emit them.
# Never record in a dry-run; otherwise a non-dry run later would skip them.
if [[ "$dry" != "true" ]]; then
  ids=$(echo "$candidates" | jq -r '[.[].id // empty]')
  dispatched_map=$(jq -r '(.dispatched // {})' "$state_file" 2>/dev/null || echo '{}')
  updated_dispatched=$(echo "$dispatched_map" | jq --arg today "$today" --argjson ids "$ids" '.[$today] = $ids')
  cat > "$run_dir/state.json" <<EOF
{
  "dispatched": $updated_dispatched
}
EOF
fi

cat > "$run_dir/result.json" <<EOF
{
  "events": $events
}
EOF
