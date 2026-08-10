#!/usr/bin/env bash
# growth_agent.sh — agentic loop that prospects, uses GLM-5.2 to draft a
# helpful GitHub comment, and queues a HITL issue_comment proposal.
# Designed to run as a daemon: 4 cycles of ~15min each, then exit so
# systemd can respawn it on the hour to keep memory growth bounded.
set -euo pipefail

state_file="${FLEET_STATE_FILE:-}"
queue_file="${FLEET_QUEUE_FILE:-}"
run_dir="${FLEET_RUN_DIR:-/tmp/fleet-run}"
fleet_dir="${FLEET_DIR:-.}"
mkdir -p "$run_dir"

model="${AGENT_MODEL:-glm-5-2}"
cycles=4
sleep_min=15
timeout_sec=120
proposals=0
dry="${FLEET_DRY_RUN:-false}"

if [[ "$dry" == "true" ]]; then
  sleep_min=1
fi

# Use absolute path to devin; the systemd PATH may not include ~/.local/bin.
devin_bin="/root/.local/bin/devin"

# Queued/pending/approved/rejected targets so we don't create duplicates.
existing_targets() {
  if [[ -n "$queue_file" && -f "$queue_file" ]]; then
    jq -r '.[].target // empty' "$queue_file" 2>/dev/null | sort -u
  else
    echo ""
  fi
}

# Run prospect child in its own run dir so it updates state without
# shadowing this loop's outputs.
refresh_prospects() {
  local child_dir="$run_dir/prospect-child"
  mkdir -p "$child_dir"
  FLEET_RUN_DIR="$child_dir" \
  FLEET_STATE_FILE="$state_file" \
  FLEET_DRY_RUN="${FLEET_DRY_RUN:-false}" \
    bash "$fleet_dir/loops/prospect.sh" >/dev/null 2>&1 || true
}

# Pick the highest-scored prospect whose target isn't already in the queue.
pick_top() {
  local excl
  excl=$(existing_targets)
  if [[ -n "$state_file" && -f "$state_file" ]]; then
    jq -r --arg exclude "$excl" '
      ($exclude | split("\n") | map(select(. != ""))) as $xs |
      (.prospects // []) |
      map(select(.issue.number != null and .repository.nameWithOwner != null)) |
      map(.target = "\(.repository.nameWithOwner)#\(.issue.number)") |
      map(select(.target as $t | $xs | index($t) | not)) |
      sort_by(-.score) |
      .[0] // empty
    ' "$state_file" 2>/dev/null
  else
    echo ""
  fi
}

# Call devin in non-interactive print mode with a constrained prompt.
# We use --respect-workspace-trust false and --permission-mode auto so
# the headless daemon never blocks on a trust/approval prompt.
draft_with_devin() {
  local prompt_file="$1"
  local out="$2"
  timeout "$timeout_sec" "$devin_bin" \
    --model "$model" \
    --print \
    --permission-mode auto \
    --respect-workspace-trust false \
    --prompt-file "$prompt_file" \
    > "$out" 2>&1 || true
  # Print mode emits the assistant reply on stdout, sometimes with
  # surrounding whitespace or a trailing dot; keep the first clean block.
  sed -E 's/^[[:space:]]+|[[:space:]]+$//g' "$out" \
    | sed -E '/^$/d' \
    | head -n 20
}

for i in $(seq 1 $cycles); do
  refresh_prospects
  top=$(pick_top)

  if [[ -z "$top" ]]; then
    if [[ $i -lt $cycles ]]; then
      sleep $((sleep_min * 60))
    fi
    continue
  fi

  repo=$(echo "$top" | jq -r '.repository.nameWithOwner')
  number=$(echo "$top" | jq -r '.issue.number')
  title=$(echo "$top" | jq -r '.issue.title')
  issue_url=$(echo "$top" | jq -r '.issue.url')
  body=$(echo "$top" | jq -r '.issue.body[0:800]')
  score=$(echo "$top" | jq -r '.score')
  target="$repo#$number"

  if [[ "$dry" == "true" ]]; then
    comment="[DRY-RUN] This is a placeholder comment for $target."
  else
    prompt_file="$run_dir/prompt-$i.txt"
    cat > "$prompt_file" <<EOF
You are drafting a helpful, non-spammy GitHub issue comment to grow interest in machin (github.com/javimosch/machin), a single-binary Go/WASM build and distribution toolchain.

Issue to respond to:
- Repository: $repo
- Issue #$number: $title
- URL: $issue_url
- Body excerpt: $body

Write a short, friendly, technical comment (max 120 words) that:
1. Acknowledges the issue context.
2. Briefly mentions that machin can help turn Go projects into tiny static binaries or WASM modules.
3. Asks one specific, engaging question relevant to the issue.
4. Avoids marketing language, buzzwords, and emojis.

Output ONLY the comment text, no markdown fences, no signatures, no explanations.
EOF

    out_file="$run_dir/devin-$i.out"
    comment=$(draft_with_devin "$prompt_file" "$out_file")

    if [[ -z "$comment" || ${#comment} -lt 20 ]]; then
      echo "agent: no usable comment for $target (devin output too short)" >&2
      if [[ $i -lt $cycles ]]; then
        sleep $((sleep_min * 60))
      fi
      continue
    fi
  fi

  # Queue a HITL proposal for issue_comment.
  jq -n \
    --arg kind "issue_comment" \
    --arg target "$target" \
    --arg body "$comment" \
    --arg repo "$repo" \
    --arg number "$number" \
    --arg issue_url "$issue_url" \
    --arg score "$score" \
    '{
      kind: $kind,
      target: $target,
      body: $body,
      meta: {repo: $repo, number: $number, issue_url: $issue_url, score: ($score | tonumber), source: "growth_agent"}
    }' >> "$run_dir/proposals.jsonl"

  proposals=$((proposals + 1))
  echo "agent: queued proposal for $target" >&2

  if [[ $i -lt $cycles ]]; then
    sleep $((sleep_min * 60))
  fi
done

cat > "$run_dir/result.json" <<EOF
{
  "events": [{"name": "agent.suggested", "data": {"proposals": $proposals}}]
}
EOF
