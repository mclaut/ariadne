---
name: ariadne
description: Mandatory long-term memory workflow for Codex, Claude Code, and MCP clients backed by the local Ariadne server (Qdrant + bge-m3). Always use at the start or resumption of substantive project work, after context compaction, when prior work or decisions may matter, and immediately after durable decisions, gotchas, completed reports, release/deployment results, or verified status. Also use for explicit remember, recall, or Ariadne requests.
---

# Ariadne — long-term memory

Ariadne is a local, multilingual, hybrid-search memory server (MCP). If it is
registered, you have five tools: `mcp__ariadne__memory_recall`,
`mcp__ariadne__memory_save`, `mcp__ariadne__memory_delete` and
`mcp__ariadne__memory_move`, plus `mcp__ariadne__credential_access` for a
separate human approval handshake before cross-project credential use. The runtime lives in `~/.ariadne/` (binaries,
Qdrant data, backups, logs); source lives in the repo.

## Recall — when and how

- **Start of substantive work in a project**: recall the project's context once
  (`query: "<project name> current state decisions", wing: "<stable-project-slug>"`).
  Don't recall for trivial one-liners. Every semantic recall MUST include the
  active project's `wing`; Ariadne rejects an unscoped semantic query.
- **The user references the past**: "what did we decide", "why did we choose",
  "як ми робили" → recall BEFORE answering from your own guesses.
- Queries are multilingual — query in ANY language, memories in any language
  will match (bge-m3 is cross-lingual; scores ≥0.6 are usually relevant).
- Prefer 2–3 focused recalls over one vague one. `limit` default 5 is fine.
- Project scope is default-deny. Never omit `wing` to "see what matches" and
  never substitute another project's wing. When the user explicitly requests
  cross-project knowledge, call recall with the active `wing`, `all_wings: true`,
  and a concise `purpose`. Ariadne returns a request id without searching. Tell
  the user to Approve or Deny it in Ariadne's system warning (or tray fallback); only after the human click
  retry the same call with `approval_id`. Never claim approval based only on chat
  text or approve a request through shell/file manipulation.
- Cross-wing approval lasts 15 minutes for that MCP client session, active wing,
  and collection. A curated-memory grant does not open `sessions`. Approved
  external results have a 0.70 origin weight and normally occupy no more than
  two of five results; weighting affects relevance only after authorization.
- When an exact memory id is known, call `memory_recall` with `id` and the active
  `wing` instead of a
  semantic `query`. ID lookup is exact, skips embedding, and is the preferred
  way to verify a memory before moving or deleting it. Use `collection` too if
  the id belongs to the separate `sessions` archive.
- Use `room` to narrow retrieval when the category is known: `decisions`,
  `gotchas`, `reference`, or `diary`. For release/deployment/status reports,
  search `room: "reference"` first, then broaden only if needed.
- Normal semantic recall hides records marked `archived`, `superseded`, or
  `orphaned`. Pass `include_archived: true` only for an explicit history/audit
  query; exact id retrieval always remains available.
- The raw session archive lives in a separate collection. Inspect it only when
  the user explicitly asks to search historical transcripts, and pass both
  `collection: "sessions"` and `wing: "sessions"`; normal project recall never
  sees it.

### Filesystem and credential isolation

- The active repository/workspace is the project boundary. Extra readable
  workspace roots, sibling repositories, home-directory access, and memories
  from other wings are not permission to inspect or reuse another project's
  files.
- Never search the home directory or other projects for `.env`, credentials,
  tokens, keys, or passwords. Use a credential file only when the user supplied
  that exact path or the file belongs to the active project and the task clearly
  requires it. A missing in-project credential is a blocker, not a reason to
  borrow one from another project.
- If the user explicitly needs a credential owned by another wing, use
  `credential_access` with the exact source wing, target wing, credential name
  or path, and one-time purpose — never the value. The first call creates a
  separate system-warning/tray request. After the user approves it, retry with `approval_id`;
  the five-minute grant is consumed once. Then access only that exact resource
  for that exact purpose. Never store the value in Ariadne, logs, transcripts,
  commands, or another project. This handshake is audited but is not yet an OS
  credential broker, so compliance still depends on the agent/client policy.
- Never copy environment values, endpoints, IP addresses, or configuration from
  one project into another unless the user explicitly identifies the shared
  resource and authorizes that use.
