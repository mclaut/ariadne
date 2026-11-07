# Maintenance failures and non-destructive recovery

The tray keeps failed backups visible until a successful backup completes.
Maintenance retries remain warnings; a newer progress event cannot hide an
unresolved failed stage. Each new failed attempt sends a notification, even if
its issue text matches the preceding attempt. The menu also keeps the current
issue visible. Notification command failures are logged and retried on polling.
Operating-system notification permissions and Focus settings still apply.

For an explicitly non-destructive repair run, set `ARIADNE_BACKUP_PRESERVE=1`
on the maintenance process. A full backup still requires independent recovery
verification. On success, the previous full generation is archived intact in
its managed `.retired-full-*` container; independent full recovery and snapshot
verification copies remain available, and legacy/native pruning is skipped.
The new verified full becomes the active keeper. This opt-in applies only to
that process and its children; the default one-full retention policy is unchanged.
Preserved repair evidence requires a separately authorized cleanup.
