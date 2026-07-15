# Per-worktree databases (accessories)

An **accessory** is an isolated stateful dependency isola provisions per
worktree, so migrations, seed data, and destructive tests on one branch never
touch another. isola connects to your **existing** server (it never starts,
stops, or otherwise manages the server) and exposes a connection string a
service references as `${accessories.<name>.url}`. Two built-in kinds ship today:
`postgres` and `redis`.

Each accessory is one `[accessories.<name>]` table. The `kind` field selects the
driver; the remaining fields are driver-specific.

## How a service opts in

There is **no auto-injected variable**. A service gets the connection string only
when it references it explicitly:

```toml
[services.api.env]
DATABASE_URL = "${accessories.database.url}"
```

The resolved URL is delivered to the service **process** and (per `[env_file]`)
written into its **env file**, so dotenv and ORM tools that read the file get the
same isolated value.

## postgres

Each worktree gets its own database, physically cloned from a seeded template
over the wire protocol (pure-Go pgx driver, no `psql` needed).

```toml
[accessories.database]
kind       = "postgres"                                     # driver
server_url = "postgres://postgres@localhost:5432/postgres"  # existing server + maintenance db
clone_from = "myapp_dev"                                    # seeded template copied per worktree
name       = "myapp_${ISOLA_BRANCH_SLUG}"                   # per-worktree database name
# url      = "postgres://app:app@localhost:5432/${db}"      # optional connection-string override
```

- **`server_url`** (required): the existing server plus a **maintenance database**
  the configured role connects to in order to run `CREATE`/`DROP DATABASE`
  (usually `postgres`). The role needs the **CREATEDB** privilege (or superuser),
  and must own `clone_from` (or be superuser) to clone it. It must be a
  `postgres://` URL, since the injected connection string is derived from it by
  swapping the database; a libpq keyword/value DSN is rejected unless you also
  set `url`.
- **`clone_from`** (required): the seeded template database copied for every
  worktree. Keep it seed-only (schema + fixtures) and don't run services against
  it.
- **`name`** (required): the per-worktree database name. Supports `${VAR}`
  (typically `${ISOLA_BRANCH_SLUG}`; also `${ISOLA_BRANCH}` and any process env
  var). The resolved value must be a legal Postgres identifier (<= 63 bytes; no
  quotes, backslashes, or control chars) and must **not** equal `clone_from` or
  the maintenance database, so isola never provisions on top of shared state (a
  branch `dev` with `clone_from = "myapp_dev"` is rejected).
- **`url`** (optional): overrides the exposed connection string. Supports `${db}`,
  which expands to the resolved `name`. Use it when the app connects as a
  different role/host than the admin `server_url`, or when `server_url` is a DSN.
  Unset, the injected URL is `server_url` with its database swapped for `name`.

Behavior:

- **Provision (`up`)**: creates `name` with `CREATE DATABASE <name> TEMPLATE
  <clone_from>` if absent; reuses it as-is if present. Before cloning, isola
  auto-terminates lingering connections to `clone_from` (a physical-copy needs a
  quiescent source), so the template need not be idle.
- **Reset**: `DROP DATABASE IF EXISTS <name> WITH (FORCE)` then re-clone from
  `clone_from`, returning it to the template baseline.
- **Drop**: `DROP DATABASE IF EXISTS <name> WITH (FORCE)`. Guarded: isola refuses
  to drop `clone_from` or the maintenance database, and only drops a name it
  recorded creating.

## redis

Each worktree gets its own numbered logical database on the shared server,
allocated collision-free. There is no template, so the baseline is an empty DB.

```toml
[accessories.cache]
kind       = "redis"                    # driver
server_url = "redis://localhost:6379"   # existing server (rediss:// also works)
# databases = 16                        # logical DBs the server exposes (default 16)
```

- **`server_url`** (required): the existing Redis server, a `redis://` (or
  `rediss://`) URL.
- **`databases`** (optional, default `16`): how many logical DBs the server
  exposes (indices `0..databases-1`); match your server's `databases` directive.
  This caps how many worktrees can hold a DB at once.

Allocation and ownership:

- Each worktree is assigned one logical DB index. isola writes an owner marker
  key (`__isola_owner__`) **inside** that DB, valued `<project>:<branch-slug>`
  (project-qualified, so two repos sharing a server never collide). Assignment
  hashes the owner id to a starting index and linear-probes for a free/owned DB,
  so it needs no central registry. A worktree keeps its DB across `up`/`down`.
- The injected `${accessories.cache.url}` is `server_url` with the DB index as
  its path, e.g. `redis://localhost:6379/3`.
- When all `databases` are in use, provisioning fails with a message to raise the
  server's `databases` and the config value. Single-DB only (not Redis Cluster).

Behavior:

- **Provision (`up`)**: allocates (or reuses) the DB; does not flush data.
- **Reset**: `FLUSHDB` on the worktree's DB, then re-writes the owner marker.
- **Drop**: `FLUSHDB`, but only if the DB is still owned by this worktree; a slot
  already reassigned to another worktree is left alone.

## Lifecycle

- **On `isola up`**: every accessory is brought up (created/allocated if absent,
  reused if present) before its services start, and its URL is exposed to any
  service that references it. If an accessory fails to come up, isola **warns and
  starts services anyway** without that variable, so the app falls back to its
  own config; watch for it hitting the wrong database. Re-run `isola up` to retry.
- **On `isola down`**: services stop; the database or logical DB is kept for next
  time.
- **On worktree removal**: isola reconciles automatically (on the next `isola up`
  and on the shared proxy's ~30s timer) and drops the databases it provisioned.
  `isola down --prune` forces the same teardown now; `isola destroy` tears down
  the current worktree only. None of these ever drop `clone_from` or the server
  database.

## Manage out of band

`isola accessory <verb> [name]` acts on the **current worktree** (positional
`name` targets one accessory; omit it for all). Cloning can be a large physical
copy, so each op has a generous 10-minute timeout.

```bash
isola accessory ls               # every worktree x accessory: KIND, RESOURCE, PROVISIONED
isola accessory up               # bring up this worktree's accessories (reuse existing); prints the URL
isola accessory reset            # postgres: drop + re-clone template; redis: FLUSHDB
isola accessory drop             # drop this worktree's resources and forget their state
isola accessory reset database   # act on a single accessory by name
```

`ls` lists every worktree even before provisioning; `up`/`reset`/`drop` act only
on the worktree you run them in.

## Preparing a template (postgres)

`CREATE DATABASE ... TEMPLATE` is a physical copy that needs a quiescent source,
so keep `clone_from` a seed-only database you don't run against (isola terminates
lingering connections before cloning, but don't point running services at it).
Seed it once (schema + fixtures); each worktree clones from it. To refresh every
worktree, re-seed the template and run `isola accessory reset`.
