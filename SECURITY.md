# Security Policy

## Supported versions

| Version | Security updates |
|---|---|
| 0.8.x | Supported |
| Earlier releases | Not supported |

Upgrade to the latest stable release before reporting an issue that may already
be fixed.

## Reporting a vulnerability

Do not open a public issue. Use GitHub's
[private vulnerability reporting](https://github.com/mclaut/ariadne/security/advisories/new)
to send a confidential report to the maintainers.

Include, when available:

- The affected Ariadne version, operating system, and component.
- Required access or attack preconditions.
- The expected security impact.
- Minimal reproduction steps or a proof of concept.
- A suggested mitigation or fix.

Redact real memories, credentials, tokens, personal paths, and unrelated logs.
Reports are reviewed on a best-effort basis. The maintainers will coordinate a
fix and disclosure timeline with the reporter when the issue is confirmed.

## Security model

- Qdrant is bound to `127.0.0.1` by default. Its default server has no
  authentication, and memory payloads are stored as plaintext.
- Semantic recall is project-scoped by default and requires `wing`.
  Cross-project recall requires `all_wings: true`, a stated purpose, and a
  human decision in Ariadne's system warning or tray fallback. The resulting grant is limited to 15 minutes, the MCP
  session, active wing, and collection. Origin weighting is applied only after
  authorization.
- Cross-project credential use has a separate one-time system/tray approval scoped to
  the exact source wing, target wing, resource name/path, and purpose. Ariadne
  never reads or returns credential values. This audited handshake does not yet
  provide an operating-system credential broker, so clients must still enforce
  the approved resource boundary.
- Approval requests, decisions, and consumptions are stored as separate
  append-only private state records. The read-only CLI can inspect pending
  requests but cannot approve them.
- New saves containing deterministic credential patterns are rejected. Import
  and hook paths redact detected values, while normal recall excludes
  `quarantined` records. `ariadnectl quarantine-secrets` is dry-run by default
  and applies a reversible, metadata-only quarantine when requested. An
  explicit `--apply --reconcile` restores prior status after detector refinement
  without erasing the quarantine audit trail.
- Pattern detection is defense in depth, not a secret manager. Agents and
  integrations must still treat the active repository as their filesystem
  boundary and must not obtain credentials from sibling projects.
- Ollama is local by default. A remote session-summary endpoint requires
  explicit opt-in because condensed transcript text is sent to that endpoint.
- Runtime data belongs under `~/.ariadne/` and should remain protected by the
  operating system user account.
- Release archives include SHA-256 checksums, a CycloneDX SBOM, and a keyless
  Sigstore bundle for the checksum manifest.
- Installers reuse an existing Qdrant without exposing or reconfiguring it.

Vulnerabilities that affect Qdrant, Ollama, Go, or another dependency without
an Ariadne-specific impact should also be reported to the relevant upstream
project.