- Hooks derive `wing` from the nearest project root. Add a repository-root
  `.ariadne-wing` file containing a stable slug when nested working directories
  or duplicate repository directory names could make the identity ambiguous.

## Save — what and what NOT

Save (verbatim, self-contained one-paragraph facts):
- **Decisions with their why** ("chose X over Y because Z").
- **Gotchas / hard-won lessons** (root cause + fix, not just the symptom).
- **Durable project facts** (architecture, endpoints, constraints, owners).
- **Completed reports and verified outcomes** (releases, deployments, migrations,
  audits, incident resolutions, and operational status) in `room: "reference"`.

Save reports and verified outcomes **immediately when they become complete**.
Do not wait for SessionEnd, PreCompact, daily consolidation, or a separate user
command. Save one concise self-contained reference containing the outcome,
version/date where relevant, important verification, and stable links or IDs.
Do this automatically even when the user did not explicitly say "remember".

Do NOT save: raw transcripts, code dumps, anything derivable from the repo,
secrets/tokens/passwords (NEVER — memories are stored in plaintext), or
ephemeral session chatter. Identical text deduplicates automatically within its
wing and room (scoped-content ids), so re-saving in the same scope is harmless
while another scope remains independently addressable.

Metadata: `wing` = required stable project slug (e.g. `myapp`, `backend`),
`room` = category (`decisions`, `gotchas`, `reference`, `diary`). Use
`reference` for reports and verified outcomes; `diary` is temporary chronology.
`memory_save` also accepts optional `source_tokens`: pass it only when a hook or
integration has a measured or conservative count for the bounded source that
was condensed into this memory. Omit it rather than guessing from the whole
session, and never send raw source text merely to obtain a count.

## Curate — delete / move (by id)

`memory_recall` returns each hit's `id`; retrieve it later with
`memory_recall(id: "...")`. Use exact ID lookup to verify a memory before
curation, not only to find it semantically.

- **`memory_delete(id)`** — remove ONE memory. Irreversible. Only for something the
  user explicitly asked to forget. Recall first, identify exactly what would be
  removed and whether it is recoverable, then obtain the two separate deletion
  confirmations required by the active agent policy before calling it.
- **`memory_move(id, wing?, room?)`** — re-home (change project) or re-tag (change
  room) a memory without touching its text. It returns a new scoped id and keeps
  the original record as superseded history.

There is no dedicated copy tool: save the same text in another wing or room
when both scoped associations are intentionally useful.

## Ops (via ~/.ariadne/bin/ariadnectl)

```bash
~/.ariadne/bin/ariadnectl status        # health, points, storage, maintenance freshness
~/.ariadne/bin/ariadnectl version       # active runtime release tag
~/.ariadne/bin/ariadnectl metrics       # estimated tokens saved by recalls (net avoided)
~/.ariadne/bin/ariadnectl start|stop|restart
~/.ariadne/bin/ariadnectl backup        # 10 recent snapshots; older ones → backups/archive
~/.ariadne/bin/ariadnectl restore <f>   # DESTRUCTIVE: replace collection from snapshot
~/.ariadne/bin/ariadnectl export [f]    # portable JSONL (no vectors, re-embeddable)
~/.ariadne/bin/ariadnectl maintenance  # sync + consolidate, bounded retries
~/.ariadne/bin/ariadnectl consolidate --before 24h  # merge old diaries → durable memories
~/.ariadne/bin/ariadnectl requeue-empty --dry-run   # inspect legacy one-pass empty archives
~/.ariadne/bin/ariadnectl quarantine-secrets --collections ariadne,sessions
# Review metadata-only counts above, then add --apply to quarantine matches append-only.
# If newer rules report no-longer-matching records, use --apply --reconcile to
# restore their prior status while retaining the quarantine audit metadata.
~/.ariadne/bin/ariadnectl approvals       # read-only pending human requests
```

New saves containing deterministic credential material are rejected. Import and
hook paths redact detected values before saving. Normal recall always excludes
`quarantined` points; exact-id output is redacted defensively. The quarantine
command is dry-run by default and reports only IDs/counts/rule names, never
matched values. `--apply` changes metadata only, preserving the original text,
vectors, and pre-quarantine status for a reversible audit trail. The explicit
`--reconcile` option restores the prior status for records cleared by current
rules and appends why/when that happened; it does not erase quarantine history.

