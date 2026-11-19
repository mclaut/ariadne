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

Version 0.8.30 adds the Ariadne panel and cleans up after interrupted backups.

- **Ariadne panel.** A local, token-protected page explains why the tray dot is yellow or red, with service state, recent maintenance, log tails and controls.
- **No leftover backups.** Failed or interrupted full backups no longer leave their ~15 GB stage behind.
- **Stronger curator.** Consolidation uses `qwen2.5:14b` when it is installed and no model is configured.

Version 0.8.29 kept Qdrant running through updates and drained the consolidation queue in one run.

- **Updates keep Qdrant running.** The installer waits for launchd to release a service before restarting it and retries the start.
- **The whole queue in one run.** Consolidation has no run-wide deadline; transient Ollama failures are retried in place and every group is saved as it finishes.

Version 0.8.28 fixed the update path and reboot-aware maintenance health.

- **Updates install again.** The Go bootstrap verifies its checksum from `dl.google.com`; v0.8.27 updates that failed with a checksum mismatch succeed now.
- **Reboots are not hangs.** Maintenance cut short by a restart is reported as interrupted and retried on schedule.

Version 0.8.27 was a security maintenance release.

- **Patched Go toolchain.** Built with Go 1.26.11, closing 24 reachable standard-library advisories in HTTP/2, HTTP/1, TLS, certificate validation and file-system root handling.
- **Updated dependencies.** `golang.org/x/net` v0.63.0 and `grpc` v1.85.0.
- **Vulnerability gate.** Every CI run and release runs `govulncheck`; a reachable vulnerability blocks the build.
- **Drop-in upgrade.** No data, configuration or behavior changes; memories, backups and settings are kept.

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
