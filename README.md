# ariadne

A **native, local-first, multilingual memory server** for [Claude Code](https://claude.com/claude-code)
(and any MCP client). Go + [Qdrant](https://qdrant.tech) + [bge-m3](https://huggingface.co/BAAI/bge-m3) — no Docker, no cloud, no API keys.

Built as a replacement for embedded vector-DB memory backends that crash or
starve under several concurrent Claude sessions. ariadne is a **server**: one
Qdrant handles concurrent writes natively, so the whole single-writer /
lock-starvation class simply doesn't exist.

## Why

- **Stable** — Qdrant server, not an embedded HNSW that SIGSEGVs on compaction.
- **Multilingual** — bge-m3 covers 100+ languages; cross-lingual recall works
  (an English query finds Ukrainian notes, cosine ~0.8–0.94 across uk/ru/en/es/
  de/it/pl/ro/hu/lt/lv/et/fi/fr/ar).
- **Hybrid search** — dense (bge-m3) + BM25 sparse (pure Go tokenizer; Qdrant
  computes IDF server-side) fused with RRF. Exact terms/codes/names rank sharply.
- **Native** — Qdrant binary + Ollama (Metal), everything runs on the laptop.

## Components

| Path | What |
|---|---|
| `cmd/ariadne` | MCP server (stdio). Tools: `memory_save`, `memory_recall`. |
| `cmd/import` | Backfill from a chromadb sqlite, markdown memory files or JSONL (batched embeds). |
| `cmd/hook` | Claude Code session hooks (`ariadne-hook`): SessionStart auto-recall, SessionEnd auto-capture. |
| `cmd/install` | One-shot installer (macOS/Linux): preflight, reuse-or-install Qdrant, services, MCP, skill, hooks. |
| `cmd/ariadnectl` | Control + health core (`status -json`, start/stop, backup/export). |
| `internal/store` | Storage core: embed (Ollama), BM25 sparse, Qdrant hybrid. |
| `app/` | Swift menu-bar monitor (status, start/stop, alerts). |
| `skills/ariadne` | Claude Code skill: recall/save discipline + `doctor.sh`/`recall.sh`. |
| `deploy/` | LaunchAgent / systemd templates: Qdrant service + daily memfiles-sync. |
| `poc/` | Standalone experiments that validated the stack. |

## Setup

### One-shot installer (macOS + Linux)

```bash
go run ./cmd/install -dry-run   # preflight + plan, changes nothing
go run ./cmd/install -yes       # do it
```

The installer is deliberately careful with existing infrastructure:

- **An already-running Qdrant is REUSED, never restarted or reconfigured** —
  Ariadne only adds its own collection. A busy port that is *not* Qdrant aborts
  the install. Use `-qdrant-host/-qdrant-rest/-qdrant-grpc` to point at a
  remote instance (e.g. a GPU workstation), `-ollama` for a remote embedder.
- **GPU / RAM / disk are checked up front** and insufficiencies are stated
  plainly (no GPU → an honest "embeddings on CPU, ~10x slower" warning;
  <6GiB RAM or <5GiB disk → hard FAIL).
- Idempotent: re-running skips everything that is already in place.

It installs the Qdrant binary + service (macOS LaunchAgent / Linux systemd
user unit, loopback-only), builds the Go binaries into `~/.ariadne/bin`,
pulls `bge-m3`, creates the collection, registers the MCP server in
`~/.claude.json` (backup kept) and installs the Claude Code skill.

### Manual setup

The **runtime lives in `~/.ariadne`** (binaries, data, backups, logs) — the repo
holds only source. On macOS this is not just taste: launchd agents **cannot**
exec programs or write logs under `~/Desktop`/`~/Documents` (TCC) — they die
with `EX_CONFIG` and empty logs. Keep the runtime out of those folders.

```bash
mkdir -p ~/.ariadne/{bin,backups,logs}

# 1. Qdrant (native binary — no Docker)
curl -sL https://github.com/qdrant/qdrant/releases/latest/download/qdrant-aarch64-apple-darwin.tar.gz \
  | tar xz -C ~/.ariadne/bin

# 2. Ollama + bge-m3 (native, Metal)
brew install ollama && brew services start ollama
ollama pull bge-m3

# 3. Build + install
go build -o ~/.ariadne/bin/ariadne    ./cmd/ariadne
go build -o ~/.ariadne/bin/ariadnectl ./cmd/ariadnectl
go build -o ~/.ariadne/bin/import     ./cmd/import

# 4. Qdrant as a service (see the TCC note above)
sed "s|__HOME__|$HOME|g" deploy/com.ariadne.qdrant.plist > ~/Library/LaunchAgents/com.ariadne.qdrant.plist
launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/com.ariadne.qdrant.plist

# 5. Register with Claude Code — add to ~/.claude.json mcpServers:
#   "ariadne": { "type": "stdio", "command": "<home>/.ariadne/bin/ariadne" }
```

**Security:** Qdrant has no auth by default — the template pins it to
`127.0.0.1` (`QDRANT__SERVICE__HOST`). Never expose it on the LAN: your
memories are stored in plaintext payloads.

Config via env (defaults in brackets): `ARIADNE_QDRANT_HOST` [localhost],
`ARIADNE_QDRANT_PORT` [6334], `ARIADNE_OLLAMA` [http://localhost:11434],
`ARIADNE_MODEL` [bge-m3], `ARIADNE_COLLECTION` [ariadne].

## Claude Code skill

`skills/ariadne/` teaches Claude Code when to recall, what (not) to save, and
how to operate the stack; `tools/doctor.sh` checks the whole chain
(binaries → services → model → collection → binding → MCP registration) and
`tools/recall.sh "query"` does CLI recall without MCP. Install (a real copy —
symlinked skills are not discovered at session start):

```bash
cp -R skills/ariadne ~/.claude/skills/ariadne
```

## Session hooks — auto-recall & auto-capture

The installer registers two Claude Code lifecycle hooks (`cmd/hook`, binary
`ariadne-hook`; skip with `-skip-hooks`):

- **SessionStart → auto-recall.** When a session starts in a project that HAS
  memories (wing = the directory name), the top hits are injected as context —
  Claude "remembers" the project before your first message. Projects without
  memories start completely clean; failures are silent and never block the
  session.
- **SessionEnd → auto-capture.** A detached worker (session exit is never
  blocked) parses the transcript, extracts deterministic facts (branch,
  commits, duration) and asks a **local Ollama chat model** to write a 4–8
  sentence summary — decisions with reasons, fixes, open items. ONE curated
  diary memory per session; raw transcripts are never stored. Trivial sessions
  are skipped (min-turns guard + the summarizer can answer `SKIP`).
  Log: `~/.ariadne/logs/capture.log`.

Config via env: `ARIADNE_CAPTURE=0` disables capture,
`ARIADNE_SUMMARY_MODEL` [qwen2.5:7b], `ARIADNE_CAPTURE_MIN_TURNS` [3].

## Backup & portability

Two distinct concepts:

- **Backup / restore** — a fast, full, native Qdrant snapshot (includes vectors;
  one-to-one restore, tied to the embedding model). For recovering after damage.
  ```bash
  ariadnectl backup            # snapshot → ./backups/, rotated (keeps 10)
  ariadnectl restore <file>    # recover the collection from a snapshot (destructive)
  ```
- **Export / import** — a portable JSONL of `{text, wing, room}` (no vectors, so
  it moves between machines and re-embeds with any model). For migration/inspection.
  ```bash
  ariadnectl export [file]                        # all memories → JSONL
  import -source jsonl -file export.jsonl           # re-embed + upsert an export
  ```

`import` also backfills from an archived chromadb sqlite
(`-source chroma -db <file>`) or markdown memory files (`-source memfiles`).
All imports are idempotent (content-hash ids). For memfiles, `-sync` keeps the
collection true to disk: edited files replace their old chunks and chunks of
deleted files are reaped. The installer registers a daily agent
(`com.ariadne.sync` / `ariadne-sync.timer`) that runs this for you; run it by
hand after large note edits.

## Status

v1 — working. Hybrid multilingual recall, session hooks (auto-recall + curated
auto-capture) and time-ordered diary are all live; several thousand memories in
daily use. Bulk import batches embeddings for a large backfill speedup. A
learned-sparse upgrade (bge-m3 SPLADE on a CUDA box) is optional if BM25 proves
too weak for morphology-rich languages.

## License

MIT
