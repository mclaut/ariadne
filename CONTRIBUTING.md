# Contributing to Ariadne

Thanks for helping make Ariadne more reliable across operating systems,
languages, and MCP clients.

## Before opening an issue

- Use [GitHub Discussions](https://github.com/mclaut/ariadne/discussions) for
  setup questions, usage help, and design conversations.
- Use the issue chooser for reproducible bugs, Windows installer reports, and
  feature requests.
- Report security vulnerabilities privately through the process in
  [SECURITY.md](SECURITY.md). Do not disclose them in a public issue.
- Search existing issues and discussions before creating a new report.

Never paste real memory text, credentials, tokens, private paths, or unredacted
logs. Replace sensitive values with clear placeholders while preserving the
structure needed to reproduce the problem.

## Development setup

The Go version is declared in `go.mod`. Runtime integration work may also need:

- Qdrant bound to `127.0.0.1`.
- Ollama with the `bge-m3` model.
- Node.js 22 for changes under `site/`.

Run the core checks from the repository root:

```bash
go build ./...
go test ./...
golangci-lint run
```

Format Go changes with:

```bash
golangci-lint fmt
```

For site changes:

```bash
cd site
npm ci
npm run lint
npm test
GITHUB_PAGES=true npm run build:pages
```

The experiments under `poc/` are a separate Go module.

## Pull requests

- Keep each pull request focused on one behavioral change.
- Match the existing package boundaries and local style.
- Add focused tests for fixes and broader coverage for shared behavior.
- Update user-facing documentation when commands, configuration, or supported
  platforms change.
- Keep Qdrant loopback-only by default. It has no authentication by default and
  memory payloads are stored as plaintext.
- Do not add personal references, private infrastructure, local machine paths,
  credentials, or production memory content to code, tests, docs, fixtures, or
  commit history.
- Explain any platform-specific behavior and how it was tested.

CI must pass on Linux and Windows. Release changes should also preserve the
macOS Intel and Apple Silicon build paths.

## Commit messages

Use short, imperative messages with a useful scope, for example:

```text
fix: preserve tray version during update
docs: clarify remote Ollama security
test: cover Windows task discovery
```

AI-assisted contributions are welcome. The contributor remains responsible for
reviewing, testing, and understanding every submitted change.
