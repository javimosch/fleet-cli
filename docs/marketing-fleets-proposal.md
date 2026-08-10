<!-- Canonical memgraph slug: [marketing-fleets-proposal] -->

# Marketing/Growth Fleets for Micro-SaaS (fleet-cli)

Five fleets that follow the documented fleet-cli patterns: every loop is a shell script under `loops/`, every outbound action has a matching `handlers/<kind>.sh`, proposals are written as **compact** JSONL (`jq -c`) to `$FLEET_RUN_DIR/proposals.jsonl`, and nothing goes out without `fleet approve` + `FLEET_LIVE=1` on `execute`.

Shared conventions across all 5 fleets (copied from `machin-growth`):

- Each fleet has the standard tail: `dispatch` (`schedule: every 15m`, emits `action.approved`) → `execute` (`on: [action.approved]`, calls `handlers/<kind>.sh`, dry-runs unless `FLEET_LIVE=1`).
- State keys: `prospects`, `engaged`, `pending_actions`, `dispatched`, plus fleet-specific lists.
- Agentic drafting loops use `devin --model glm-5-2 --print --permission-mode auto --respect-workspace-trust false --prompt-file <f>` wrapped in `timeout 120`.
- `bin/gh` cost-tracking wrapper where GitHub API is used; `bin/grepapi` wrapper for the grepapi client on rbm21.
- Daemons: `mode: daemon`, `runtime_max: 1h`, 4 internal cycles, `AGENT_CYCLES` / `AGENT_SLEEP_MIN` overrides, `proposed_targets` dedupe inside the run.

---

## 1. `grepapi-growth` — Reddit pain-point mining → helpful comments

**Repo:** `javimosch/grepapi`
**Problem:** grepapi's whole value prop ("headless LinkedIn/Reddit search without an API key") is discussed daily in scraping/automation subreddits, but nobody knows the product exists. This fleet finds people actively asking for what grepapi does and drafts one genuinely useful reply per thread.

| Loop | Mode / schedule | Command | on | emits | outbound | approval |
|---|---|---|---|---|---|---|
| `reddit_prospect` | `schedule: every 30m` | `loops/reddit_prospect.sh` | — | `prospect.found` | false | false |
| `score` | event | `loops/score.sh` | `[prospect.found]` | `prospect.scored` | false | false |
| `draft_comment` | event | `loops/draft_comment.sh` | `[prospect.scored]` | `proposal.created` | **true** | **true** |
| `dispatch` | `schedule: every 15m` | `loops/dispatch.sh` | — | `action.approved` | false | false |
| `execute` | event | `loops/execute.sh` | `[action.approved]` | `action.executed` | **true** | false (already approved) |

- `reddit_prospect.sh` — calls the grepapi Reddit fetcher on rbm21 (`grepapi reddit search --q "<query>" --json`) for a query list, filters posts <48h old with no grepapi mention, appends new ones to state `prospects`.
- `score.sh` — pure-jq scoring: intent keywords (`"scrape"`, `"rate limited"`, `"API alternative"`), post age, comment count, subreddit weight; drops anything already in `engaged`.
- `draft_comment.sh` — takes the top un-queued scored post, drafts a technical, non-salesy comment via devin, writes a `reddit_comment` proposal with `jq -c`.
- `dispatch.sh` / `execute.sh` — the standard HITL tail.

