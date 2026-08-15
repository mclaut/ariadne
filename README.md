# ariadne

**English** · **[Українська](README.uk.md)**

A **native, local-first, multilingual memory server** for
[Codex](https://github.com/openai/codex),
[Claude Code](https://claude.com/claude-code), and any MCP client.
[Go](https://go.dev/) +
[Qdrant](https://qdrant.tech) + [bge-m3](https://huggingface.co/BAAI/bge-m3) —
no Docker, no cloud, no API keys.

Purpose-built for private coding-agent memory: a small native appliance rather
than a hosted, multi-tenant memory platform. The default path is offline and
cross-lingual, with observable retrieval cost and no account dependency.

[![Release](https://img.shields.io/github/v/release/mclaut/ariadne)](https://github.com/mclaut/ariadne/releases/latest)
[![CI](https://github.com/mclaut/ariadne/actions/workflows/ci.yml/badge.svg)](https://github.com/mclaut/ariadne/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/license-MIT-11120f.svg)](LICENSE)

**[Project site](https://mclaut.github.io/ariadne/)** ·
**[Hugging Face Space](https://huggingface.co/spaces/mclaut/ariadne)** ·
**[Latest release](https://github.com/mclaut/ariadne/releases/latest)**

Built as a replacement for embedded vector-DB memory backends that crash or
starve under several concurrent MCP sessions. ariadne is a **server**: one
Qdrant handles concurrent writes natively, so the whole single-writer /
lock-starvation class simply doesn't exist.

## What's New in v0.8.11

### Fixed

- **The desktop tray is now truly single-instance.** Before creating a status
  item, Ariadne takes an OS-level lock: non-blocking `flock` on macOS/Linux and
  a named mutex on Windows. A duplicate exits cleanly without adding an icon.
- **Old launchd jobs no longer multiply after login.** The installer reconciles
  every canonical and versioned tray, maintenance, and Ariadne-owned Qdrant job,
  then keeps only the canonical labels active.

### Added

- **History-preserving launchd cleanup.** Superseded plist files move to Ariadne's
  runtime archive instead of being deleted, including the legacy monitor plist.
- **Process-level diagnostics.** The full-stack doctor now reports the actual
  tray-process count as well as launchd ownership, so manual duplicates cannot
  hide behind one service label.

## Previously in v0.8.10

v0.8.10 made token attribution auditable with lossless Qdrant pagination,
recallable-versus-history coverage, separated measured and estimated provenance,
explicit gap classification, and safe opt-in attribution backfill.

## Previously in v0.8.9

### Fixed

- **Qdrant remains available with many concurrent agent sessions.** Each MCP
  process now uses one persistent gRPC connection instead of the client
  library's implicit pool of three, while the macOS launchd service receives an
  explicit 8192-file descriptor limit instead of inheriting 256.
- **Startup no longer hammers payload-index creation.** Ariadne inspects the
  existing schema and creates only missing indexes. Real storage errors now
  fail startup instead of being silently ignored.
- **Every short-lived client closes cleanly.** Hooks, import, install, and
  maintenance release their Qdrant connection when they finish.

### Added

- **Descriptor-pressure visibility.** `ariadnectl status`, the tray, and the
  full-stack doctor report Qdrant's open descriptors and configured limit, with
  a visible warning before exhaustion.

## Previously in v0.8.8

### Added

- **Fail-closed remote Qdrant authentication.** Remote gRPC requires an API key
  plus TLS; REST requires the same key and HTTPS. Long-running clients retain
  only a protected key-file path, never the key value. An explicit insecure
  override remains available for a user-managed SSH or equivalent tunnel.
- **Honest retrieval comparison.** `cmd/eval` now calculates deterministic
  macro Recall, MRR, and nDCG for judged BM25 and learned-sparse runs instead of
  claiming a SPLADE improvement without corpus evidence.

### Changed

- **Metrics schema v3 scales without discarding history.** Every recall event
  remains append-only, while an indexed 30-day path and transactional daily
  rollups make lifetime totals bounded and fast. Existing v2 databases migrate
  without changing raw rows or totals.
- **Collection scans are complete.** Memfile reconciliation now pages through
  every Qdrant point instead of relying on a fixed upper limit.
- **Maintenance has a reusable core.** Bounded retry/backoff orchestration lives
  in `internal/maintenance`; `ariadnectl` retains its established CLI and
  activity semantics.

### Fixed

- **Remote settings survive installation and self-update.** macOS, Linux, and
  Windows launchers propagate the same non-secret Qdrant transport settings;
  explicit Windows installer arguments still take precedence.
- **Repository tooling no longer compiles dependencies from `site/node_modules`.**
  The site is a separate Go module, and `make clean` archives generated assets
  with a recovery manifest instead of deleting them.

## Previously in v0.8.7

### Fixed

- **Tray restart now works under launchd's restricted environment.** Ariadne
  resolves Homebrew from standard macOS locations when `brew` is absent from
  launchd's minimal `PATH`, so both Qdrant and Ollama actually restart.
- **Restart always attempts recovery.** `ariadnectl restart` runs the start
  phase even when stopping one service reports an error, then returns every
  failure instead of leaving the stack down after a partial stop.
- **The completion message survives the tray restart.** The verified result is
  appended before the old tray exits; its launchd replacement displays the
  final notification and appends a delivered marker.

### Added

- **Verified service operations.** The tray observes Qdrant and Ollama before
  and after Start, Stop, or Restart, waits for the requested state, verifies PID
  changes on restart, and refuses to report success while the collection is
  unhealthy.
- **Visible PID and operation diagnostics.** Tray rows and `ariadnectl status`
  expose service PIDs; structured logs include duration, before/after state,
  command output, and the exact verification failure.

### Changed

- **Conflicting controls are locked during service work.** Start, Stop,
  Restart, maintenance, update, backup, and export cannot overlap while a
  service action is in progress.
- **Platform command errors keep their useful output.** macOS, Linux, and
  Windows service-control failures now preserve stderr/stdout for diagnosis.

## Previously in v0.8.6

### Fixed

- **Tray restarts are reliable on macOS.** A launchd-managed tray now asks its
  supervisor for one clean replacement instead of racing a second menu-bar
  process and occasionally leaving no icon.
- **Service failures are reported honestly.** `start`, `stop`, and `restart`
  propagate platform errors to the CLI and tray instead of displaying success.
- **Claude upgrades no longer leave stale integration files.** The installer
  validates and refreshes the shipped skill plus exact hook paths, matchers, and
  timeouts.

### Added

- **Persistent recall across Claude context transitions.** Auto-recall now runs
  for `startup`, `resume`, `clear`, `compact`, and `fork`, and injects an explicit
  reminder to save durable decisions, gotchas, and verified outcomes immediately.

### Changed

- **Approval is always a deliberate click.** System warnings contain only
  **Approve** and **Deny**; neither is default or focused, and Return/Enter or
  Escape cannot decide or dismiss the request.
- **Hook installation is update-aware.** Existing Ariadne hooks are updated in
  place while unrelated Claude hooks are preserved.

## Previously in v0.8.5

**The macOS approval warning comes to the foreground.** The native dialog
activates before it is displayed, so an access request cannot sit unnoticed
behind the active coding window. Activation changes visibility, never authority.

**Hugging Face publishing is valid.** The Space metadata stays within the Hub's
60-character `short_description` limit.

## Previously in v0.8.4

**Approval requests now interrupt visibly.** A new cross-wing or protected-
resource request opens a system warning dialog immediately instead of relying
on the tray badge and desktop notification alone. The dialog shows the bounded
scope and purpose with **Approve**, **Deny**, and a safe-default **Later** action.
Closing it, pressing Escape, or choosing Later grants nothing; a pending request
is shown again after one minute. The tray queue remains available as a fallback
and audit view.

The prompt uses the native macOS warning dialog, Windows system popup, or an
available KDialog/Zenity provider on Linux. Only an explicit Approve/Deny writes
the append-only decision record.

## Previously in v0.8.3

**Cross-project memory with a real human gate.** `all_wings: true` now creates a
pending request instead of searching. The Ariadne tray displays its active
wing, purpose, and bounded query; only an Approve click issues a 15-minute grant
scoped to that MCP client session, active wing, and collection. The client then
retries with `approval_id`. Exact-ID cross-wing recall follows the same path.

**Local context stays dominant.** After approval, external candidates receive a
0.70 origin weight and normally occupy at most two of five results. Responses
label local versus cross-wing origins and disclose the applied weight. The
weight is applied after authorization and is never treated as permission.

**Credentials require a second, one-time approval.** `credential_access`
creates an independent tray request for one exact source wing, target wing,
credential name/path, and purpose. Approval expires after five minutes and is
consumed once. Ariadne never reads or returns the value; requests, decisions,
and consumptions remain as separate append-only audit records.

## Previously in v0.8.2

**Project isolation is now default-deny.** Semantic `memory_recall` requires a
project `wing`; searching every project needs the explicit `all_wings: true`
opt-in intended for a user-requested cross-project audit. Session hooks resolve
the nearest repository root and may use a stable `.ariadne-wing` marker, so a
nested working directory cannot silently become a new namespace. The shared
Codex and Claude Code skill also treats the active workspace as the filesystem
boundary: a readable sibling project is not permission to borrow its `.env`,
credentials, endpoints, or configuration.

**Credential material is blocked and quarantinable.** MCP saves and the store
reject high-confidence private keys, credential URIs, known token formats, and
explicit secret assignments. Import and hook capture redact detected values,
consolidation validates output again, and recall excludes quarantined records
with defensive redaction on exact-ID output. `ariadnectl quarantine-secrets`
performs a metadata-only dry-run by default; `--apply` preserves the original
payload, vector, and previous status while removing the record from normal
recall. `--apply --reconcile` can restore the previous status after detector
refinement without erasing the quarantine audit trail.

## Previously in v0.8.1

**One service owner, visible health.** Ariadne detects duplicate macOS Qdrant
jobs before they can hide behind a green HTTP health check. `ariadnectl`
normalizes start, stop, and installer ownership to one canonical job while
retaining old plist files and every byte of memory history. The expanded doctor
resolves the active immutable runtime, checks both Codex and Claude MCP paths,
reports maintenance and launchd state, exposes attribution coverage, and warns
about runaway logs.

**Maintenance that distinguishes retry from review.** Each captured session is
curated atomically before same-day outputs are coalesced and deduplicated. An
independent local quality pass rejects memories that fuse unrelated concerns,
then gives invalid model output one focused repair pass.
Transient network and Ollama failures still receive bounded backoff;
deterministic schema, language, local-path, and quality failures become
explicit deferred work instead of replaying the whole stage. Unchanged sources
are not retried again with the same model and pipeline revision. Source diaries
remain active and append-only throughout, and safe deferred review does not
misreport otherwise successful maintenance as unhealthy.

**A stronger curator without heavier capture.** Capture and consolidation can
use different local models through `ARIADNE_CONSOLIDATION_MODEL` and
`ARIADNE_CONSOLIDATION_JUDGE_MODEL`. In a fixed 11-batch validation,
`qwen2.5:7b` deferred five batches while `qwen2.5:14b` cleared all eleven. The
14B curator is therefore the recommended local choice when memory permits;
capture can remain on a smaller model.

**A resilient, explainable tray.** The macOS LaunchAgent restarts the tray after
an abnormal exit but respects an explicit clean Quit. Lifecycle reasons are
appended to the tray log, so a missing icon no longer leaves an empty forensic
trail.

## Previously in v0.8.0

**Scoped append-only memory lifecycle.** Identical text may exist independently
in different projects and rooms. A move writes the destination record first and
retains the original as superseded history. Session capture records actual event
and capture times separately and assigns stable opaque source lineage. Daily
consolidation saves durable memories first, then archives source diaries by
metadata; it never deletes them. A first zero-output review remains active as
`candidate_empty` and needs a later confirming pass after a grace period before
archival. Normal recall hides archived/superseded records, while exact-ID lookup
or `include_archived: true` provides an explicit audit path.

**Conservative historical ranking.** Qdrant still supplies the dense+BM25 RRF
candidate set. A small bounded second pass adds source quality, explicit
temporal intent, and context-size signals. Old decisions and gotchas do not
decay merely because they are old; recency applies only when the query asks for
the latest/current/history-aware answer, and metadata cannot override a
material semantic lead.

**Non-destructive, observable maintenance.** Memfile sync skips unchanged source
revisions before embedding, imports changed revisions before marking older
chunks `superseded`, and preserves original observation timestamps; vanished
sources become `orphaned`, not deleted. Consolidation is batch- and
context-bounded, validates model output, and retains source diaries when any
promotion fails. The runner retries transient failures with capped backoff,
records append-only activity, and exposes failed, partial, stuck, or stale state
in status and the tray. Backup rotation archives older snapshots.

**Deterministic regression evaluation.** `go run ./cmd/eval` executes the
multilingual coding-memory ranking suite in `evaluation/coding-memory.json`
without touching Qdrant or Ollama.

## Previously in v0.7.0

**Exact retrieval and immediate durable memory.** `memory_recall` now accepts an
exact scoped-content `id`, so agents can retrieve and verify one memory without an
embedding call or approximate ranking. Semantic recall can also be scoped by
both project (`wing`) and category (`room`).

The shared Ariadne skill now requires agents to save completed release,
deployment, migration, audit, incident-resolution, and verified-status reports
to `reference` immediately when the outcome becomes known. This is proactive:
the agent does not wait for SessionEnd, PreCompact, daily consolidation, or a
separate “remember this” command. Decisions and hard-won gotchas follow the same
immediate-save discipline.

Token metrics separate source-backed measurement from unknown delivery instead
of treating every legacy recall as negative savings:

- **measured saved/net** — source-backed context benefit after attributed cost;
- **attribution coverage** — the share of observed recall cost with provenance;
- **unattributed** — visible legacy/manual delivery with no invented benefit.

The tray shows measured savings, coverage, recall count, and unattributed cost.
`ariadnectl metrics` and JSON retain the complete observed-cost counters.

```json
{
  "id": "2704862554782470108"
}
```

## Previously in v0.6.0

**Automatic diary distillation.** SessionEnd capture writes one concise,
local-model diary memory per substantive session. Daily maintenance revisits
diary entries older than 24 hours, groups them by project and day, and promotes
durable decisions with rationale, verified gotchas, critical constraints, and
important open risks. Source chronology remains archived for audit instead of
being discarded.

The process is fail-safe and append-only: Ariadne creates a due weekly Qdrant
snapshot before archival, saves every distilled memory, then marks the source
group archived. A first empty pass stays active for later confirmation, and a
failure leaves the source group active for retry. The summary endpoint remains
local-only unless remote capture is explicitly enabled.

Preview or run it manually:

```bash
ariadnectl consolidate --before 24h --dry-run
ariadnectl maintenance  # memfile sync + consolidation, with bounded retries
ariadnectl consolidate --before 24h  # consolidation only
ariadnectl requeue-empty --dry-run  # inspect records archived by the legacy one-pass empty policy
```

The installer schedules the 04:30 daily memory maintenance job on macOS and
Linux. A failed stage is retried up to three times with bounded exponential
backoff; partial import failures return non-zero and block consolidation.
systemd uses `Persistent=true`; a loaded launchd calendar agent receives one
catch-up start after the Mac wakes when a scheduled time was missed. The tray
shows the latest outcome, warns when maintenance failed, remains partial, is
stuck, or is older than 36 hours, and offers **Run maintenance now**. A
`complete_with_deferred` outcome remains observable without turning the service
indicator orange. Output is written to `~/.ariadne/logs/maintenance.log`;
append-only outcomes live in
`~/.ariadne/state/activity.jsonl`.

## Previously in v0.5.0

- **Explicit Windows client integration** — the installer detects Claude Code
  and Codex CLI, then asks which one to configure. It never creates settings for
  an absent client and non-interactive installs require an explicit choice.
- **Core-only and configure-later modes** — install the local Ariadne stack with
  `-CoreOnly`, then connect Claude or Codex later with `-ConfigureClients`.
  Consent-gated updates preserve existing client configurations unchanged.
- **Physical and VM preflight** — PowerShell now reports Windows, CPU, RAM, disk,
  and machine type; enforces the default models' minimum resources; gives
  Proxmox/KVM CPU guidance; and directly executes `qdrant.exe --version` before
  registering the service.

## What's New in v0.4.0

- **Local token-efficiency metrics** — Ariadne estimates confirmed savings,
  recall overhead, and signed net context benefit for automatic and MCP recalls. Totals are
  available through `ariadnectl metrics`, its JSON form, and the tray menu.
- **Content-free accounting** — only numeric counters and opaque event hashes
  are stored locally. Repeated hook delivery counts as overhead without claiming
  the same represented session context twice.

## What's New in v0.3.1

- **Clean Windows prerequisite repair** — the installer now detects the
  Microsoft Visual C++ Runtime required by the official Qdrant Windows binary.
  When it is missing or older than 14.44, Ariadne downloads Microsoft's
  official redistributable, verifies its Authenticode signer, and requests
  administrator approval for that prerequisite only.
- **Actionable Qdrant failures** — startup gets a full 60-second window, loader
  and process errors are preserved in `~/.ariadne/logs/qdrant.log`, and the
  installer prints the scheduled-task result plus the last log lines instead of
  returning only a generic timeout.
- **Real Windows Qdrant coverage** — CI now downloads the exact pinned Qdrant
  archive, verifies its SHA-256 digest, starts it with Ariadne's loopback/storage
  settings, and waits for `/healthz` on a Windows runner.

## What's New in v0.3.0

- **Native Windows installation** — `install.ps1` installs release binaries,
  native Qdrant, signed Ollama, user-level startup tasks, Codex/Claude Code MCP
  bindings, the skill, and session hooks. Docker is not required; administrator
  approval is needed only when Windows lacks Qdrant's Microsoft VC++ Runtime.
- **Windows self-updates** — the version-aware tray now offers the same explicit
  confirmation and automatic restart flow on Windows as on macOS and Linux.
- **Five release targets** — Windows x64, Linux x64/ARM64, and macOS
  Intel/Apple Silicon archives are built from tags by GitHub Actions.
- **Verifiable artifacts** — releases include SHA-256 checksums, a CycloneDX
  SBOM, and a keyless Sigstore bundle for the checksum manifest.
- **MCP discovery** — a cross-platform MCPB and `server.json` are generated from
  the release binaries and published to the official MCP Registry with GitHub
  OIDC.
- **Public project site and launch kit** — structured metadata, `llms.txt`,
  platform installation paths, architecture, security notes, and ready-to-use
  launch copy make Ariadne easier for both people and AI systems to discover.

## Why

- **Stable** — Qdrant server, not an embedded HNSW that SIGSEGVs on compaction.
- **Multilingual** — bge-m3 covers 100+ languages; cross-lingual recall works
  (an English query finds Ukrainian notes, cosine ~0.8–0.94 across uk/ru/en/es/
  de/it/pl/ro/hu/lt/lv/et/fi/fr/ar).
- **Hybrid search** — dense (bge-m3) + BM25 sparse (pure Go tokenizer; Qdrant
  computes IDF server-side) fused with RRF, followed by a bounded historical-
  quality rerank. Exact terms/codes/names rank sharply; explicit temporal queries
  prefer the correct dated event without blindly decaying durable decisions.
- **Default-deny scoped recall** — semantic searches require a project (`wing`)
  and may be narrowed further by category (`room`). Cross-project recall needs
  an explicit purpose plus a human-approved tray request.
- **Native** — Qdrant binary + Ollama on Windows, macOS, and Linux; supported
  NVIDIA/AMD acceleration, Metal on Apple Silicon, and a CPU fallback.

## Components

| Path | What |
|---|---|
| `cmd/ariadne` | MCP server (stdio). Tools: `memory_save`, `memory_recall`, `credential_access`, `memory_delete`, `memory_move`. |
| `cmd/import` | Backfill from a chromadb sqlite, markdown memory files or JSONL (batched embeds). |
| `cmd/hook` | Claude Code session hooks (`ariadne-hook`): SessionStart auto-recall plus SessionEnd/PreCompact auto-capture. |
| `cmd/install` | One-shot installer (macOS/Linux): preflight, reuse-or-install Qdrant, services, MCP, skill, hooks. Windows uses `install.ps1`. |
| `cmd/ariadnectl` | Control + health core: status/metrics, lifecycle, backup/restore/export, maintenance/consolidation, quarantine and approval inspection. |
| `cmd/eval` | Read-only ranking regressions and judged retrieval-run comparison. |
| `internal/store` | Storage core: embed (Ollama), BM25 sparse, Qdrant hybrid. |
| `internal/maintenance` | UI-independent bounded retry engine for maintenance stages. |
| `internal/qdrantauth` | API-key loading and fail-closed remote Qdrant transport policy. |
| `internal/retrievaleval` | Recall/MRR/nDCG scoring for comparable ranked runs. |
| `cmd/ariadne-tray` | Cross-platform tray monitor (macOS/Linux/Windows) — pure-Go, localized, over the `ariadnectl` core. |
| `skills/ariadne` | Codex and Claude Code skill: scoped recall/save discipline + `doctor.sh`/`recall.sh`. |
| `deploy/` | LaunchAgent / systemd templates: Qdrant service, daily memfiles-sync, tray autostart. |
| `poc/` | Standalone experiments that validated the stack. |

## Setup

### Windows

Open PowerShell as your regular user. Download the script first so it remains
inspectable before execution:

```powershell
irm https://raw.githubusercontent.com/mclaut/ariadne/main/install.ps1 -OutFile install.ps1
powershell -ExecutionPolicy Bypass -File .\install.ps1
```

The Windows installer downloads the matching GitHub Release archive and checks
it against `SHA256SUMS`. If Ollama is missing, it downloads the official
`OllamaSetup.exe`, verifies that its Authenticode signer is **Ollama Inc.**, and
installs it without elevation. It installs the pinned native Qdrant x64 asset
after verifying its SHA-256 digest, registers Qdrant and `ariadne-tray` as
current-user scheduled tasks, pulls `bge-m3` and the summary model, then
offers to register Ariadne with the detected Codex and/or Claude Code CLI.
Client configuration is always explicit: the installer never creates settings
for software that is not installed.

Choose an integration directly for unattended setup, install only the core, or
configure clients later:

```powershell
.\install.ps1 -Yes -Integration Claude
.\install.ps1 -Yes -Integration Codex
.\install.ps1 -Yes -Integration Both
.\install.ps1 -Yes -CoreOnly
.\install.ps1 -ConfigureClients -Integration Codex
```

`-Integration None` and `-CoreOnly` leave all client configuration untouched.
Tray-driven updates also preserve existing client settings; they do not enroll
a newly discovered client during an update.

The official Qdrant Windows binary requires Microsoft Visual C++ Runtime 14.44
or newer. Ariadne checks it before starting Qdrant. If it is absent or outdated,
the installer downloads the official Microsoft x64 redistributable, verifies
the Microsoft Authenticode signer, and Windows requests administrator approval
for that prerequisite. Rerun the same install command after a cancelled prompt.
If Qdrant still cannot start, the installer prints diagnostics and the tail of
`~/.ariadne/logs/qdrant.log` directly in PowerShell.

Qdrant installed by Ariadne is always bound to `127.0.0.1`. An intentionally
reused remote Qdrant can be configured without putting its key in client files:

```powershell
.\install.ps1 -QdrantHost qdrant.example -QdrantRestPort 6333 `
  -QdrantGrpcPort 6334 -QdrantApiKeyFile C:\secure\qdrant.key -QdrantTls
```

The installer validates the authenticated health endpoint and propagates only
the key-file path and transport settings to Codex, Claude, hooks, and the tray.
`-AllowInsecureRemoteQdrant` is the explicit exception for an independently
encrypted private tunnel. Ollama remains managed by its native Windows app and
serves `http://localhost:11434`. Requirements: Windows 10 22H2
or newer, x64, Windows PowerShell 5.1+, at least 8 GiB RAM and 15 GiB free disk
for the default local models (16 GiB RAM recommended, 4+ CPU cores preferred).
Virtual machines need the same guest resources; for Proxmox/KVM expose a modern
CPU type such as `host`. The installer prints the detected hardware before any
download and validates that `qdrant.exe` can execute on the machine.
Use `-SkipOllama` or `-SkipModels` when those dependencies are provisioned
separately.

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
curl -fsSL https://raw.githubusercontent.com/mclaut/ariadne/main/install.sh | sh -s -- -strict-supply-chain
```

(sudo — for distro packages and the official Ollama installer on Linux —
prompts on the terminal, so the pipe is fine.)

Supply-chain defaults are pinned: `install.sh` installs `go1.26.2` unless
`ARIADNE_GO_VERSION` is set, verifies the Go tarball SHA256 before unpacking,
and the Go installer installs Qdrant from a pinned `-qdrant-version` release
(default `v1.18.2`) after checking the GitHub release-asset digest. For locked
down environments, pass `-strict-supply-chain`; on Linux this refuses the
Ollama `curl | sh` bootstrap and asks you to install Ollama manually first.

#### Linux and Ollama

Ariadne uses Ollama for `bge-m3` embeddings and, when session hooks are enabled,
for the local summary model. An existing local Ollama installation is reused.
If the `ollama` command is missing, the default installer runs Ollama's official
Linux install script, waits for `http://localhost:11434` to become ready, then
pulls `bge-m3` and the configured summary model.

If Ollama is installed but its daemon is stopped, start it before running the
Ariadne installer:

```bash
sudo systemctl enable ollama
sudo systemctl start ollama
sudo systemctl status ollama
curl -fsS http://127.0.0.1:11434/api/version
```

On Linux, Ollama remains a system service owned by the OS. The
`ariadnectl start`, `ariadnectl stop`, and `ariadnectl restart` commands manage
Ariadne's Qdrant user unit and deliberately leave Ollama alone. On systems
without systemd, run `ollama serve` under the local service manager instead.
See Ollama's official
[Linux installation guide](https://docs.ollama.com/linux) for manual packages,
ARM64, NVIDIA, and AMD/ROCm setup.

With `-strict-supply-chain`, Ariadne never runs Ollama's `curl | sh` installer.
Install and start Ollama yourself, then rerun Ariadne. `-skip-deps` also leaves
Ollama and the Linux tray dependencies entirely to you. To reuse Ollama on
another machine, provision the required models there and pass
`-ollama http://host:11434` together with `-skip-model-pull`.

### From a clone (macOS/Linux, or to hack on it)

```bash
go run ./cmd/install -dry-run   # preflight + plan, changes nothing
go run ./cmd/install -yes       # do it
```

The installer is deliberately careful with existing infrastructure:

- **An already-running Qdrant is REUSED, never restarted or reconfigured** —
  Ariadne only adds its own collection. A busy port that is *not* Qdrant aborts
  the install. Use `-qdrant-host/-qdrant-rest/-qdrant-grpc` to point at a
  remote instance, `-ollama` for a remote embedder. Remote Qdrant is
  fail-closed unless API-key authentication and TLS are configured (or the
  explicit insecure override is set for an already-encrypted private tunnel).
- **GPU / RAM / disk are checked up front** and insufficiencies are stated
  plainly (no GPU → an honest "embeddings on CPU, ~10x slower" warning;
  <6GiB RAM or <5GiB disk → hard FAIL).
- Idempotent: re-running skips everything that is already in place.

It installs the Qdrant binary + service (macOS LaunchAgent / Linux systemd
user unit, loopback-only), builds the Go binaries into `~/.ariadne/bin`,
pulls `bge-m3`, creates the collection, registers the MCP server in
`~/.claude.json` (backup kept) and installs the Claude Code skill.
Codex and other MCP clients can use the same installation by registering
`~/.ariadne/bin/ariadne` as a stdio MCP server.

### Release archives and MCPB

Every stable release includes native archives for five targets, a portable
cross-platform `.mcpb`, `server.json`, `SHA256SUMS`, a CycloneDX SBOM, and a
Sigstore bundle. The MCPB contains the MCP server for all supported platforms;
Qdrant and Ollama still need to be installed by the native OS installer first.
See [GitHub Releases](https://github.com/mclaut/ariadne/releases/latest) for
manual downloads and verification files.

### Manual setup (macOS example)

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
memories are stored in plaintext payloads. For an intentional remote Qdrant,
set an admin or collection-scoped API key on the server and configure Ariadne
with `ARIADNE_QDRANT_API_KEY_FILE` (recommended, mode `0600`) or the
process-only `ARIADNE_QDRANT_API_KEY`, plus `ARIADNE_QDRANT_TLS=1`. The Go
client sends the key through gRPC metadata and every Qdrant REST request uses
the `api-key` header. Qdrant itself recommends TLS whenever an API key is used.
The installer accepts the file form so generated MCP configuration retains
only a path, never the secret value; generated maintenance, hook, and tray
launchers receive the same non-secret client settings. `ARIADNE_QDRANT_ALLOW_INSECURE_REMOTE=1`
is an explicit escape hatch for a separately encrypted transport such as a
private tunnel; it must not be used on an ordinary LAN.

Config via env (defaults in brackets): `ARIADNE_QDRANT_HOST` [localhost],
`ARIADNE_QDRANT_PORT` [6334], `ARIADNE_QDRANT_REST`
[http://localhost:6333], `ARIADNE_QDRANT_API_KEY_FILE`,
`ARIADNE_QDRANT_API_KEY`, `ARIADNE_QDRANT_TLS` [0],
`ARIADNE_QDRANT_ALLOW_INSECURE_REMOTE` [0], `ARIADNE_OLLAMA`
[http://localhost:11434], `ARIADNE_MODEL` [bge-m3],
`ARIADNE_COLLECTION` [ariadne].

## Codex and Claude Code skill

`skills/ariadne/` teaches agents when to recall, what (not) to save, and
how to operate the stack; `tools/doctor.sh` checks the whole chain
(binaries → services → model → collection → binding → MCP registration) and
`tools/recall.sh "query"` does CLI recall without MCP. Install (a real copy —
symlinked skills are not discovered at session start):

```bash
cp -R skills/ariadne ~/.claude/skills/ariadne
cp -R skills/ariadne ~/.codex/skills/ariadne
```

## Session hooks — auto-recall & auto-capture

The installer registers two Claude Code lifecycle hooks (`cmd/hook`, binary
`ariadne-hook`; skip with `-skip-hooks`):

- **SessionStart → auto-recall.** When a session starts in a project that HAS
  memories (wing = the nearest project root or its `.ariadne-wing` marker), the
  top hits are injected as context —
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
`ARIADNE_SUMMARY_OLLAMA` [defaults to `ARIADNE_OLLAMA` or localhost],
`ARIADNE_SUMMARY_MODEL` [qwen2.5:7b], `ARIADNE_CAPTURE_MIN_TURNS` [3]. The
summary endpoint must be local by default; if you deliberately want a remote
summary model, set `ARIADNE_SUMMARY_OLLAMA` and `ARIADNE_CAPTURE_REMOTE=1`
because condensed transcript text is sent to that endpoint for summarization.
The summary model is loaded only for capture and unloaded right after
(`keep_alive:0`), so it costs RAM only briefly; for a smaller footprint set
`ARIADNE_SUMMARY_MODEL=qwen2.5:3b` (~2GB vs ~4.7GB, at some summary quality) —
or pass `-summary-model` to the installer so it pulls that one.

Consolidation can use a stronger model without increasing capture latency:
`ARIADNE_CONSOLIDATION_MODEL` falls back to `ARIADNE_SUMMARY_MODEL`, while
`ARIADNE_CONSOLIDATION_JUDGE_MODEL` falls back to the consolidation model. A
24GB machine can run `qwen2.5:14b` (~9GB) for both roles; validate it with a
dry-run before changing scheduled maintenance.

### Daily diary consolidation

`ariadnectl consolidate` turns short-lived session chronology into compact
long-term knowledge. It selects `room=diary` entries older than 24 hours by
default, processes each captured session atomically, then coalesces and
deduplicates validated same-day outputs within each project (`wing`). The
configured local consolidation model emits only `decisions`, `gotchas`, and
`reference` memories. Requests are split to a bounded source-token budget.
An empty result first marks the source `candidate_empty`; only a later empty
pass after the default seven-day grace period may archive it as
`empty_confirmed`.

Automatic consolidation creates a native Qdrant backup only when archival is
needed and the latest backup is older than seven days (override with
`ARIADNE_BACKUP_MIN_INTERVAL`); manual `ariadnectl backup` is unconditional.
New memories are saved before source diary entries are marked archived; source
text and vectors remain intact. Deterministic failures remain active and receive
a model-and-pipeline revision marker, so unchanged input is reconsidered only
after the model or curation pipeline changes. Use `--before 48h` for a longer
review window or `--dry-run` to inspect output without changing either the store
or production activity history. Search history with `include_archived: true`. Remote summary endpoints
remain blocked unless `ARIADNE_CAPTURE_REMOTE=1` is explicitly set.

### Coding-memory evaluation

The checked-in suite exercises historical ranking invariants without a live
database or model:

```bash
go run ./cmd/eval -cases evaluation/coding-memory.json
```

It currently covers recency for explicitly temporal queries, no blind decay for
durable decisions, semantic-dominance bounds, oversized legacy payloads, source
quality, and multilingual temporal intent. To compare actual BM25 and
learned-sparse outputs, export their ordered point IDs for the same judged
queries and run:

```bash
go run ./cmd/eval \
  -retrieval-runs evaluation/retrieval-runs.example.json \
  -baseline bm25
```

The comparison reports macro-averaged Recall, MRR, nDCG, and nDCG delta at each
cutoff; `-json` emits machine-readable scores. The checked-in file is an
illustrative schema, not product evidence. A real conclusion requires frozen
relevance judgments and runs from the same corpus, query set, filters, and
cutoffs. The evaluator never contacts Qdrant, Ollama, or a remote model and
never mutates memory.

## Monitor

A tray/menu-bar monitor polls `ariadnectl status -json` every 5s and shows a
green/orange/red/grey icon, per-service detail, and start/stop/restart/backup
actions; it notifies when a service drops. The menu and tooltip show the current
Ariadne version. A background check queries GitHub Releases every six hours and
offers a consent-gated update when a newer stable version exists on Windows,
macOS, and Linux; update output is written to
`~/.ariadne/logs/update.log`. The `ariadne-tray` UI is localized —
**7 languages** (English, Українська, Deutsch, Italiano, Español, Français,
Polski) with a live **🌐 Language** switcher that shows the active one at a
glance. The choice persists in `~/.ariadne/lang` and `ariadnectl` follows it, so
the whole interface stays in one language. Adding a language is one block in
`internal/i18n`.

### Estimated token savings

Ariadne locally tracks how much observed recall context it delivers and how
much bounded source context each attributed memory represents. The tray shows
all-time measured savings, attribution coverage, recall count, and
unattributed delivery; the full counters are available as:

```bash
ariadnectl metrics
ariadnectl metrics -json
```

For source-backed recalls, measured net is represented source context minus the
attributed share of the complete observed cost (query plus response). Recalling
the same memory twice in one client session credits its source only once while
counting both deliveries. Legacy/manual memories without source-size metadata
receive no invented benefit: their cost is reported separately as
`unattributed`, not as negative overhead. `memory_save` accepts optional
`source_tokens` for integrations that know a bounded source size and
`occurred_at` for historical facts; capture and
consolidation populate provenance automatically. The deterministic multilingual
estimate uses UTF-8 bytes because exact tokenizers vary by client and model; `~`
marks a conservative context-reuse estimate, not provider billing or an A/B
counterfactual. Metrics contain only numeric counters and opaque hashes, never
memory or transcript text, and stay in `~/.ariadne/metrics.db` with user-only
permissions. Raw events remain append-only; schema v3 maintains transactional
daily summaries for bounded lifetime reads and indexes the rolling time window.
Set `ARIADNE_METRICS=0` to disable new records.

- **`ariadne-tray`** (pure-Go, `fyne.io/systray`) is the monitor on every
  platform: the installer builds it into `~/.ariadne/bin` and registers autostart
  — a `~/.config/autostart` entry on Linux, a `com.ariadne.tray` LaunchAgent on
  macOS (migrating off any older Swift monitor so you get one icon). On Linux it
  needs a StatusNotifierItem host (native on KDE/XFCE; on GNOME install the
  "AppIndicator and KStatusNotifierItem" extension). On Windows it starts from
  a current-user scheduled task and updates through the signed release archive.

## Backup & portability

Two distinct concepts:

- **Backup / restore** — a fast, full, native Qdrant snapshot (includes vectors;
  one-to-one restore, tied to the embedding model). For recovering after damage.
  ```bash
  ariadnectl backup            # 10 recent snapshots; older ones move to backups/archive
  ariadnectl restore <file>    # recover the collection from a snapshot (destructive)
  ```
- **Export / import** — portable JSONL with text, scope, provenance, historical
  status, timestamps, and source accounting (no vectors, so it moves between
  machines and re-embeds with any model). For migration/inspection.
  ```bash
  ariadnectl export [file]                        # all memories → JSONL
  import -source jsonl -file export.jsonl           # re-embed + upsert an export
  ```

`import` also backfills from an archived chromadb sqlite
(`-source chroma -db <file>`) or markdown memory files (`-source memfiles`).
All imports are idempotent within a `(wing, room, text)` scope; identical text
may intentionally exist in different projects or rooms. For memfiles, `-sync`
uses a privacy-safe source key and relative source path, skips unchanged files
before embedding, imports a changed revision first, preserves original event
time, marks prior chunks `superseded`, and marks chunks from vanished sources
`orphaned`; it does not delete their history. The
installer registers a daily agent
(`com.ariadne.sync` / `ariadne-sync.timer`) that runs retry-bounded memfile sync
plus diary consolidation for you; use `ariadnectl maintenance` or the tray
button after large note edits. The
`-force` flag is reserved for an explicit migration/repair pass; routine sync
must omit it so unchanged revisions stay out of the embedding queue.

## Status

v0.8.11 — current release. Cross-platform single-instance tray enforcement,
canonical launchd ownership reconciliation, history-preserving legacy plist archival,
and actual tray-process diagnostics; lossless Qdrant attribution pagination, recallable-versus-history
corpus accounting, measured-versus-estimated provenance, explicit safe attribution backfill,
and bounded agent-side source accounting; single-connection MCP clients, launchd descriptor capacity,
payload-index reconciliation, graceful Qdrant client shutdown, and descriptor-pressure diagnostics;
fail-closed remote Qdrant authentication, append-only metrics v3 rollups,
complete paginated collection scans, deterministic BM25/learned-sparse evaluation,
reusable maintenance orchestration, and reversible site cleanup; verified Qdrant/Ollama lifecycle operations, launchd-safe Homebrew resolution,
durable post-restart notifications, PID observability, conflict-free service controls,
deliberate two-button approval warnings, reliable supervised tray restarts,
honest service-control errors, persistent Claude recall across context transitions, update-aware hooks and skills,
foreground macOS approval warnings, human-approved cross-wing recall, origin weighting,
one-time credential grants, append-only approval audit, default-deny project recall, deterministic credential
blocking/redaction, append-only secret quarantine, stable project markers,
runtime ownership diagnostics, repair-aware maintenance,
tray lifecycle logging, truthful full-stack doctor checks, exact ID retrieval,
room-scoped hybrid recall, append-only
source history, conservative temporal ranking, scoped identities, incremental
timestamp-safe memfile sync, two-pass empty consolidation, retry-bounded
maintenance with history/storage observability, immediate
durable reference/report capture, honest token-efficiency accounting, automatic
daily diary distillation, explicit Windows client setup,
local token-efficiency metrics,
native desktop installation,
session hooks (auto-recall + curated auto-capture), and time-ordered diary are
all live; several thousand memories are in daily use. Bulk import batches
embeddings for a large backfill speedup. A
learned-sparse upgrade (bge-m3 SPLADE on a CUDA box) is optional if BM25 proves
too weak for morphology-rich languages.

## Contributing and security

Reproducible bug reports, native Windows installer feedback, and focused feature
requests are welcome through the
[issue chooser](https://github.com/mclaut/ariadne/issues/new/choose). Setup and
usage questions belong in [GitHub Discussions](https://github.com/mclaut/ariadne/discussions).
See [CONTRIBUTING.md](CONTRIBUTING.md) for local checks and pull request guidance.

Generated site artifacts can be cleared from the working tree without data
loss: `make clean` moves build output to `~/.ariadne/archive/site/` with a
recovery manifest, while `make clean-all` includes `site/node_modules`.

Do not open a public issue for a vulnerability. Follow [SECURITY.md](SECURITY.md)
and use GitHub's private vulnerability reporting instead.

## Contributors

- **Project maintainer** — architecture, product direction, validation, and
  release decisions.
- **[OpenAI Codex](https://github.com/codex)** — implementation, tests,
  documentation, cross-platform packaging, and release engineering.
- **[Claude Code](https://github.com/claude)** — implementation, localization,
  installer hardening, and cross-platform runtime improvements.

All AI-assisted changes are reviewed and approved by the maintainer.

## License

MIT
