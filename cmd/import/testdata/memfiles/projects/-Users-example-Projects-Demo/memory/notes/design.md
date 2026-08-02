# Durable design note

The service keeps source history append-only so maintenance can be audited and retried safely.

Unchanged native memory files must not be embedded again during the daily synchronization pass.
