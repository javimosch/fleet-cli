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

if [[ "$live" == "1" && "$dry" != "true" ]]; then
  dry_bool="false"
else
  dry_bool="true"
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
  "events": [{"name": "action.executed", "data": {"count": 0, "dry_run": $dry_bool, "failed": 0}}]
}
EOF
  exit 0
fi

remaining_file="$run_dir/remaining.json"
echo "$pending" > "$remaining_file"

engaged_file="$run_dir/engaged.jsonl"
: > "$engaged_file"

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
    jq '.[1:]' "$remaining_file" > "$remaining_file.tmp" && mv "$remaining_file.tmp" "$remaining_file"
    continue
  fi

  action_file="$run_dir/action.json"
  echo "$action" > "$action_file"

  if [[ "$live" == "1" ]]; then
    handler_out_file="$run_dir/handler-out.json"
    if bash "$handler" "$action_file" > "$handler_out_file" 2>&1; then
      # Capture handler result (last line of JSON output).
      handler_result=$(tail -n 1 "$handler_out_file" 2>/dev/null || echo '{}')
      comment_url=$(echo "$handler_result" | jq -r '.comment_url // ""')

      now=$(date -u +%Y-%m-%dT%H:%M:%SZ)
      repo=$(echo "$action" | jq -r '.meta.repo // ""')
      number=$(echo "$action" | jq -r '.target | split("/") | last // ""')
      target=$(echo "$action" | jq -r '.target // ""')

      jq -n \
        --arg repo "$repo" \
        --arg number "$number" \
        --arg target "$target" \
        --arg comment_url "$comment_url" \
        --arg posted_at "$now" \
        --arg last_checked_at "$now" \
        '{
          repo: $repo,
          number: $number,
          target: $target,
          comment_url: $comment_url,
          posted_at: $posted_at,
          last_checked_at: $last_checked_at
        }' >> "$engaged_file"

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

remaining=$(cat "$remaining_file")

# Build events. If live, also include action.engaged events for monitoring.
events='[{"name": "action.executed", "data": {"count": '$executed', "dry_run": '$dry_bool', "failed": '$failed'}}]'
if [[ "$live" == "1" && "$dry" != "true" && -s "$engaged_file" ]]; then
  engaged_count=$(jq -s 'length' "$engaged_file")
  events=$(echo "$events" | jq -c --argjson n "$engaged_count" '. + [{"name": "action.engaged", "data": {"count": $n}}]')
fi

cat > "$run_dir/result.json" <<EOF
{
  "events": $events
}
EOF

# Only mutate state when we are actually executing.
if [[ "$live" == "1" && "$dry" != "true" ]]; then
  # Preserve existing engaged entries and append the new ones.
  existing_engaged=$(jq -r '.engaged // []' "$state_file" 2>/dev/null || echo '[]')
  new_engaged=$(jq -s . "$engaged_file" 2>/dev/null || echo '[]')
  engaged=$(jq -s 'add // []' <<< "$existing_engaged $new_engaged")

  cat > "$run_dir/state.json" <<EOF
{
  "pending_actions": $remaining,
  "engaged": $engaged
}
EOF
fi
