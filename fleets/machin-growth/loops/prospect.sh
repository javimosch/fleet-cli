#!/usr/bin/env bash
# prospect.sh — find GitHub issues where machin is genuinely relevant and watch engaged threads for follow-ups.
set -euo pipefail

repo="${FLEET_REPO:-javimosch/machin}"
run_dir="${FLEET_RUN_DIR:-/tmp/fleet-run}"
state_file="${FLEET_STATE_FILE:-}"
mkdir -p "$run_dir"

dry="${FLEET_DRY_RUN:-false}"

# Clean temp files from any previous run.
rm -f "$run_dir"/raw-*.json "$run_dir"/engaged-*.json "$run_dir"/followups.jsonl

echo '[]' > "$run_dir/prospects.json"

queries=(
  'single binary language:go is:issue is:open'
  'static binary language:go is:issue is:open'
  'tiny binary language:go is:issue is:open'
  'CGO_ENABLED=0 language:go is:issue is:open'
  'go build static binary language:go is:issue is:open'
)

for q in "${queries[@]}"; do
  out="$run_dir/raw-$RANDOM.json"
  gh search issues "$q" --limit 10 \
    --json number,title,url,repository,createdAt,commentsCount,body \
    | jq --arg q "$q" 'map(. + {query: $q})' \
    > "$out" 2>/dev/null || true
  if ! jq -e . "$out" >/dev/null 2>&1; then
    echo '[]' > "$out"
  fi
  sleep 3
done

