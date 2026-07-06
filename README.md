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
| `cmd/ariadne-tray` | Cross-platform tray monitor (macOS/Linux/Windows) — pure-Go, localized, over the `ariadnectl` core. |
| `skills/ariadne` | Claude Code skill: recall/save discipline + `doctor.sh`/`recall.sh`. |
| `deploy/` | LaunchAgent / systemd templates: Qdrant service, daily memfiles-sync, tray autostart. |
| `poc/` | Standalone experiments that validated the stack. |

## Setup

### One command (Linux + macOS)

```bash
curl -fsSL https://raw.githubusercontent.com/mclaut/ariadne/main/install.sh | sh
```

Nothing by hand: `install.sh` bootstraps Go and the source (GitHub tarball — no
git needed), then runs the installer below, which auto-installs Ollama, Qdrant,
the models, the services, and — on Linux — the tray plus its desktop deps. Pass
installer flags straight through, e.g. a lighter summary model or a preview:

```bash
curl -fsSL https://raw.githubusercontent.com/mclaut/ariadne/main/install.sh | sh -s -- -summary-model qwen2.5:3b
curl -fsSL https://raw.githubusercontent.com/mclaut/ariadne/main/install.sh | sh -s -- -dry-run
```

(sudo — for apt packages and Ollama on Linux — prompts on the terminal, so the
pipe is fine. Windows: PowerShell path is on the roadmap.)

### From a clone (or to hack on it)

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
`ARIADNE_SUMMARY_MODEL` [qwen2.5:7b], `ARIADNE_CAPTURE_MIN_TURNS` [3]. The
summary model is loaded only for capture and unloaded right after
(`keep_alive:0`), so it costs RAM only briefly; for a smaller footprint set
`ARIADNE_SUMMARY_MODEL=qwen2.5:3b` (~2GB vs ~4.7GB, at some summary quality) —
or pass `-summary-model` to the installer so it pulls that one.

## Monitor

A tray/menu-bar monitor polls `ariadnectl status -json` every 5s and shows a
green/orange/red/grey icon, per-service detail, and start/stop/restart/backup
actions; it notifies when a service drops. The `ariadne-tray` UI is localized —
**7 languages** (English, Українська, Deutsch, Italiano, Español, Français,
Polski) with a live **🌐 Language** switcher that shows the active one at a
glance. The choice persists in `~/.ariadne/lang` and `ariadnectl` follows it, so
the whole interface stays in one language. Adding a language is one block in
`internal/i18n`.

- **`ariadne-tray`** (pure-Go, `fyne.io/systray`) is the monitor on every
  platform: the installer builds it into `~/.ariadne/bin` and registers autostart
  — a `~/.config/autostart` entry on Linux, a `com.ariadne.tray` LaunchAgent on
  macOS (migrating off any older Swift monitor so you get one icon). On Linux it
  needs a StatusNotifierItem host (native on KDE/XFCE; on GNOME install the
  "AppIndicator and KStatusNotifierItem" extension). Cross-compiles for Windows.

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
