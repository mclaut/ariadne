# Ariadne v0.3.0 launch kit

## Positioning

**One line:** Ariadne is local-first multilingual memory for coding agents that
need to remember across sessions without cloud storage or embedded vector
database locks.

**Short pitch:** Ariadne gives Codex, Claude Code, and any MCP client durable
memory through a native Go server, Qdrant, Ollama, and bge-m3 hybrid search. It
runs on Windows, macOS, and Linux; stores memory locally; and supports concurrent
agent sessions without a single-writer database bottleneck.

**Proof points:**

- Hybrid dense plus BM25 sparse retrieval, fused with reciprocal rank fusion.
- Cross-lingual recall across 100+ languages through bge-m3.
- Four MCP tools: save, recall, delete, and move.
- Native Windows, macOS, and Linux installers; no Docker required.
- Release checksums, CycloneDX SBOM, and Sigstore verification.
- MIT licensed and published through GitHub Releases and the MCP Registry.

**Project site:** https://ariadne-memory.mclaut124670.chatgpt.site

## Show HN

**Title**

> Show HN: Ariadne - local-first multilingual memory for coding agents

**Post**

> I built Ariadne because several concurrent coding-agent sessions kept turning
> embedded vector stores into a lock-contention problem. Ariadne moves memory to
> a real local server: a small Go MCP process talks to Qdrant for storage and to
> Ollama bge-m3 for multilingual embeddings. Retrieval combines dense vectors
> with BM25 sparse search and fuses the rankings with RRF.
>
> v0.3.0 adds a native Windows installer and consent-gated tray updates, alongside
> macOS and Linux builds. There is no Docker requirement, no cloud account, and
> no API key. Qdrant is pinned to loopback because memories are plaintext and the
> default server has no auth.
>
> Ariadne exposes four MCP tools: memory_save, memory_recall, memory_delete, and
> memory_move. It also has optional Claude Code hooks for session-start recall and
> curated local session summaries.
>
> I would value feedback on the installer, cross-lingual retrieval, and how MCP
> clients should communicate local runtime dependencies.
>
> Site: https://ariadne-memory.mclaut124670.chatgpt.site
>
> Source: https://github.com/mclaut/ariadne

## Reddit

Suggested communities, subject to each community's current self-promotion rules:
`r/LocalLLaMA`, `r/selfhosted`, `r/ollama`, and communities focused on MCP or
coding agents.

**Title**

> I built a local-first multilingual memory server for concurrent coding agents

**Post**

> Ariadne is an MIT-licensed MCP memory server built from Go, Qdrant, Ollama, and
> bge-m3. I made it after embedded vector stores became unreliable under several
> concurrent agent sessions.
>
> It keeps memory local, combines semantic retrieval with BM25 exact-term search,
> and supports cross-language recall. The new v0.3.0 release adds native Windows
> installation and automatic updates that always ask first. macOS and Linux are
> supported too, without Docker.
>
> The release includes checksums, an SBOM, Sigstore verification, and an MCPB.
> I am especially interested in real-world Windows feedback and multilingual
> retrieval tests.
>
> https://ariadne-memory.mclaut124670.chatgpt.site

## X / Bluesky

> Ariadne v0.3.0 is out: local-first multilingual memory for Codex, Claude Code,
> and any MCP client. Now native on Windows, macOS, and Linux. Go + Qdrant +
> Ollama bge-m3, hybrid dense/BM25 recall, no Docker, no cloud, no API key.
> https://ariadne-memory.mclaut124670.chatgpt.site

## LinkedIn

> Released Ariadne v0.3.0, an open-source local-first memory server for coding
> agents and MCP clients.
>
> The design uses a real Qdrant server instead of an embedded vector database, so
> concurrent agent sessions can read and write without fighting over a single
> local file. Retrieval combines multilingual bge-m3 embeddings with BM25 sparse
> search, while Ollama keeps embedding and optional session summarization local.
>
> This release adds a complete Windows path, including native Qdrant, verified
> Ollama installation, user-level startup tasks, tray version reporting, and
> consent-gated automatic updates. It also ships five platform archives,
> checksums, a CycloneDX SBOM, Sigstore verification, and an MCP Registry package.
>
> Ariadne is MIT licensed: https://github.com/mclaut/ariadne

## Product Hunt

**Name:** Ariadne

**Tagline:** Local-first multilingual memory for AI coding agents

**Description:**

> Give Codex, Claude Code, and any MCP client durable memory across sessions.
> Ariadne runs locally on Windows, macOS, and Linux with Go, Qdrant, Ollama, and
> bge-m3 hybrid search. No Docker, cloud account, or API key required.

## GitHub Discussion

**Title:** Ariadne v0.3.0: native Windows support and verifiable releases

**Body:**

> Ariadne v0.3.0 is available. The headline change is a complete native Windows
> installation and update path, backed by the same local Qdrant and Ollama stack
> used on macOS and Linux.
>
> The release also introduces five platform archives, SHA-256 checksums, a
> CycloneDX SBOM, Sigstore verification, a cross-platform MCPB, and automated MCP
> Registry publishing.
>
> Please use this thread for Windows installer reports, multilingual recall
> examples, MCP client compatibility notes, and requests for the next release.

## 60-second demo

1. Show two coding-agent sessions and the green Ariadne tray dot with `v0.3.0`.
2. In session A, save a Ukrainian project decision with `memory_save`.
3. In session B, ask the same question in English and show cross-lingual recall.
4. Save an exact error code, then recall it to show the BM25 contribution.
5. Open the tray, show Qdrant/Ollama health, and check for updates.
6. End on the Windows/macOS/Linux install selector and the GitHub repository.

## Launch sequence

1. Publish the GitHub Release and verify all assets and checksums.
2. Confirm the MCP Registry entry resolves to the same MCPB digest.
3. Publish the project site and set it as the GitHub repository homepage.
4. Add the social preview image and enable GitHub Discussions.
5. Post Show HN first, then answer technical questions for at least two hours.
6. Adapt the Reddit post to one community at a time; do not cross-post identical
   copy or ignore local self-promotion rules.
7. Publish the short social post and LinkedIn post with the demo clip.
8. Follow up after 48 hours with installation fixes, benchmarks, or a transparent
   retrospective rather than repeating the announcement.

## AI discovery checklist

- `server.json` and the MCP Registry identify the server and release package.
- `llms.txt`, structured data, Open Graph, sitemap, and permissive OAI-SearchBot
  rules describe the project consistently.
- README headings use explicit phrases such as "MCP memory server", "Windows",
  "Ollama", "Qdrant", and "hybrid search".
- GitHub topics cover Codex, Claude Code, MCP, Ollama, local-first, Qdrant,
  multilingual embeddings, and AI agents.
- Release notes and the site link directly to install commands and verification
  artifacts; no key facts exist only inside an image or video.