# Merge all raw arrays, dedupe by URL, score, filter low / bad fits.
# Regexes use [] and (^|[^a-zA-Z0-9_]) to avoid jq string-escape pitfalls.
cat "$run_dir"/raw-*.json 2>/dev/null | jq -s 'add | group_by(.url) | map(.[0]) |
  map(. as $p |
    ($p.title // "") as $title |
    ($p.body // "") as $body |
    ($title + " " + $body) as $text |
    (
      (if ($text | test("single[ -]?binary|static[ -]?binary|tiny[ -]?(binary|executable)|standalone"; "i")) then 30 else 0 end)
      + (if ($text | test("golang|(^|[^a-zA-Z0-9_])go([^a-zA-Z0-9_]|$)"; "i")) then 25 else 0 end)
      + (if ($text | test("compile|compiler|build|distribution|release"; "i")) then 15 else 0 end)
      + (if ($text | test("(^|[^a-zA-Z0-9_])binary([^a-zA-Z0-9_]|$)"; "i")) then 5 else 0 end)
      + (if ($text | test("compatibility|portability|portable|no[ -]?runtime|self[- ]?contained|deploy"; "i")) then 10 else 0 end)
      + (if ($text | test("CGO_ENABLED|(^|[^a-zA-Z0-9_])cgo([^a-zA-Z0-9_]|$)"; "i")) then 5 else 0 end)
      + (if ($text | test("pure[ -]?go|purego|no[ -]?cgo|without[ -]?cgo"; "i")) then 10 else 0 end)
      + (if ($text | test("(^|[^a-zA-Z0-9_])scratch([^a-zA-Z0-9_]|$)|distroless"; "i")) then 10 else 0 end)
      + (if (($p.commentsCount // 0) > 0) then 5 else 0 end)
      - (if ($text | test("pyinstaller|python|php|clojure|electron|(^|[^a-zA-Z0-9_])rust([^a-zA-Z0-9_]|$)|(^|[^a-zA-Z0-9_])java([^a-zA-Z0-9_]|$)|c#|dotnet|[.]net|node|javascript|typescript|npm|(^|[^a-zA-Z0-9_])py([^a-zA-Z0-9_]|$)"; "i")) then 80 else 0 end)
      - (if ($text | test("(^|[^a-zA-Z0-9_])docker([^a-zA-Z0-9_]|$)|(^|[^a-zA-Z0-9_])kubernetes([^a-zA-Z0-9_]|$)|(^|[^a-zA-Z0-9_])container([^a-zA-Z0-9_]|$)"; "i")) then 40 else 0 end)
      - (if (([$text | match("(^|[^a-zA-Z0-9_])test([^a-zA-Z0-9_]|$)"; "g")] // []) | length) > 2 then 20 else 0 end)
      - (if ($text | test("(^|[^a-zA-Z0-9_])(ui|gui|game|gaming|window|appearance|graphics|graphic)([^a-zA-Z0-9_]|$)"; "i")) then 40 else 0 end)
    ) as $score |
    $p + {
      score: $score,
      reason: (if $score >= 70 then "title/body strongly match a Go static-binary use case"
               elif $score >= 40 then "mentions single/static binaries but may have language mismatch"
               else "low relevance" end),
      body: ($body | .[0:2000])
    }
    | select(.score >= 30)
  )
  | sort_by(-.score)
  | .[:30]
' > "$run_dir/prospects.json" 2>/dev/null || echo '[]' > "$run_dir/prospects.json"

# ---- Adjust for actual repository primary language ----
# Search results can be noisy, so verify the repo language and re-score.
if [[ -s "$run_dir/prospects.json" ]]; then
  tmp="$run_dir/prospects-lang.json"
  : > "$tmp"
  while IFS= read -r row; do
    repo=$(echo "$row" | jq -r '.repository.nameWithOwner')
    lang=$(gh api "repos/$repo" --jq '.language' 2>/dev/null || echo "null")
    if [[ "$lang" == "Go" ]]; then
      echo "$row" | jq --arg lang "$lang" '. + {repo_language: $lang, score: (.score + 25)}' >> "$tmp"
    elif [[ -n "$lang" && "$lang" != "null" ]]; then
      echo "$row" | jq --arg lang "$lang" '. + {repo_language: $lang, score: (.score - 50)}' >> "$tmp"
    else
      echo "$row" | jq --arg lang "unknown" '. + {repo_language: $lang}' >> "$tmp"
    fi
  done < <(jq -c '.[]' "$run_dir/prospects.json")

  if [[ -s "$tmp" ]]; then
    jq -s 'map(. + {
      reason: (if .score >= 70 then "title/body strongly match a Go static-binary use case"
               elif .score >= 40 then "mentions single/static binaries but may have language mismatch"
               else "low relevance" end)
    })
    | sort_by(-.score)
    | .[:20]' "$tmp" > "$run_dir/prospects-scored.json"
    mv "$run_dir/prospects-scored.json" "$run_dir/prospects.json"
  fi
fi

prospects=$(cat "$run_dir/prospects.json")
prospect_count=$(echo "$prospects" | jq 'length')

# ---- Follow-up detection on engaged threads ----
engaged='[]'
if [[ -n "$state_file" && -f "$state_file" ]]; then
  engaged=$(jq -r '.engaged // []' "$state_file" 2>/dev/null || echo '[]')
fi

: > "$run_dir/followups.jsonl"
: > "$run_dir/updated-engaged.jsonl"

now=$(date -u +%Y-%m-%dT%H:%M:%SZ)
max_engaged=10
checked=0

echo "$engaged" | jq -c '.[]' 2>/dev/null | head -n "$max_engaged" | while IFS= read -r e; do
  er=$(echo "$e" | jq -r '.repo // ""')
  en=$(echo "$e" | jq -r '.number // ""')
  last=$(echo "$e" | jq -r '.last_checked_at // "1970-01-01T00:00:00Z"')

  if [[ -z "$er" || -z "$en" ]]; then
    echo "$e" >> "$run_dir/updated-engaged.jsonl"
    continue
  fi

  raw=$(gh issue view "$en" -R "$er" --json comments 2>/dev/null || echo '{}')

  # Comments from people other than javimosch, newer than the last check.
  new_comments=$(echo "$raw" | jq --arg last "$last" '
    (.comments // []) |
    map(select(.createdAt > $last and .author.login != "javimosch"))
  ')

  followup_count=$(echo "$new_comments" | jq 'length')

  if [[ "$followup_count" -gt 0 ]]; then
    issue_title=$(echo "$raw" | jq -r '.title // ""')
    jq -n \
      --argjson e "$e" \
      --argjson comments "$new_comments" \
      --arg title "$issue_title" \
      '{
        repo: $e.repo,
        number: $e.number,
        target: $e.target,
        comment_url: $e.comment_url,
        posted_at: $e.posted_at,
        last_checked_at: $e.last_checked_at,
        issue_title: $title,
        new_comments: $comments
      }' >> "$run_dir/followups.jsonl"
  fi

  # Always refresh last_checked_at.
  echo "$e" | jq --arg now "$now" '. + {last_checked_at: $now}' >> "$run_dir/updated-engaged.jsonl"
  checked=$((checked + 1))
done

followups=$(jq -s . "$run_dir/followups.jsonl" 2>/dev/null || echo '[]')
updated_engaged=$(jq -s . "$run_dir/updated-engaged.jsonl" 2>/dev/null || echo '[]')
followup_count=$(echo "$followups" | jq 'length')

# Build events.
events='[{"name": "prospect.found", "data": {"count": '$prospect_count'}}]'
if [[ "$followup_count" -gt 0 ]]; then
  events=$(echo "$events" | jq -c --argjson n "$followup_count" '. + [{"name": "followup.found", "data": {"count": $n}}]')
fi

if [[ "$dry" == "true" ]]; then
  cat > "$run_dir/result.json" <<EOF
{
  "events": $events
}
EOF
else
  cat > "$run_dir/result.json" <<EOF
{
  "events": $events,
  "state_set": {
    "prospects": $prospects,
    "prospected_at": "$now",
    "engaged": $updated_engaged,
    "followups": $followups
  }
}
EOF
fi
