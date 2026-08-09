#!/usr/bin/env bash
# draft.sh — turn scored prospects into outbound proposals for human approval.
set -euo pipefail

state_file="${FLEET_STATE_FILE:-}"
run_dir="${FLEET_RUN_DIR:-/tmp/fleet-run}"
mkdir -p "$run_dir"

if [[ -z "$state_file" || ! -f "$state_file" ]]; then
  echo '[]' > "$run_dir/proposals.jsonl"
  exit 0
fi

prospects=$(jq -r '.prospects // []' "$state_file")

max_drafts=5
if [[ "${FLEET_DRY_RUN:-false}" == "true" ]]; then
  echo "# dry-run: drafting proposals only" >&2
fi

echo "$prospects" | jq -c --argjson max "$max_drafts" --arg repo javimosch/machin '
  .[:$max] |
  .[] |
  select(.score >= 70) |
  {
    kind: "issue_comment",
    target: .url,
    body: ("Hi — I built a small stack-based language/VM called machin that compiles to tiny static binaries (single file, no runtime).\n\nYour issue \"" + .title + "\" looks like the exact problem we optimized for. I am the author, so full disclosure, but I would be happy to answer questions or point you to a minimal example if it helps.\n\nRepo: https://github.com/" + $repo + "\n\nThanks for considering it."),
    meta: {
      repo: (.repository.nameWithOwner // "unknown"),
      title: .title,
      score: .score,
      query: .query,
      reason: "machin is relevant to the single/static binary goal of the issue"
    }
  }
' > "$run_dir/proposals.jsonl" 2>/dev/null || true

count=$(jq -s 'length' "$run_dir/proposals.jsonl" 2>/dev/null || echo 0)
cat > "$run_dir/result.json" <<EOF
{
  "events": [{"name": "proposal.created", "data": {"count": $count}}]
}
EOF
