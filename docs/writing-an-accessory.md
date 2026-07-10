# Writing a new accessory

Accessories are pluggable behind a small driver interface, so a new kind (MySQL,
MongoDB, a message queue, ...) is a self-contained package. To add one:

1. Implement the `Accessory` interface (`Name`, `Kind`, `Provision`, `Drop`; plus
   the optional `Resettable` if your kind has a template to reset to). `Provision`
   creates the per-worktree resource and returns a connection URL (which services
   reference as `${accessories.<name>.url}`) plus an opaque handle isola persists
   for teardown.
2. Register it from an `init()` with `accessory.Register("yourkind", New)`.
3. Blank-import your package so the registration runs.

See [`internal/accessory`](../internal/accessory) for the interface and the
built-in `postgres`/`redis` drivers.
