---
title: Ariadne
emoji: 🧵
colorFrom: green
colorTo: blue
sdk: static
app_file: index.html
fullWidth: true
header: mini
pinned: true
license: mit
short_description: Local-first memory with verified backup rotation.
models:
  - BAAI/bge-m3
tags:
  - mcp
  - agents
  - memory
  - local-first
  - qdrant
  - ollama
  - codex
  - claude-code
  - multilingual
---

# Ariadne

Ariadne is a native, local-first, multilingual memory server for Codex,
Claude Code, and any MCP client. It combines Go, Qdrant, Ollama, and bge-m3
hybrid retrieval without Docker, cloud storage, or API keys.

The Space is a project showcase. Ariadne itself runs on the user's Windows,
macOS, or Linux machine, where memories remain local.

## Current release

Version 0.8.25 keeps one independently restored full recovery set when an explicit full-backup plan is configured.

- **One full set.** Runtime, client configuration and collection snapshots are included in the full set instead of accumulating separately.
- **Recovery before rotation.** Files, databases and configured models pass independent recovery before the previous full set expires.
- **Safe failures.** Failed recovery keeps the previous full. A stale full blocks runtime upgrades instead of silently creating another component copy.
- **Audit preserved.** Access history, manifests and restore logs remain outside rotation. Existing extra copies require an explicit cleanup plan.

## Links

- [GitHub repository](https://github.com/mclaut/ariadne)
- [Latest release](https://github.com/mclaut/ariadne/releases/latest)
- [Project documentation](https://mclaut.github.io/ariadne/)
- [Ukrainian documentation](https://mclaut.github.io/ariadne/uk/)
- [MCP Registry metadata](https://github.com/mclaut/ariadne/blob/main/server.json)

## Install

Linux and macOS:

```bash
curl -fsSL https://raw.githubusercontent.com/mclaut/ariadne/main/install.sh | sh
```

For a one-time upgrade from a tray still running v0.8.13 or older, use the
pinned v0.8.16 installer; it preserves Qdrant data, backups, settings, and logs:

```bash
curl -fsSL https://raw.githubusercontent.com/mclaut/ariadne/v0.8.16/install.sh | sh
```

After v0.8.16 is active, normal confirmed tray updates keep `Ariadne.app`, the
tray, and command-line binaries synchronized.

Windows PowerShell:

```powershell
irm https://raw.githubusercontent.com/mclaut/ariadne/main/install.ps1 -OutFile install.ps1
powershell -ExecutionPolicy Bypass -File .\install.ps1
```

MIT licensed. The source of this Space is maintained in
`packaging/huggingface-space` in the Ariadne repository.

## Full backup rotation in v0.8.23

An explicit full-backup plan keeps one independently restored full recovery set. Older full sets expire only after the replacement passes file, database and model recovery checks. Audit is preserved separately, outside rotation.

## Runtime rotation fixed in v0.8.24

Old release-local previous-runtime directories expire only after exact full-backup matching and independent recovery of the retained runtime. Audit metadata and retired-file inventories are preserved separately. Active, modified, newer or unknown copies stay protected.
