---
name: ariadne
description: Long-term memory for Codex, Claude Code, and MCP clients backed by the local Ariadne server (Qdrant + bge-m3). Use proactively during substantive project work: recall past context and immediately save durable decisions, gotchas, completed reports, release/deployment results, verified status, and critical reference facts without waiting for session end or an explicit remember command. Also use when the user asks about earlier work, requests permanent memory, or needs Ariadne operations.
---

# Ariadne — long-term memory

Ariadne is a local, multilingual, hybrid-search memory server (MCP). If it is
registered, you have four tools: `mcp__ariadne__memory_recall`,
`mcp__ariadne__memory_save`, `mcp__ariadne__memory_delete` and
`mcp__ariadne__memory_move`. The runtime lives in `~/.ariadne/` (binaries,
Qdrant data, backups, logs); source lives in the repo.

## Recall — when and how

- **Start of substantive work in a project**: recall the project's context once
  (`query: "<project name> current state decisions"`). Don't recall for trivial
  one-liners.
- **The user references the past**: "what did we decide", "why did we choose",
  "як ми робили" → recall BEFORE answering from your own guesses.
- Queries are multilingual — query in ANY language, memories in any language
  will match (bge-m3 is cross-lingual; scores ≥0.6 are usually relevant).
- Prefer 2–3 focused recalls over one vague one. `limit` default 5 is fine.
- When an exact memory id is known, call `memory_recall` with `id` instead of a
  semantic `query`. ID lookup is exact, skips embedding, and is the preferred
  way to verify a memory before moving or deleting it. Use `collection` too if
  the id belongs to the separate `sessions` archive.
- Use `room` to narrow retrieval when the category is known: `decisions`,
  `gotchas`, `reference`, or `diary`. For release/deployment/status reports,
  search `room: "reference"` first, then broaden only if needed.
- The raw session archive lives in a separate collection — pass
  `collection: "sessions"` to dig into old transcripts; normal recall never
  sees them.

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
ephemeral session chatter. Identical text deduplicates automatically
(content-hash ids), so re-saving is harmless.

Metadata: `wing` = stable project slug (e.g. `myapp`, `backend`),
`room` = category (`decisions`, `gotchas`, `reference`, `diary`). Use
`reference` for reports and verified outcomes; `diary` is temporary chronology.

## Curate — delete / move (by id)

`memory_recall` returns each hit's `id`; retrieve it later with
`memory_recall(id: "...")`. Use exact ID lookup to verify a memory before
curation, not only to find it semantically.

- **`memory_delete(id)`** — remove ONE memory. Irreversible. Only for something the
  user asked to forget, or a memory that is clearly wrong or superseded. Recall
  first, confirm the id is the right one, and say what you're removing.
- **`memory_move(id, wing?, room?)`** — re-home (change project) or re-tag (change
  room) a memory without touching its text; the id stays the same. Use it when a
  memory landed in the wrong wing/room.

There is no copy tool: ids are a content-hash of the text, so identical text is
always exactly one point.

## Ops (via ~/.ariadne/bin/ariadnectl)

```bash
~/.ariadne/bin/ariadnectl status        # health: Qdrant, Ollama, points, disk
~/.ariadne/bin/ariadnectl metrics       # estimated tokens saved by recalls (net avoided)
~/.ariadne/bin/ariadnectl start|stop|restart
~/.ariadne/bin/ariadnectl backup        # Qdrant snapshot → ~/.ariadne/backups (keeps 10)
~/.ariadne/bin/ariadnectl restore <f>   # DESTRUCTIVE: replace collection from snapshot
~/.ariadne/bin/ariadnectl export [f]    # portable JSONL (no vectors, re-embeddable)
~/.ariadne/bin/ariadnectl consolidate --before 24h  # merge old diaries → durable memories
```

`metrics` reports three honest values: **confirmed saved** (sum of positive
per-recall reuse), **recall overhead** (delivery not backed by measurable source
context, including legacy memories), and signed **net** (saved minus overhead).
The tray shows confirmed savings, which cannot be negative; CLI/JSON preserve
overhead and signed net instead of hiding real costs.

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
  right room). Capture log: `~/.ariadne/logs/capture.log`;
  disable with `ARIADNE_CAPTURE=0`. Capture summaries use
  `ARIADNE_SUMMARY_OLLAMA` (default: local Ollama); remote summary endpoints are
  blocked unless `ARIADNE_CAPTURE_REMOTE=1` is set, because condensed transcript
  text is sent there.

## Troubleshooting

1. Run `tools/doctor.sh` — it checks the whole chain (binaries → services →
   model → collection → MCP registration) and prints what's broken.
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
