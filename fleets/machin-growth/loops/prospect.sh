#!/usr/bin/env bash
# prospect.sh — find GitHub issues where machin is genuinely relevant.
set -euo pipefail

repo="${FLEET_REPO:-javimosch/machin}"
run_dir="${FLEET_RUN_DIR:-/tmp/fleet-run}"
mkdir -p "$run_dir"

# Clean temp files from any previous run.
rm -f "$run_dir"/raw-*.json

echo '[]' > "$run_dir/prospects.json"

queries=(
  'single binary language:go is:issue is:open'
  'static binary language:go is:issue is:open'
  'tiny binary language:go is:issue is:open'
  'go compiler single binary is:issue is:open'
)

for q in "${queries[@]}"; do
  out="$run_dir/raw-$RANDOM.json"
  gh search issues "$q" --limit 10 \
    --json number,title,url,repository,createdAt,commentsCount,body \
    | jq --arg q "$q" 'map(. + {query: $q})' \
    > "$out" 2>/dev/null || true
  # If gh/jq wrote non-JSON, replace with [] so later jq doesn't choke.
  if ! jq -e . "$out" >/dev/null 2>&1; then
    echo '[]' > "$out"
  fi
done

# Merge all raw arrays, dedupe by URL, score, filter low / bad fits.
# Each jq regex uses \\ to emit a single \ to the regex engine.
cat "$run_dir"/raw-*.json 2>/dev/null | jq -s 'add | group_by(.url) | map(.[0]) |
  map(. as $p |
    ($p.title // "") as $title |
    ($p.body // "") as $body |
    ($title + " " + $body) as $text |
    # Base relevance to tiny/static single binaries (allow hyphen/space)
    (
      (if ($text | test("single[ -]?binary|static[ -]?binary|tiny[ -]?(binary|executable)|standalone"; "i")) then 30 else 0 end)
      # Bonus for Go / compile / build context
      + (if ($text | test("golang|(^|[^a-zA-Z0-9_])go([^a-zA-Z0-9_]|$)"; "i")) then 25 else 0 end)
      + (if ($text | test("compile|compiler|build|distribution"; "i")) then 15 else 0 end)
      # Slight boost for active threads
      + (if (($p.commentsCount // 0) > 0) then 5 else 0 end)
      # Strong penalties for languages / contexts machin cannot help with
      - (if ($text | test("pyinstaller|python|electron|rust|(^|[^a-zA-Z0-9_])java([^a-zA-Z0-9_]|$)|c#|dotnet|[.]net|node|javascript|typescript|npm|(^|[^a-zA-Z0-9_])py([^a-zA-Z0-9_]|$)"; "i")) then 80 else 0 end)
      - (if ($text | test("(^|[^a-zA-Z0-9_])docker([^a-zA-Z0-9_]|$)|(^|[^a-zA-Z0-9_])kubernetes([^a-zA-Z0-9_]|$)|container"; "i")) then 40 else 0 end)
    ) as $score |
    $p + {
      score: $score,
      reason: (if $score >= 70 then "title/body strongly match a Go static-binary use case"
               elif $score >= 40 then "mentions single/static binaries but may have language mismatch"
               else "low relevance" end),
      body: ($body | .[0:2000])
    }
    | select(.score >= 40)
  )
  | sort_by(-.score)
  | .[:20]
' > "$run_dir/prospects.json" 2>/dev/null || echo '[]' > "$run_dir/prospects.json"

prospects=$(cat "$run_dir/prospects.json")
count=$(echo "$prospects" | jq 'length')

cat > "$run_dir/state.json" <<EOF
{
  "prospects": $prospects,
  "prospected_at": "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
}
EOF

cat > "$run_dir/result.json" <<EOF
{
  "events": [{"name": "prospect.found", "data": {"count": $count}}]
}
EOF
