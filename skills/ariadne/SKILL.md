---
name: ariadne
description: Long-term memory for Claude Code backed by the Ariadne MCP server (Qdrant + bge-m3, local). Use when the user asks to remember/recall something across sessions, mentions past decisions ("what did we decide about X"), asks to save a fact/decision permanently, or when Ariadne itself needs ops work (status, backup, restore, export, troubleshooting).
---

# Ariadne — long-term memory

Ariadne is a local, multilingual, hybrid-search memory server (MCP). If it is
registered, you have two tools: `mcp__ariadne__memory_recall` and
`mcp__ariadne__memory_save`. The runtime lives in `~/.ariadne/` (binaries,
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

## Save — what and what NOT

Save (verbatim, self-contained one-paragraph facts):
- **Decisions with their why** ("chose X over Y because Z").
- **Gotchas / hard-won lessons** (root cause + fix, not just the symptom).
- **Durable project facts** (architecture, endpoints, constraints, owners).

Do NOT save: raw transcripts, code dumps, anything derivable from the repo,
secrets/tokens/passwords (NEVER — memories are stored in plaintext), or
ephemeral session chatter. Identical text deduplicates automatically
(content-hash ids), so re-saving is harmless.

Metadata: `wing` = project slug (e.g. `myapp`, `backend`),
`room` = category (`decisions`, `gotchas`, `reference`, `diary`).

## Ops (via ~/.ariadne/bin/ariadnectl)

```bash
~/.ariadne/bin/ariadnectl status        # health: Qdrant, Ollama, points, disk
~/.ariadne/bin/ariadnectl start|stop|restart
~/.ariadne/bin/ariadnectl backup        # Qdrant snapshot → ~/.ariadne/backups (keeps 10)
~/.ariadne/bin/ariadnectl restore <f>   # DESTRUCTIVE: replace collection from snapshot
~/.ariadne/bin/ariadnectl export [f]    # portable JSONL (no vectors, re-embeddable)
```

Backup vs export: **backup** = fast 1:1 snapshot tied to the embedding model;
**export** = portable text that any future model can re-embed. Before risky
operations (restore, migration, bulk import) run a backup first.

## Session hooks (if installed)

- **SessionStart auto-recall**: project memories may already be injected at
  session start (marked "🧵 Ariadne auto-recall") — treat them as background
  context and recall deeper with the MCP tool when needed.
- **SessionEnd auto-capture**: a local model summarizes each session into ONE
  `diary` memory. Don't duplicate it by saving your own session summary;
  DO still save important decisions/gotchas explicitly (better wording,
  right room). Capture log: `~/.ariadne/logs/capture.log`;
  disable with `ARIADNE_CAPTURE=0`.

## Troubleshooting

1. Run `tools/doctor.sh` — it checks the whole chain (binaries → services →
   model → collection → MCP registration) and prints what's broken.
2. Common fixes:
   - Qdrant down → `~/.ariadne/bin/ariadnectl start` (LaunchAgent `com.ariadne.qdrant`).
   - Ollama down → `brew services start ollama`; model missing → `ollama pull bge-m3`.
   - Tools absent in Claude Code → check `mcpServers.ariadne` in `~/.claude.json`
     points to `~/.ariadne/bin/ariadne`, then restart the session.
3. macOS gotcha: launchd agents CANNOT exec binaries (or write logs) under
   `~/Desktop`/`~/Documents` (TCC) — they die with `EX_CONFIG` and empty logs.
   Everything must run from `~/.ariadne`. Don't "fix" the agent by pointing it
   back into the repo.
4. `tools/recall.sh "query"` — CLI recall (dense-only) without MCP, for quick
   checks and hooks.