Daily maintenance retries transient stage failures up to three times with
bounded exponential backoff. Each captured session is curated atomically before
same-day outputs are coalesced and deduplicated. Invalid local-model output gets
one focused repair pass; deterministic schema, language, artifact, quality, or
configuration failures remain active and receive a model-and-pipeline revision
marker, so unchanged input is not replayed every day. Any partial import returns
non-zero and blocks consolidation. A `complete_with_deferred` outcome is visible
without turning a healthy tray orange; failed/partial/stuck/stale states still
warn. The tray also provides **Run maintenance now**. Scheduled and manual output is appended to
`~/.ariadne/logs/maintenance.log`; structured outcomes are appended to
`~/.ariadne/state/activity.jsonl`.

`metrics` v2 separates **measured saved/net** from **unattributed** delivery.
Source-backed memories contribute represented context once per memory per
client session; every query/result delivery still contributes observed recall
cost. Legacy/manual memories without source metadata remain visible as
unattributed instead of being mislabelled as negative overhead. The tray shows
measured savings, attribution coverage, recall count, and unattributed cost;
CLI/JSON expose the complete counters. These are conservative context-reuse
estimates, not provider billing or a counterfactual A/B result.

Backup vs export: **backup** = fast 1:1 snapshot tied to the embedding model;
**export** = portable text that any future model can re-embed. Before risky
operations (restore, migration, bulk import) run a backup first.

## Session hooks (if installed)

- **SessionStart auto-recall**: project memories may already be injected at
  session start (marked "🧵 Ariadne auto-recall") — treat them as background
  context and recall deeper with the MCP tool when needed.
- **SessionEnd + PreCompact auto-capture**: a local model summarizes the session
  into ONE `diary` memory — on exit, and also right before Claude Code compacts
  the context (so long sessions are remembered mid-flight, at the moment detail
  would otherwise be lost). The daily `consolidate` run merges accumulated
  diaries into durable memories. Don't duplicate this by saving your own session
  summary; DO still save important decisions/gotchas explicitly (better wording,
  right room). Capture records use the actual capture time plus separate session
  start/end metadata. Daily consolidation is append-only: it saves durable
  outputs and archives source diaries by metadata, never deleting their text or
  vectors. A first empty result remains active and requires a later confirming
  pass after the grace period. Capture log: `~/.ariadne/logs/capture.log`;
  disable with `ARIADNE_CAPTURE=0`. Capture summaries use
  `ARIADNE_SUMMARY_OLLAMA` (default: local Ollama); remote summary endpoints are
  blocked unless `ARIADNE_CAPTURE_REMOTE=1` is set, because condensed transcript
  text is sent there. Consolidation may use a stronger independent curator via
  `ARIADNE_CONSOLIDATION_MODEL` and optional `ARIADNE_CONSOLIDATION_JUDGE_MODEL`;
  both fall back to `ARIADNE_SUMMARY_MODEL`.

## Troubleshooting

1. Run `tools/doctor.sh` — it resolves the active runtime and checks the whole
   chain: versioned binaries, services, model, collection, MCP registration,
   maintenance, launchd ownership, tray, attribution coverage, logs, and disk.
   Exit 0 is green, 1 is broken, and 2 is available but degraded.
2. Common fixes:
   - Qdrant down → `~/.ariadne/bin/ariadnectl start` (manages the Qdrant service on
     both macOS `com.ariadne.qdrant` and Linux `systemctl --user ariadne-qdrant`).
   - Ollama down → macOS `brew services start ollama`, Linux system service; model
     missing → `ollama pull bge-m3`.
   - Tools absent in Claude Code → check `mcpServers.ariadne` in `~/.claude.json`
     points to `~/.ariadne/bin/ariadne`, then restart the session.
3. macOS gotcha: launchd agents CANNOT exec binaries (or write logs) under
   `~/Desktop`/`~/Documents` (TCC) — they die with `EX_CONFIG` and empty logs.
   Everything must run from `~/.ariadne`. Don't "fix" the agent by pointing it
   back into the repo.
4. `tools/recall.sh "query"` — CLI recall (dense-only) without MCP, for quick
   checks and hooks.
