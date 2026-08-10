#!/usr/bin/env bash
# handlers/issue_comment.sh — post an issue comment for a machin-growth action.
set -euo pipefail

action_file="${1:-${FLEET_ACTION_FILE:-}}"
if [[ -z "$action_file" || ! -f "$action_file" ]]; then
  echo '{"error":"no action file"}'
  exit 1
fi

live="${FLEET_LIVE:-0}"
if [[ "${FLEET_DRY_RUN:-false}" == "true" ]]; then
  live=0
fi

target=$(jq -r '.target // ""' "$action_file")
body=$(jq -r '.body // ""' "$action_file")
meta_repo=$(jq -r '.meta.repo // ""' "$action_file")

if [[ -z "$target" || -z "$body" ]]; then
  echo '{"error":"missing target or body"}'
  exit 1
fi

# Parse repo and issue number from target URL.
url_repo=$(echo "$target" | sed -n 's#https://github\.com/\([^/]*/[^/]*\)/issues/.*#\1#p')
number=$(echo "$target" | sed -n 's#.*/issues/\([0-9]*\)$#\1#p')

if [[ -n "$meta_repo" && "$meta_repo" != "$url_repo" ]]; then
  echo "{\"error\":\"repo mismatch: $meta_repo vs $url_repo\"}"
  exit 1
fi

repo="${meta_repo:-$url_repo}"
if [[ -z "$repo" || -z "$number" ]]; then
  echo '{"error":"cannot parse repo/number from target"}'
  exit 1
fi

body_file=$(mktemp)
trap 'rm -f "$body_file"' EXIT
printf '%s\n' "$body" > "$body_file"

if [[ "$live" == "1" ]]; then
  comment_url=$(gh issue comment "$number" -R "$repo" --body-file "$body_file")
  echo "{\"posted\": true, \"repo\": \"$repo\", \"number\": $number, \"comment_url\": \"$comment_url\"}"
else
  echo "{\"dry_run\": true, \"repo\": \"$repo\", \"number\": $number}"
fi
