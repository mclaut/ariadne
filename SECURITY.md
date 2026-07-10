# Security Policy

## Supported versions

| Version | Security updates |
|---|---|
| 0.3.x | Supported |
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
