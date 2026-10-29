# Full recovery sets

`ariadnectl backup-full` uses a private regular file `~/.ariadne/full-backup.json`
(mode 0600 on Unix; restrict it with the account ACL on Windows). Every source is explicit: do not include the backup tree itself,
live Qdrant storage, raw SQLite/WAL files, or unrelated shared client configuration.
Paths in `sources`, `sqlite` and `prefixes` are absolute source paths keyed by
relative recovery paths. Missing sources fail the backup; nothing is silently skipped.

Required recovery layout is `runtime/bin/ariadnectl` and `runtime/bin/qdrant`
(with `.exe` on Windows), each mapped to its installed binary so root symlinks are
dereferenced. Include the application, wrappers, services and owned dependencies
required by the installation. `sqlite` creates online `VACUUM INTO` copies;
`prefixes` captures immutable prefixes of append-only files while permitting later
appends. Do not use `prefixes` for mutable databases or configuration.

Set `clients: true` to capture only the `ariadne` MCP entries from the selected
Codex profile and Claude, plus Ariadne hook handlers. Unrelated client entries are
excluded. Extra `audit_paths` identify relative audit subtrees that must survive
generation rotation. `audit`, `runtime/state`, `runtime/logs`, `runtime/metrics.db`,
the source manifest, plan and recovery receipt are always preserved if present.

`git_roots` lists relative repositories for read-only `git fsck` checks. If models
are included, set `models_root`, `ollama_binary`, `embedding_model` and
`embedding_dimensions`; these refer to the copied recovery tree. Include only
owned model manifests and all blobs they reference, plus the compatible runtime
and its dependencies. An isolated local model endpoint verifies embedding recovery.

Minimal shape (replace each placeholder with an owned absolute source):

```json
{
  "format": 1,
  "sources": {
    "runtime/bin/ariadnectl": "/absolute/runtime/bin/ariadnectl",
    "runtime/bin/qdrant": "/absolute/runtime/bin/qdrant",
    "source": "/absolute/project"
  },
  "sqlite": {"runtime/metrics.db": "/absolute/runtime/metrics.db"},
  "prefixes": {},
  "audit_paths": [],
  "git_roots": ["source"],
  "clients": true
}
```

When the plan exists, ordinary `ariadnectl backup` and due automatic backups use
this path. Automatic cadence is based on the verified full manifest, never a file
mtime or only a newer collection snapshot. The default interval remains seven days
when automatic consolidation requires a backup.

`legacy_full` is an optional array of exact `{name, manifest, sha256}` enrollments
for `full-YYYYMMDDTHHMMSSZ` directories directly under `~/.ariadne/backups`.
The recorded manifest must enumerate every entry (including self-excluded receipts),
and its entire content must still match. Timestamp names alone never authorize
discovery or deletion. Prior access/state/logs, legacy evidence, verification
records and top-level receipts are preserved in an immutable audit archive first.

Full sets are component-consistent captures, not global transactions or OS images.
OS frameworks and hardware remain external dependencies. Nested symlinks are
preserved; when relocating an installation, rebase installation-owned absolute
links in a new destination before activation. Merge saved client sections into
the destination configuration; do not replace unrelated entries. Never test a
recovery using the production `restore` command or live storage.

Recovery consumes temporary space for one independent complete copy plus native
database restore work. Interrupted/failed stages are retained for diagnosis and
block claims that all storage is bounded; audit archives intentionally accumulate.
