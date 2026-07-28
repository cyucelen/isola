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

Derive resource names with `WorktreeInfo.ExpandWithin(template, maxBytes, extra)`
rather than `Expand` whenever the thing you are naming has a length limit — pass
your own budget, since limits differ per resource (a Postgres identifier is 63
bytes, a DNS label 63, a path component 255). It shortens the branch slug with
`slug.Fit`, which hashes the untruncated slug so two branches sharing a long
prefix cannot collapse onto one resource, and reports an error rather than
handing back a name that does not fit.

See [`internal/accessory`](../internal/accessory) for the interface and the
built-in `postgres`/`redis` drivers.