**handlers/**: `reddit_comment.sh` (posts via the grepapi/authed-browser session on rbm21; accepts both `r/sub/comments/<id>` and full permalink), `reddit_dm.sh` (optional, follow-up).

**Event graph:**
```
reddit_prospect --prospect.found--> score --prospect.scored--> draft_comment
dispatch --action.approved--> execute --action.executed--> (state: engaged)
```

**HITL:** `draft_comment` is `requires_approval: true` — Reddit bans accounts for pattern-matched self-promo, so a human must read every comment for tone and check that it answers the question before mentioning the product. `execute` only posts what a human already approved, and only with `FLEET_LIVE=1`.

**First target:** `r/webscraping` — query `linkedin scraping without api` (secondary: `r/DataHoarder`, `r/Automate`, query `reddit search api alternative`).

---

## 2. `github-issue-growth` — answer open issues where grepapi is the answer

**Repo:** `javimosch/grepapi`
**Problem:** dozens of open GitHub issues on scraping libraries are literally "LinkedIn blocks me / Reddit API pricing killed my app". This is the exact `machin-growth` pattern retargeted from Go static binaries to headless-search pain.

| Loop | Mode / schedule | Command | on | emits | outbound | approval |
|---|---|---|---|---|---|---|
| `prospect` | `schedule: daily@08:30` | `loops/prospect.sh` | — | `prospect.found` | false | false |
| `growth_agent` | `mode: daemon`, `runtime_max: 1h` | `loops/growth_agent.sh` | — | `agent.suggested` | **true** | **true** |
| `dispatch` | `schedule: every 15m` | `loops/dispatch.sh` | — | `action.approved` | false | false |
| `execute` | event | `loops/execute.sh` | `[action.approved]` | `action.executed` | **true** | false |
| `followup` | `schedule: daily@18:00` | `loops/followup.sh` | `[action.executed]` | `followup.needed` | false | false |

- `prospect.sh` — `bin/gh` search over issue text, scores by reactions/recency/repo stars, writes `prospects` to state; keeps the previous good list if the search returns empty (rate-limit guard, per `[fleet-growth-agent]`).
- `growth_agent.sh` — daemon, 4 cycles × 15 min: refresh prospects via `prospect` in a child `FLEET_RUN_DIR`, pick the highest-scored un-queued issue, draft an `issue_comment` with devin, append a compact proposal, track `proposed_targets`.
- `followup.sh` — re-reads posted comments via `gh` and records reply/reaction counts into state `engagement_stats`; emits `followup.needed` when someone replied with a question.
- `dispatch.sh` / `execute.sh` — standard tail; `execute` prefers `meta.number`, falling back to splitting the target after `#`.

**handlers/**: `issue_comment.sh` (accepts `owner/repo#46` **and** `https://github.com/owner/repo/issues/46`), `issue_reply.sh` (thread replies for follow-ups).

**Event graph:**
```
prospect --prospect.found--> (state: prospects)
growth_agent (daemon) --agent.suggested--> (queue only; proposals land at process exit)
dispatch --action.approved--> execute --action.executed--> followup --followup.needed--> growth_agent
```

**HITL:** comments on other people's repos are the highest-reputation-risk channel — `growth_agent` proposals must be approved individually. Also cap it: reject anything targeting a repo already in `engaged`.

**First target:** `gh search issues "reddit api pricing" OR "linkedin blocked" state:open created:>2026-05-01 --json` restricted to repos with >200 stars; concretely the open scraping-blocked issues on `JustAnotherArchivist/snscrape`-style repos.

---

## 3. `microsaas-distribution` — directories, awesome-lists, launch surfaces

**Repo:** `javimosch/microsaas-distribution-fleet` (fleet definition; targets any product repo via `FLEET_REPO`)
**Problem:** every micro-SaaS launch leaks the same value: nobody systematically submits to the 40+ directories, awesome-lists, and aggregators, and nobody tracks which submission is still pending. This fleet turns distribution into a queue with state.

| Loop | Mode / schedule | Command | on | emits | outbound | approval |
|---|---|---|---|---|---|---|
| `catalog` | `schedule: weekly@mon09:00` | `loops/catalog.sh` | — | `channel.pending` | false | false |
| `draft_submission` | event | `loops/draft_submission.sh` | `[channel.pending]` | `proposal.created` | **true** | **true** |
| `dispatch` | `schedule: every 15m` | `loops/dispatch.sh` | — | `action.approved` | false | false |
| `execute` | event | `loops/execute.sh` | `[action.approved]` | `action.executed` | **true** | false |
| `verify` | `schedule: daily@20:00` | `loops/verify.sh` | `[action.executed]` | `channel.verified` | false | false |

- `catalog.sh` — reads `data/channels.json` (name, kind, url, how-to), diffs against state `submitted`, emits one `channel.pending` per remaining channel (max N/week to stay human-reviewable).
- `draft_submission.sh` — renders channel-specific copy (title/tagline/description/tags) with devin from the product README, emits `awesome_list_pr` or `directory_submission` proposals.
- `verify.sh` — curls the channel URL / `gh pr view` to confirm the listing is live; appends to `submitted` or re-queues.

**handlers/**: `awesome_list_pr.sh` (fork + branch + edit README + `gh pr create` with the standard PR body format), `directory_submission.sh` (form POST or "manual: open this URL" instruction record), `issue_comment.sh` (for lists that accept issues instead of PRs).

**Event graph:**
```
catalog --channel.pending--> draft_submission --proposal.created--> (queue)
dispatch --action.approved--> execute --action.executed--> verify --channel.verified--> (state: submitted)
```

**HITL:** PRs to third-party awesome-lists are public and permanent; maintainers close low-effort entries and remember the author. Human reviews the one-line description and the alphabetical placement before the PR opens.

**First target:** `sindresorhus/awesome-nodejs`-adjacent scraping lists — start with `lorien/awesome-web-scraping` (add grepapi under "Tools"), then `awesome-selfhosted`.

---

## 4. `changelog-to-social` — ship → post, on every real commit

**Repo:** `javimosch/grepapi` (reusable across all micro-SaaS repos)
**Problem:** grepapi ships weekly but the social channels are silent; manual posting never happens. This fleet converts merged work into platform-native posts, with a human as the editor rather than the writer.

| Loop | Mode / schedule | Command | on | emits | outbound | approval |
|---|---|---|---|---|---|---|
| `release_watch` | `schedule: every 30m` | `loops/release_watch.sh` | — | `release.detected` | false | false |
| `draft_posts` | event | `loops/draft_posts.sh` | `[release.detected]` | `proposal.created` | **true** | **true** |
| `dispatch` | `schedule: every 15m` | `loops/dispatch.sh` | — | `action.approved` | false | false |
| `execute` | event | `loops/execute.sh` | `[action.approved]` | `action.executed` | **true** | false |
| `metrics` | `schedule: daily@21:00` | `loops/metrics.sh` | `[action.executed]` | `metrics.collected` | false | false |

- `release_watch.sh` — `git log` / `gh release list` since state `last_sha`; ignores chore/docs-only diffs; emits `release.detected` with the changed-feature summary.
- `draft_posts.sh` — one devin call producing a compact JSONL with 3–4 proposals: `linkedin_post` (long, technical), `tweet` (hook + link), `devto_post` (only for substantial features), `mastodon_post`.
- `metrics.sh` — pulls impressions/likes per posted item into state `post_stats` so `draft_posts` can be told which hooks worked.

**handlers/**: `linkedin_post.sh`, `tweet.sh`, `devto_post.sh`, `mastodon_post.sh` — all thin wrappers over the existing minipostiz-cli publish path, reading credentials from the environment, never echoing them.

**Event graph:**
```
release_watch --release.detected--> draft_posts --proposal.created--> (queue)
dispatch --action.approved--> execute --action.executed--> metrics --metrics.collected--> (state: post_stats)
```

**HITL:** posts go out under a personal identity across 4 platforms — one bad LLM-flavored post costs more than a week of silence. Approval is per-platform, so LinkedIn can be approved while the tweet is rejected.

**First data source:** the grepapi repo's own git log + `docs/` changelog; first post = the headless Reddit fetcher, framed as "Reddit API pricing workaround" rather than a feature note.

---

## 5. `grepapi-icp-outreach` — LinkedIn ICP discovery, dogfooding grepapi

**Repo:** `javimosch/grepapi`
**Problem:** grepapi's buyers are the people building lead-gen/recruiting tooling — findable only via LinkedIn search, which is exactly what grepapi does. This fleet is both a growth engine and the loudest possible dogfood demo.

| Loop | Mode / schedule | Command | on | emits | outbound | approval |
|---|---|---|---|---|---|---|
| `li_search` | `schedule: daily@09:00` | `loops/li_search.sh` | — | `lead.found` | false | false |
| `enrich` | event | `loops/enrich.sh` | `[lead.found]` | `lead.qualified` | false | false |
| `outreach_agent` | `mode: daemon`, `runtime_max: 1h` | `loops/outreach_agent.sh` | `[lead.qualified]` | `agent.suggested` | **true** | **true** |
| `dispatch` | `schedule: every 15m` | `loops/dispatch.sh` | — | `action.approved` | false | false |
| `execute` | event | `loops/execute.sh` | `[action.approved]` | `action.executed` | **true** | false |

- `li_search.sh` — runs the grepapi LinkedIn fetcher through the invisible authed Edge session on rbm21, one saved search per day, appends new profiles to state `leads`; hard-caps results per run to stay gentle on the session.
- `enrich.sh` — jq + optional `bin/gh` lookup to attach a GitHub handle / repo signal; drops profiles without a build signal and anything already in `engaged`.
- `outreach_agent.sh` — daemon, 4 cycles: pick the top qualified lead, draft a short personalized comment-on-their-post **or** connection note referencing a specific thing they built; emits `linkedin_comment` / `linkedin_dm` proposals via `jq -c`.

**handlers/**: `linkedin_comment.sh`, `linkedin_dm.sh` (both drive the rbm21 authed browser session; enforce a per-day send cap and abort if the session is unauthenticated rather than retrying), `linkedin_post.sh` (reused from fleet 4 for the "how I built this with grepapi" post).

**Event graph:**
```
li_search --lead.found--> enrich --lead.qualified--> outreach_agent (daemon)
dispatch --action.approved--> execute --action.executed--> (state: engaged)
```

**HITL:** LinkedIn DMs are the highest-consequence channel — account restriction is permanent and there's no appeal. Every message is human-approved, and `execute` enforces a daily cap even for approved proposals; nothing sends without `FLEET_LIVE=1`.

**First target:** LinkedIn search `"lead generation" AND ("scraping" OR "automation") founder` filtered to people who posted in the last 14 days — i.e. exactly the query grepapi is demoed with.

---

### Rollout order

Start with **2** (`github-issue-growth`) — it's a near-copy of `machin-growth`, so it validates the plumbing (`HOME` in systemd units, compact JSONL, handler target formats) with the lowest-risk channel. Then **1**, **4**, **3**, and only last **5**, since LinkedIn is the least forgiving surface. For each: `fleet validate <name>` → `fleet run <name> <loop> --dry-run --no-chain` → `fleet install <name> --system` → `fleet queue` / `fleet approve` → `FLEET_LIVE=1` on `execute`.
