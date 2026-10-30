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
this path, with **one full recovery set and no separate runtime/config/snapshot
copies**. A plan is the explicit opt-in to full-only retention. Without a plan,
the existing component-only mode remains available. Automatic cadence is based on the verified full manifest, never a file
mtime or only a newer collection snapshot. The default interval remains seven days
when automatic consolidation requires a backup.

`legacy_full` is an optional array of exact `{name, manifest, sha256}` enrollments
for `full-YYYYMMDDTHHMMSSZ` directories directly under `~/.ariadne/backups`.
The recorded manifest must enumerate every entry (including self-excluded receipts),
and its entire content must still match. Timestamp names alone never authorize
discovery or deletion. Prior access/state/logs, legacy evidence, verification
records and top-level receipts are preserved in an immutable audit archive first.

After a cleanup-only failure, `ariadnectl backup-full --retry-cleanup` re-verifies
the already restored full keeper and retries only enrolled legacy cleanup. It
does not create a second full set or bypass the immutable recovery proof.

Full sets are component-consistent captures, not global transactions or OS images.

In full-only mode, snapshots are created directly under the pending full set at
`snapshots/snapshot-<collection>`. No separate `previous/snapshot-*` generation is
published. Native snapshots recorded in the full recovery receipt expire only
after that full set has passed independent recovery and publication. Cleanup
retries validate the full and the matching nested snapshot manifests first;
unrelated native snapshots are not selected. Earlier full layouts using
`snapshots/<collection>` remain readable by cleanup retries.

Runtime upgrades, same-version reinstalls and client-configuration capture reuse
the sealed successful full keeper. A missing, modified or unsuccessful full set
blocks these paths instead of silently creating component backups. The pre-upgrade check also requires the full to contain the currently installed
binaries and application. Run `ariadnectl backup-full` first if the keeper is
stale or when establishing full-only retention.
Existing `previous/runtime`, `previous/snapshot-*`, `previous/config-*` and
release-local legacy copies are not deleted by switching to full-only mode.
Inventory them, preserve their audit, and approve an exact cleanup plan separately.
The `backup-retention runtime-cleanup` compatibility command verifies the full
keeper in this mode; it does not remove existing component copies.

Old full manifests, plans, recovery receipts, access/activity history, and nested
snapshot manifests and restore logs are archived under `backups/full-audit`
before full rotation. Audit archives remain outside rotation.

OS frameworks and hardware remain external dependencies. Nested symlinks are
preserved; when relocating an installation, rebase installation-owned absolute
links in a new destination before activation. Merge saved client sections into
the destination configuration; do not replace unrelated entries. Never test a
recovery using the production `restore` command or live storage.

Recovery consumes temporary space for one independent complete copy plus native
database restore work. Interrupted/failed stages are retained for diagnosis and
block claims that all storage is bounded; audit archives intentionally accumulate.
