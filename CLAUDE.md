# CLAUDE.md — Ariadne

## What this is

Ariadne is a **local-first, multilingual memory server** for Claude Code (and any
MCP client): Go + Qdrant + bge-m3, hybrid dense+sparse search. It replaces
embedded vector-DB memory backends that crash or lock under several concurrent
sessions — Qdrant is a server, so the single-writer/lock-starvation class is gone.
**Public repo.**

## Build / test / lint

- Build everything: `go build ./...`
- Install a runtime binary: `go build -o ~/.ariadne/bin/<name> ./cmd/<name>`
  (`ariadne`, `ariadnectl`, `import`, `ariadne-hook`, `install`)
- Lint (must be clean before every commit): `golangci-lint run` — config `.golangci.yml`
- Format: `golangci-lint fmt`
- The `poc/` experiments are a separate module: `cd poc && go build .`

## Architecture

- `internal/store` — storage core: bge-m3 dense embedding (Ollama) + BM25 sparse
  (pure Go tokenizer; Qdrant computes IDF) fused with RRF server-side. No MCP concerns.
- `cmd/ariadne` — MCP server (stdio). Tools: `memory_save`, `memory_recall`,
  `memory_delete`, `memory_move` (curate: delete by id, re-home/re-tag by id).
- `cmd/ariadnectl` — control/health core; `cmd/ariadne-tray` (localized system-tray
  monitor, macOS/Linux/Windows) is a thin viewer over it.
- `cmd/import` — backfill (chromadb sqlite / markdown memfiles / JSONL); `-sync`
  keeps memfile chunks true to disk.
- `cmd/hook` (`ariadne-hook`) — Claude Code session hooks: SessionStart auto-recall,
  SessionEnd curated auto-capture (local chat model → one diary memory, never raw).
- `cmd/install` — one-shot installer (macOS/Linux); reuses an existing Qdrant,
  never restarts/reconfigures it.

## Runtime layout (NOT the repo)

Binaries, Qdrant data, backups and logs live in `~/.ariadne/` — never in the repo,
never under `~/Desktop`/`~/Documents`. On macOS, launchd cannot exec programs or
write logs from those folders (TCC → agents die with `EX_CONFIG`, empty logs).
The repo holds only source.

## Conventions

- **Public repo — zero personal references.** No usernames, private paths, other
  projects, IPs, or internal systems — not even as examples, defaults, or comments.
  Grep the working tree **and git history** before every push.
- **Qdrant is loopback-only** (`QDRANT__SERVICE__HOST=127.0.0.1`) — it has no auth
  by default and memories are stored as plaintext payloads. Never expose it.
- `golangci-lint run` clean before every commit; justify any `//nolint` inline.
- Surgical changes, match existing style, keep it simple (no speculative abstraction).
