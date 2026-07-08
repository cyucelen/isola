# Per-worktree databases (accessories)

An **accessory** is an isolated stateful dependency created per worktree, so
migrations, seed data, and destructive tests on one branch never touch another.
isola connects to your **existing** server (never starting or stopping it) and
injects a connection string into services. Two built-in kinds ship today:

- **`postgres`** — each worktree gets its own database, physically cloned from a
  seeded template (via the pure-Go pgx driver, no `psql` needed).
- **`redis`** — each worktree gets its own numbered logical DB, allocated
  collision-free; `reset`/`drop` run `FLUSHDB`. Caps at the configured
  `databases` count (default 16; set it to match your server) and is not for
  Redis Cluster.

## Configure Postgres

Add an `[accessories.<name>]` table to `.isola.toml`:

```toml
[accessories.primary]
kind       = "postgres"                                     # driver
server_url = "postgres://postgres@localhost:5432/postgres"  # existing server + maintenance db
clone_from = "myapp_dev"                                    # seeded template copied per worktree
name       = "myapp_${ISOLA_BRANCH_SLUG}"                   # per-worktree database name
inject     = "DATABASE_URL"                                 # env var injected into services
# url      = "postgres://app:app@localhost:5432/${db}"      # optional injected-URL override (${db} = name)
```

- `server_url` must be a `postgres://` URL (used for CREATE/DROP and, by default,
  as the template for the injected connection string). If it is a libpq DSN,
  you must set `url` explicitly.
- The resolved `name` must be a legal Postgres identifier (≤ 63 bytes) and must
  **not** equal `clone_from` or the server's maintenance database — a branch
  named `dev` with `clone_from = "myapp_dev"` is rejected to protect the template.

## Configure Redis

```toml
[accessories.cache]
kind       = "redis"           # driver
server_url = "redis://localhost:6379"  # existing Redis server
inject     = "REDIS_URL"       # env var injected into services (e.g. redis://localhost:6379/2)
# databases = 16               # logical DBs to use (default 16; set to match your server)
```

Each worktree is assigned its own numbered logical DB. There is no template, so
`reset` flushes the worktree's DB back to empty. A worktree keeps the same DB
across `up`/`down`; `drop` frees it. This caps at `databases` worktrees and does
not work on Redis Cluster (single-DB only).

## Lifecycle

- **On `isola up`**: each accessory is brought up (created from `clone_from` if
  absent, reused if present) and its `inject` var (e.g. `DATABASE_URL`) is set in
  every service's environment. If it fails, isola warns and starts services
  anyway, just without that accessory's injected var.
- **On `isola down --prune`** (after `git worktree remove`): databases isola
  recorded creating are dropped. It never drops `clone_from` or the server db.

## Manage out of band

```bash
isola accessory ls               # show each worktree's accessory + its resource
isola accessory up               # bring up the current worktree's accessories (reuse if present)
isola accessory reset            # reset to baseline (postgres re-clones template; redis FLUSHDB)
isola accessory drop             # drop the current worktree's resources
isola accessory reset primary    # act on a single accessory (positional name)
```

## Preparing a template

`CREATE DATABASE ... TEMPLATE` is a physical copy that requires a quiescent
source, so keep `clone_from` a seed-only database you don't run against
(isola terminates lingering connections to it before cloning). Seed it once
(schema + fixtures), then let each worktree clone from it.
