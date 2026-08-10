#!/usr/bin/env bash
# draft.sh — turn the best prospects into outbound action proposals for HITL.
set -euo pipefail

state_file="${FLEET_STATE_FILE:-}"
queue_file="${FLEET_QUEUE_FILE:-}"
run_dir="${FLEET_RUN_DIR:-/tmp/fleet-run}"
mkdir -p "$run_dir"

if [[ -z "$state_file" || ! -f "$state_file" ]]; then
  echo '[]' > "$run_dir/proposals.jsonl"
  exit 0
fi

prospects=$(jq -r '.prospects // []' "$state_file")
engaged=$(jq -r '.engaged // []' "$state_file" 2>/dev/null || echo '[]')

# Gather target URLs we should skip (already engaged or already queued).
engaged_targets=$(echo "$engaged" | jq '[.[].target // empty]')
queued_targets='[]'
if [[ -n "$queue_file" && -f "$queue_file" ]]; then
  queued_targets=$(jq -s '[.[] | .target]' "$queue_file" 2>/dev/null || echo '[]')
fi

max_drafts=3

# Only draft prospects that look like a strong match and are not duplicates.
echo "$prospects" | jq -c --argjson max "$max_drafts" --arg repo javimosch/machin --argjson engaged "$engaged_targets" --argjson queued "$queued_targets" '
  .[:$max] |
  .[] |
  # High bar: strong title/body match.
  select(.score >= 85 and (.reason | contains("strongly match"))) |
  # Skip if we already engaged or queued this target.
  select(.url as $u | $engaged | index($u) | not) |
  select(.url as $u | $queued | index($u) | not) |
  {
    kind: "issue_comment",
    target: .url,
    body: (
      "Hi — I work on a small stack-based language/VM called machin that compiles to a single static binary with no runtime.\n\n"
      + "I came across this issue while looking for places where that property might actually help. I am the author, so full disclosure, and I know this may not be the right fit for your project. If it is, I would be happy to answer questions or point you to a minimal example.\n\n"
      + "Repo: https://github.com/" + $repo + "\n\n"
      + "Thanks for considering it."
    ),
    meta: {
      repo: (.repository.nameWithOwner // "unknown"),
      title: .title,
      score: .score,
      query: .query,
      reason: "title and body strongly indicate a Go static/single-binary use case"
    }
  }
' > "$run_dir/proposals.jsonl" 2>/dev/null || true

count=$(jq -s 'length' "$run_dir/proposals.jsonl" 2>/dev/null || echo 0)
cat > "$run_dir/result.json" <<EOF
{
  "events": [{"name": "proposal.created", "data": {"count": $count}}]
}
EOF
