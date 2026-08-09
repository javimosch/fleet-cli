#!/usr/bin/env bash
# prospect.sh — find public issues/discussions where machin is genuinely relevant.
set -euo pipefail

repo="${FLEET_REPO:-javimosch/machin}"
run_dir="${FLEET_RUN_DIR:-/tmp/fleet-run}"
mkdir -p "$run_dir"

touch "$run_dir/prospects.json"
echo '[]' > "$run_dir/prospects.json"

queries=(
  'single binary language:go is:issue is:open'
  'static binary language:go is:issue is:open'
  'tiny executable language:go is:issue is:open'
)

for q in "${queries[@]}"; do
  gh search issues "$q" --limit 5 --json number,title,url,repository,createdAt,commentsCount 2>/dev/null | \
    jq --arg q "$q" '.[] | .query = $q' > "$run_dir/raw-$RANDOM.json" || true
done

# Merge, dedupe by URL, score by relevance heuristics.
cat "$run_dir"/raw-*.json 2>/dev/null | jq -s '
  group_by(.url) | map(.[0]) |
  map(. + {
    score: (
      if (.title | test("single binary|static binary|tiny executable|standalone"; "i")) then 80 else 50 end
      + (.commentsCount // 0 | if . > 0 then 10 else 0 end)
    ),
    reason: "machin compiles to tiny static binaries; this issue asks for that property"
  }) |
  sort_by(-.score)
' > "$run_dir/prospects.json" 2>/dev/null || echo '[]' > "$run_dir/prospects.json"

prospects=$(cat "$run_dir/prospects.json")

cat > "$run_dir/state.json" <<EOF
{
  "prospects": $prospects,
  "prospected_at": "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
}
EOF

# Emit an event with count.
count=$(echo "$prospects" | jq 'length')
cat > "$run_dir/result.json" <<EOF
{
  "events": [{"name": "prospect.found", "data": {"count": $count}}]
}
EOF
