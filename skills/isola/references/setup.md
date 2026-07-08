# Setting up isola

isola runs each service defined in `.isola.toml` per git worktree, with
automatic port allocation and `*.localhost` reverse-proxy routing. It spawns
your existing dev commands directly (no Docker) and connects to your existing
Postgres for database isolation — it never manages a server itself.

## 1. Create the config

From the repository root:

```bash
isola init          # writes a starter .isola.toml
```

Then edit `.isola.toml`. Each `[services.<name>]` defines one dev server:

```toml
[services.frontend]
command    = "pnpm run dev"              # runs via `sh -c`; use $PORT
dir        = "frontend"                  # relative to the worktree root ("" = root)
port_range = { min = 3100, max = 3199 }  # each branch gets a stable port in this range
proxy_port = 3000                        # the proxy listens here for this service

[services.backend]
command    = "python manage.py runserver 0.0.0.0:$PORT"
dir        = "backend"
port_range = { min = 8100, max = 8199 }
proxy_port = 8000
```

Rules: `command` is required; port ranges must be positive and must not overlap
between services; each `proxy_port` must be unique.

## 2. Environment variables

isola injects these into every service: `PORT`, `ISOLA_BRANCH`,
`ISOLA_BRANCH_SLUG`, `ISOLA_SERVICE`, plus `ISOLA_<SERVICE>_PORT` and
`ISOLA_<SERVICE>_URL` for cross-service wiring. Add your own under `[env]` or
per service; values may reference `${VAR}` (e.g. `API_URL = "${ISOLA_BACKEND_URL}"`).

Git worktrees omit gitignored files, so a new worktree has no `.env`. On `isola
up`, isola copies files matching `copy_files` (default `[".env"]`) from the main
worktree into each worktree, never overwriting an existing one. It's a top-level
key, so put it above any `[section]`; set `copy_files = []` to disable:

```toml
copy_files = [".env", ".env.*"]
```

## 3. Optional: HTTPS

The proxy auto-starts on `isola up`. To make the auto-started proxy serve
HTTPS, set it in config; then trust the generated CA once:

```toml
[proxy]
https = true   # auto-generates a local CA + certs
```

```bash
isola trust                 # install the CA so browsers stop warning
```

(Or run one manually: `isola proxy start --https`.)

## 4. Optional: per-worktree databases

Add an `[accessories.*]` table to give each worktree its own database cloned
from a seeded template. See `references/databases.md`.

## 5. Verify

```bash
isola doctor   # checks config and environment health
```
