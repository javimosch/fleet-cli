#!/usr/bin/env bash
# execute.sh — run pending approved actions through kind-specific handlers.
set -euo pipefail

state_file="${FLEET_STATE_FILE:-}"
fleet_dir="${FLEET_DIR:-.}"
run_dir="${FLEET_RUN_DIR:-/tmp/fleet-run}"
mkdir -p "$run_dir"

dry="${FLEET_DRY_RUN:-false}"
live="${FLEET_LIVE:-0}"

if [[ "$dry" == "true" ]]; then
  live=0
fi

# Pending actions come from the dispatch loop's state_append.
pending='[]'
if [[ -n "$state_file" && -f "$state_file" ]]; then
  pending=$(jq -r '.pending_actions // []' "$state_file" 2>/dev/null || echo '[]')
fi

count=$(echo "$pending" | jq 'length')
if [[ "$count" -eq 0 ]]; then
  cat > "$run_dir/result.json" <<EOF
{
  "events": [{"name": "action.executed", "data": {"count": 0, "dry_run": ($dry == "true")}}]
}
EOF
  exit 0
fi

remaining_file="$run_dir/remaining.json"
echo "$pending" > "$remaining_file"

executed=0
failed=0

while true; do
  action=$(jq '.[0]' "$remaining_file" 2>/dev/null || echo 'null')
  if [[ "$action" == "null" ]]; then
    break
  fi

  kind=$(echo "$action" | jq -r '.kind // ""')
  handler="$fleet_dir/handlers/${kind}.sh"

  if [[ ! -x "$handler" ]]; then
    echo "[execute] no handler for kind '$kind'; skipping" >&2
    # Remove from remaining so we don't loop forever.
    jq '.[1:]' "$remaining_file" > "$remaining_file.tmp" && mv "$remaining_file.tmp" "$remaining_file"
    continue
  fi

  action_file="$run_dir/action.json"
  echo "$action" > "$action_file"

  if [[ "$live" == "1" ]]; then
    if bash "$handler" "$action_file" >&2; then
      executed=$((executed + 1))
      jq '.[1:]' "$remaining_file" > "$remaining_file.tmp" && mv "$remaining_file.tmp" "$remaining_file"
    else
      failed=$((failed + 1))
      echo "[execute] handler $handler failed; stopping" >&2
      break
    fi
  else
    id=$(echo "$action" | jq -r '.id // ""')
    target=$(echo "$action" | jq -r '.target // ""')
    echo "[DRY-RUN] execute: $handler $action_file  ($id -> $target)" >&2
    executed=$((executed + 1))
    jq '.[1:]' "$remaining_file" > "$remaining_file.tmp" && mv "$remaining_file.tmp" "$remaining_file"
  fi
done

if [[ "$live" == "1" && "$dry" != "true" ]]; then
  dry_bool="false"
else
  dry_bool="true"
fi

remaining=$(cat "$remaining_file")

cat > "$run_dir/result.json" <<EOF
{
  "events": [
    {"name": "action.executed", "data": {"count": $executed, "dry_run": $dry_bool, "failed": $failed}}
  ]
}
EOF

if [[ "$live" == "1" && "$dry" != "true" ]]; then
  cat > "$run_dir/state.json" <<EOF
{
  "pending_actions": $remaining
}
EOF
fi
