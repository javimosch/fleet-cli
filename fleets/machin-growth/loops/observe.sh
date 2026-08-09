#!/usr/bin/env bash
# observe.sh — record machin repo metrics.
set -euo pipefail

repo="${FLEET_REPO:-javimosch/machin}"
metrics=$(gh api "repos/$repo" --jq '{
  stars: .stargazers_count,
  forks: .forks_count,
  open_issues: .open_issues_count,
  watchers: .subscribers_count,
  updated_at: .updated_at,
  pushed_at: .pushed_at
}')

# Append a daily observation; keep a rolling history.
mkdir -p "$FLEET_RUN_DIR"
cat > "$FLEET_RUN_DIR/state.json" <<EOF
{
  "stars": $metrics
}
EOF

# Build a JSON result the runner can also interpret as an event.
cat > "$FLEET_RUN_DIR/result.json" <<EOF
{
  "events": [{"name": "stars.recorded", "data": $metrics}]
}
EOF
