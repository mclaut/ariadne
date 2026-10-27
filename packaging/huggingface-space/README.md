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
short_description: Local-first memory with auditable attribution.
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

Version 0.8.17 keeps every access request visible throughout its lifecycle.

- **Complete queue and history:** pending, approved, denied, expired, and consumed statuses with controls bound to the exact request ID.
- **Fair prompts:** each system dialog yields after 30 seconds while unanswered requests remain pending.
- **Explicit continuation:** after approval, clients retry with `approval_id`; one-time credential grants reject reuse.
- **Read-only CLI history:** `ariadnectl approvals --all` displays all retained requests without granting access.

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
