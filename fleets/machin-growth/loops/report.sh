#!/usr/bin/env bash
# report.sh — produce a simple daily report of fleet state.
set -euo pipefail

state_file="${FLEET_STATE_FILE:-}"
queue_file="${FLEET_QUEUE_FILE:-}"
run_dir="${FLEET_RUN_DIR:-/tmp/fleet-run}"
mkdir -p "$run_dir"

stars=$(jq -r '.stars // {}' "$state_file" 2>/dev/null || echo '{}')
prospects_count=$(jq -r '(.prospects // []) | length' "$state_file" 2>/dev/null || echo 0)
pending=$(jq -s '[.[] | select(.status == "pending")] | length' "$queue_file" 2>/dev/null || echo 0)
approved=$(jq -s '[.[] | select(.status == "approved")] | length' "$queue_file" 2>/dev/null || echo 0)
rejected=$(jq -s '[.[] | select(.status == "rejected")] | length' "$queue_file" 2>/dev/null || echo 0)

cat > "$run_dir/result.json" <<EOF
{
  "state_set": {
    "report": {
      "generated_at": "$(date -u +%Y-%m-%dT%H:%M:%SZ)",
      "stars": $stars,
      "prospects_count": $prospects_count,
      "pending": $pending,
      "approved": $approved,
      "rejected": $rejected
    }
  }
}
EOF
