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
short_description: Local-first memory with human-approved boundaries.
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

Version 0.8.6 fixes the launchd tray-restart race, propagates service-control
errors, and repairs stale Claude skills and hooks during upgrades. Claude
auto-recall now covers startup, resume, clear, compact, and fork.

- **Fixed:** tray restarts, honest service failures, stale integration detection,
  and warning copy that still described a removed action.
- **Added:** persistent Claude recall after context transitions plus explicit
  guidance to save durable decisions, gotchas, and verified outcomes.
- **Changed:** system warnings now expose exactly Approve and Deny; neither is a
  default keyboard action, and unrelated Claude hooks remain untouched.

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

Windows PowerShell:

```powershell
irm https://raw.githubusercontent.com/mclaut/ariadne/main/install.ps1 -OutFile install.ps1
powershell -ExecutionPolicy Bypass -File .\install.ps1
```

MIT licensed. The source of this Space is maintained in
`packaging/huggingface-space` in the Ariadne repository.
