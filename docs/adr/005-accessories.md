# ADR-005: Accessories (Per-Worktree Stateful Dependency Isolation)

## Status

Accepted.

> **Update:** the `inject` field described below was later removed. An accessory
> no longer auto-injects a keyed env var; instead it exposes its connection
> string, which a service references explicitly as `${accessories.<name>.url}`
> in its own `env`. See the [Configuration Reference](../configuration.md#accessoriesname).

> **Update:** the safety rule "validate `name` is ≤ 63 bytes" below rejected long
> branches outright, which left a worktree with no database and every dependent
> service down. Names are now *fitted* to each resource's own budget
> (`WorktreeInfo.ExpandWithin` + `internal/slug`): the middle of the branch slug is
> elided and a hash of the untruncated slug appended, so branches sharing a long
> prefix cannot collapse onto one database. The length check remains as a guard on
> names isola did not derive. See the
> [Configuration Reference](../configuration.md#accessoriesname).

## Context

isola already gives each git worktree its own isolated *processes* (ADR-001),
*ports* (ADR-002), and *URLs* (ADR-003). But worktrees still share the
stateful dependencies those processes talk to — most importantly the database.
Two branches running against the same Postgres database means a migration on
one branch corrupts the other, seed data collides, and destructive tests are
unsafe. This defeats the purpose of per-worktree isolation.

We want to extend the per-worktree model beyond ports to **full environment
isolation**: each worktree gets its own database (and later its own Redis,
etc.), provisioned automatically, cloned from a known-good source, and torn
down when the worktree goes away. We call these isolated stateful dependencies
**accessories**.

Requirements and constraints that shaped the design:

1. **isola does not manage server lifecycle.** Users already run a local
   Postgres. isola connects to the *existing* server and manages *databases*
   within it — it does not install, start, or supervise the server. This
   mirrors how isola spawns dev servers rather than replacing the user's
   toolchain (ADR-001).
2. **Provision lazily, mirror the port model.** A worktree's database should be
   created on `isola up` (reuse if already present) and dropped on
   `isola down --prune`, exactly as ports are assigned lazily and released when
   a worktree is pruned (ADR-002, ADR-004).
3. **Clone from a seeded source.** The per-worktree database is a copy of a
   local template database (schema + seed data), so a fresh worktree starts
   ready to run, not empty.
4. **Injected into services as env vars.** The whole point is that services
   pick up the isolated database transparently — via `DATABASE_URL` or similar
   — with no per-branch code changes.
5. **Extensible without shared-code churn.** Adding a new accessory kind
   (redis, mysql, …) should not require editing config parsing, the manager, or
   any central switch. And we want a path to *third-party* accessory drivers
   later without rewriting v1.

Options considered for the isolation mechanism (v1, Postgres):

- **Separate Postgres server per worktree** (e.g. a container each) — heavy,
  reintroduces the Docker dependency ADR-001 rejected, slow to provision.
- **Schema-per-worktree in a shared database** — cheap, but leaks across
  worktrees (shared extensions, roles, `search_path` foot-guns) and doesn't
  match how most apps connect (they expect a whole database).
- **Database-per-worktree in the shared server** via
  `CREATE DATABASE ... TEMPLATE` — a physical copy of a seeded template,
  fully isolated at the database boundary, no extra server. Chosen.

## Decision

Introduce a first-class **accessory** concept: a driver-backed, per-worktree
stateful dependency, provisioned lazily and injected into services as
environment variables.

### Config surface

A new top-level `[accessories.*]` table in `.isola.toml`. Each accessory names
a `kind` (the driver discriminator) and carries driver-specific fields:

```toml
[accessories.database]
kind       = "postgres"                                       # driver discriminator (K8s-style). NOT "type" (a Go keyword)
server_url = "postgres://postgres@localhost:5432/postgres"    # existing server + doorway db, used for CREATE/DROP
clone_from = "myapp_dev"                                      # seeded source database copied per worktree
name       = "myapp_${ISOLA_BRANCH_SLUG}"                     # per-worktree database name; reuses runner.go ${VAR} expansion
inject     = "DATABASE_URL"                                   # env var injected into services
# url      = "postgres://app:app@localhost:5432/${db}"        # OPTIONAL connection-string override injected into services;
#                                                             # default is derived from server_url with the dbname swapped

[accessories.cache]
kind = "redis"                                                # future driver; fields owned by the redis driver
```

Field-name rationale (settled): `kind` not `type` (avoids the Go keyword and
matches Kubernetes vocabulary); `clone_from` not `template` (avoids collision
with the connection-`url`-template idea and reads as "the DB we copy from");
`inject` names the env var services receive; `url` is an optional override for
the injected connection string, defaulting to `server_url` with the database
name swapped for the provisioned `name`.

### Driver interface + registry

Accessory behavior lives behind an interface, implemented per kind, with
drivers self-registering by `kind` into a registry:

```go
// internal/accessory
type Accessory interface {
    Name() string
    Kind() string
    // Provision creates (or reuses) the per-worktree resource and returns a
    // driver-defined Handle plus the env vars to inject into services.
    Provision(ctx context.Context, wt WorktreeInfo) (Provisioned, error)
    // Drop tears down the resource identified by the persisted Handle. It needs
    // no live worktree, so it is safe on prune.
    Drop(ctx context.Context, handle map[string]string) error
}

// Reset is an optional capability: only Kinds with a Template can restore to a
// baseline, so it lives on a separate interface rather than the core one.
type Resettable interface {
    Reset(ctx context.Context, wt WorktreeInfo) (Provisioned, error)
}

// Provisioned carries what a driver produced: an opaque Handle (persisted for
// teardown) and the env vars to inject.
type Provisioned struct {
    Handle map[string]string
    Env    map[string]string
}
```

Each kind registers a factory keyed by its `kind` string. Adding a kind means
adding one file in `internal/accessory/` and one `Register("kind", factory)`
call — it touches no shared code. The core interface is deliberately just
`Provision`/`Drop`; `Reset` is optional so Kinds without a Template aren't forced
to fake a baseline.

Timeouts are owned by the callers (`manager`, `cmd`, prune), which wrap the
context before invoking a driver, so every Kind inherits a uniform deadline
rather than each re-implementing one.

### Deferred TOML decoding

So each driver owns its own config schema, config parsing decodes accessory
bodies lazily. Switch `internal/config/config.go` from `toml.Unmarshal` to
`toml.Decode` to obtain `toml.MetaData`, decode each `[accessories.*]` body as
a `toml.Primitive`, read only the shared `kind` field centrally, then hand the
`Primitive` + `MetaData` to the driver's factory, which calls
`MetaData.PrimitiveDecode` into its own struct. The config package never learns
a driver's fields.

### State

Generalize the `PortAssignments` pattern (ADR-004). Add per-accessory records
to `State`, keyed by (worktree, accessory), mutated under the existing
`WithLock`. Each record stores the `kind` and the driver's opaque `Handle`
(a `map[string]string`) — not a scalar id — so teardown never depends on
re-reading config and multi-resource drivers are supported. This record is the
authority for safe teardown: **isola only drops resources it holds a Handle
for.**

### Lifecycle hooks (the seams)

- **Provision on up:** `internal/process/manager.go` `StartServices` provisions
  each configured accessory for the worktree before starting services, and
  merges the returned env vars into each service's environment. Reuse if the
  record already exists.
- **Inject env:** the merged accessory env vars flow through the existing
  `internal/process/runner.go` `buildEnv`. The `name`/`url` `${VAR}` fields
  reuse `expandBraces` so substitution matches services byte-for-byte.
- **Teardown on prune:** `cmd/down.go` `pruneOrphanedState` drops accessories
  for orphaned worktrees (alongside the existing port-assignment cleanup),
  gated on the state record.

### Postgres driver (v1) specifics

- Strategy is **shared-server, database-per-worktree** — a driver-specific
  choice, not a global isola setting.
- Provision: `CREATE DATABASE <name> TEMPLATE <clone_from>`. Because
  `CREATE DATABASE ... TEMPLATE` is a *physical file copy* that requires a
  quiescent source (like built-in `template0`, which sets
  `datallowconn=false`), any live connection to `clone_from` makes it fail.
  Run `pg_terminate_backend` against `clone_from` as a safety net before the
  copy. Treat `clone_from` as a clone-source, never a run-target.
- Teardown: `DROP DATABASE <name> WITH (FORCE)` (Postgres 13+), which
  terminates lingering connections.
- Safety rules: validate `name` is a legal Postgres identifier (≤ 63 bytes,
  no quote/escape chars) before interpolating it into DDL; refuse a resolved
  `name` that equals `clone_from` or the `server_url` doorway database (a branch
  named `dev` with `clone_from = myapp_dev` would otherwise destroy the
  template); **never** drop `clone_from` or the doorway database; only drop
  databases named by a Handle in the state record.
- Connectivity: talk to Postgres over the wire protocol via the pure-Go `pgx`
  driver rather than shelling out to a `psql` client. A `psql` dependency would
  be a *runtime* dependency users must install and expose on `PATH`, which
  undercuts isola's single-binary promise (ADR-001); `pgx` is a *build-time*
  dependency compiled into the binary, so users install nothing extra (they
  still need a running Postgres *server* — their existing one). DDL uses the
  simple query protocol because Postgres forbids `CREATE`/`DROP DATABASE` under
  the extended (prepared-statement) protocol. Each operation opens one
  connection through a `conn`/`opener` seam, so the client is swappable without
  touching driver logic or tests.

### Redis driver

The second built-in kind, added with no changes to shared code (a new package +
`Register("redis", …)` + a blank import), validating the extensibility model.
Redis has no cheap "clone a database" primitive, so instead of a template it
assigns each worktree its own **numbered logical DB** (`redis://host:port/<n>`)
and injects that URL. Indices are allocated collision-free with the same
hash-plus-linear-probe idea as the port allocator, using an owner-marker key
inside each DB (which the owning worktree has to itself) as the coordination
store — so no central registry or interface change is needed for a driver that,
unlike Postgres, must allocate from a bounded pool. `Reset` (the optional
capability) and `Drop` run `FLUSHDB`; `Reset`'s baseline is empty rather than a
template. Trade-offs: capped at the server's logical-DB count (default 16) and
unusable on Redis Cluster, which supports only DB 0. Uses the pure-Go `go-redis`
client, mirroring the pgx choice.

### CLI

An `isola accessory` command group operates accessories out of band, with a
positional `[name]` to target one (default: all): `ls` (list resources and
state), `up` (bring up now, reusing any that exist), `reset` (restore to
baseline, only for resettable kinds), `drop` (tear down). The command noun
matches the domain term — not `db`, which is Postgres-specific and would misread
for a cache.

### Extensibility staging

- **Now:** in-tree Go drivers (postgres and redis; later mysql, etc.). Built-in
  kinds use bare names (`postgres`, `redis`).
- **Later:** an external-executable driver protocol (Terraform-provider style)
  so third parties ship drivers as separate binaries, addressed by namespaced
  `kind = "vendor/name"`. The `Accessory` interface and registry are designed
  so this drops in behind the same seams without a rewrite — bare names stay
  reserved for built-ins, namespaced names route to the external protocol.

## Consequences

### Positive

- **True per-worktree isolation.** Migrations, seed data, and destructive tests
  on one branch can't affect another.
- **Zero new runtime dependencies.** Reuses the user's existing Postgres; no
  Docker, no supervised server (consistent with ADR-001).
- **Fast provisioning.** `CREATE DATABASE ... TEMPLATE` is a local file copy,
  far cheaper than spinning up a server per worktree.
- **Transparent to services.** Existing apps pick up the isolated database via
  the injected env var with no code changes.
- **Open to extension, closed to churn.** New kinds are additive; the eventual
  external-driver protocol is anticipated by the interface and namespacing.

### Negative

- **`clone_from` must be quiescent.** A live connection to the template makes
  provisioning fail; we mitigate with `pg_terminate_backend`, but a busy
  template is a foot-gun users must understand.
- **DDL string interpolation.** Postgres identifiers can't be parameterized in
  `CREATE`/`DROP DATABASE`, so `name` is interpolated into SQL. This demands
  strict identifier validation (the ≤ 63-byte legal-identifier check) to stay
  injection-safe.
- **Version floor.** `DROP DATABASE ... WITH (FORCE)` requires Postgres 13+.
- **State becomes load-bearing for destructive ops.** Teardown trusts the state
  record; a corrupted or hand-edited record could strand or (guarded against)
  mis-target databases. The "only drop what we recorded creating, never touch
  `clone_from`/server db" rules are the backstop.
- **`toml.Decode` migration.** Moving off `toml.Unmarshal` to get `MetaData`
  touches shared config-loading code once, up front.
