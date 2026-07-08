# Per-worktree databases (accessories)

An **accessory** is an isolated stateful dependency provisioned per worktree.
v1 supports Postgres: each worktree gets its own database, physically cloned
from a seeded template, so migrations, seed data, and destructive tests on one
branch never touch another. isola connects to your **existing** Postgres server
(via the pure-Go pgx driver — no `psql` needed) and only manages databases
within it; it never starts or stops the server.

## Configure

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

## Lifecycle

- **On `isola up`**: each accessory is provisioned (created from `clone_from` if
  absent, reused if present) and its `inject` var (e.g. `DATABASE_URL`) is set in
  every service's environment. If provisioning fails, services are not started.
- **On `isola down --prune`** (after `git worktree remove`): databases isola
  recorded creating are dropped. It never drops `clone_from` or the server db.

## Manage out of band

```bash
isola accessory ls               # show each worktree's accessory + provisioned resource
isola accessory provision        # provision the current worktree's accessories now (reuse if present)
isola accessory reset            # drop + re-clone from the template (fresh baseline)
isola accessory drop             # drop the current worktree's provisioned resources
isola accessory reset primary    # act on a single accessory (positional name)
```

## Preparing a template

`CREATE DATABASE ... TEMPLATE` is a physical copy that requires a quiescent
source, so keep `clone_from` a seed-only database you don't run against
(isola terminates lingering connections to it before cloning). Seed it once
(schema + fixtures), then let each worktree clone from it.
