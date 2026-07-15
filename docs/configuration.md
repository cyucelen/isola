# Configuration Reference

The `.isola.toml` file lives at the root of your git repository. This document
covers every block and field. For the common case, see the
[Quick Start](../README.md#quick-start) and [Accessories](../README.md#accessories)
sections in the README.

## `project`

This repo's name across the machine. It namespaces the shared proxy
(`<branch>.<project>.localhost`) and per-worktree Redis ownership, so multiple
isola repos coexist on one machine without colliding. Unset, it defaults to the
main worktree's directory name (slugified). Set it only to override that or to
resolve a clash with another repo of the same name. Must be a DNS label
(lowercase letters, digits, hyphens).

```toml
# Top-level key: must appear ABOVE any [section] header.
project = "myapp"
```

## `copy_files`

Git worktrees don't include gitignored files, so a fresh worktree has no `.env`.
On `isola up`, isola copies files matching these glob patterns from the **main
worktree** into each worktree, **never overwriting** a file that already exists
there. Defaults to `[".env"]`; set `copy_files = []` to disable.

```toml
# Top-level key: must appear ABOVE any [section] header.
copy_files = [".env", ".env.*", "config/local.yml"]
```

`copy_files` only seeds files into a fresh worktree and never overwrites. To
write a service's resolved env (accessory URLs, `${...}` refs, your `env`) into a
file the app reads, so file-reading tools get this worktree's isolated values,
see [`[env_file]`](#env_file).

## `[services.<name>]`

Define one or more services. Each worktree will run all defined services.

| Field        | Type         | Required | Description                                                                     |
| ------------ | ------------ | -------- | ------------------------------------------------------------------------------- |
| `command`    | string       | yes      | Shell command to start the service                                              |
| `setup`      | string       | no       | Command run before `command` on each `up` (deps, migrations); runs in `dir` with the service's env. Make it idempotent; failure blocks the service |
| `dir`        | string       | no       | Working directory relative to worktree root (default: root)                     |
| `port_range` | `{min, max}` | yes      | Port allocation range for this service                                          |
| `proxy_port` | int          | yes      | Port the reverse proxy listens on for this service                              |
| `env`        | table        | no       | Environment for this service; values may reference `${...}` (see below)         |
| `env_file`   | string       | no       | Env-file name for this service (relative to `dir`); overrides [`[env_file]`](#env_file) `path`; `""` opts out |

```toml
[services.frontend]
command = "pnpm run dev"
dir = "frontend"
port_range = { min = 3100, max = 3199 }
proxy_port = 3000

[services.frontend.env]
API_URL = "${services.backend.url}"
```

Env values can reference isola-provided sources with `${...}`:

| Reference                | Resolves to                                              |
| ------------------------ | -------------------------------------------------------- |
| `${accessories.<name>.url}`    | the accessory's connection string                     |
| `${services.<name>.url}`       | a sibling's proxy URL (`<slug>.<project>.localhost:<proxy_port>`); routes through the proxy, so needs `*.localhost` to resolve (use for browser links) |
| `${services.<name>.direct_url}` | a sibling's direct loopback URL (`http://127.0.0.1:<port>`); no DNS, no proxy (use for server-side calls between services) |
| `${services.<name>.port}`      | a sibling's allocated backend port                    |
| `${proxy.ca_cert}`             | path to isola's dev CA (HTTPS only; empty otherwise), e.g. `NODE_EXTRA_CA_CERTS = "${proxy.ca_cert}"` |

Plus the isola built-ins (`PORT`, `ISOLA_*`) and your shell. The per-sibling
built-ins mirror the references above: `ISOLA_<SERVICE>_URL`,
`ISOLA_<SERVICE>_DIRECT_URL`, and `ISOLA_<SERVICE>_PORT`. A bare `$` is left
literal, so a value like `p$ssw0rd` survives unchanged. There is no global
`[env]`; declare env per service.

> [!IMPORTANT]
> **Make your command bind the allocated `$PORT`.** isola injects the
> allocated port as the `PORT` environment variable, but your service must
> actually listen on it; otherwise it will start on its own default port and
> isola will report it as `running` on a port nothing is listening on.
>
> The reliable approach is to have your service read `$PORT` itself:
>
> - **Vite**: set `server.port` in `vite.config.ts`, e.g.
>   `server: { port: Number(process.env.PORT) || 5173 }`, or run
>   `command = "npx vite --port $PORT"`.
> - **Next.js**: `command = "next dev -p $PORT"`.
> - Most frameworks honor `PORT` out of the box (Rails, Django via
>   `0.0.0.0:$PORT`, etc.).
>
> **pnpm caveat:** `command = "pnpm run dev -- --port $PORT"` does **not** work.
> pnpm inserts its own `--` separator, producing `vite ... -- --port 3193`, and
> Vite treats everything after `--` as positional args, so `--port` is silently
> ignored and Vite falls back to `5173`. Use `npx vite --port $PORT`, or read
> `PORT` inside `vite.config.ts` as shown above. (See
> [#9](https://github.com/cyucelen/isola/issues/9).)

## `[env_file]`

Besides the process environment, isola writes each service's resolved env into an
env file the app reads, so tools that read the file directly (dotenv loaders,
Prisma, Vite, `docker-compose`) get this worktree's isolated values. The file
carries the service's `env` (expanded) plus the accessory keys; ephemeral
built-ins (`PORT`, `ISOLA_*`) are excluded.

| Field     | Type   | Required | Description                                                              |
| --------- | ------ | -------- | ------------------------------------------------------------------------ |
| `enabled` | bool   | no       | Write env into services' env files (default `true`)                      |
| `create`  | bool   | no       | Create the file if missing (default `false`: only update an existing one) |
| `path`    | string | no       | Default env-file name, relative to each service's `dir` (default `.env`)  |

```toml
[env_file]
enabled = true
create  = false
path    = ".env"
```

The file is resolved per service as `<dir>/<path>`; a service overrides the name
with its own `env_file`. In a monorepo this writes each app's isolated values
into its own file (`apps/web/.env`, `apps/api/.env`, …). Whether a tool reads the
process environment or the file, it gets the same values.

## `[accessories.<name>]`

Give each worktree its own isolated stateful dependency. isola brings it up on
`isola up` and drops it on `isola down --prune`, connecting to your existing
server (it never manages the server itself). It exposes a connection string that
a service references explicitly as `${accessories.<name>.url}` (there is no
auto-injected key). The fields depend on `kind`; two built-in kinds ship today.

**`kind = "postgres"`** clones a seeded template database per worktree:

| Field        | Type   | Required | Description                                                                                                       |
| ------------ | ------ | -------- | ----------------------------------------------------------------------------------------------------------------- |
| `kind`       | string | yes      | `postgres` (bare names are built-in; `vendor/name` is reserved for third-party drivers)                           |
| `server_url` | string | yes      | Existing server + maintenance database, used to create/drop. Must be a `postgres://` URL                          |
| `clone_from` | string | yes      | Seeded template database copied for each worktree (kept quiescent, never run against)                             |
| `name`       | string | yes      | Per-worktree database name; supports `${VAR}` (e.g. `${ISOLA_BRANCH_SLUG}`)                                        |
| `url`        | string | no       | Override for the exposed connection string; supports `${db}` (defaults to `server_url` with the database swapped) |

```toml
[accessories.database]
kind       = "postgres"
server_url = "postgres://postgres@localhost:5432/postgres"
clone_from = "myapp_dev"
name       = "myapp_${ISOLA_BRANCH_SLUG}"

# A service uses it:
[services.backend.env]
DATABASE_URL = "${accessories.database.url}"
```

> [!NOTE]
> The resolved `name` must be a legal Postgres identifier (≤ 63 bytes) and must
> not equal `clone_from` or the `server_url` database, so isola never creates
> or resets a worktree on top of the template.

**`kind = "redis"`** gives each worktree its own Redis logical database, allocated
collision-free and flushed on reset/drop:

| Field        | Type   | Required | Description                                             |
| ------------ | ------ | -------- | ------------------------------------------------------- |
| `kind`       | string | yes      | `redis`                                                 |
| `server_url` | string | yes      | Existing Redis server, e.g. `redis://localhost:6379`    |
| `databases`  | int    | no       | Number of logical DBs the server exposes (default `16`) |

```toml
[accessories.cache]
kind       = "redis"
server_url = "redis://localhost:6379"

# A service uses it:
[services.worker.env]
REDIS_URL = "${accessories.cache.url}"
```

> [!NOTE]
> Each worktree gets its own numbered logical DB (`redis://.../<n>`), so this caps
> at the configured `databases` count (set it to match your server) and is not
> for Redis Cluster (single DB only).
> `reset` and `drop` run `FLUSHDB` on the worktree's DB.

Manage accessories out of band with `isola accessory ls|up|reset|drop [name]`
(`up` brings up this worktree's accessories, reusing any that already exist). To
add a new accessory kind, see [Writing a new accessory](writing-an-accessory.md).

## `[proxy]`

A single machine-wide proxy serves every isola project, routing
`<branch>.<project>.localhost:<port>` to the right project's backend. It
auto-starts in the background on `isola up` and binds the union of every
registered project's `proxy_port`s. This block is optional; omit it to keep the
defaults. `enabled`/`https`/`auto_trust` are per project (each contributes its
ports and HTTPS preference), but the daemon itself is shared: `isola proxy stop`
stops it for every project.

| Field        | Type | Required | Description                                                                              |
| ------------ | ---- | -------- | ---------------------------------------------------------------------------------------- |
| `enabled`    | bool | no       | Auto-start the proxy on `isola up` (default `true`; set `false` to opt out)              |
| `https`      | bool | no       | Serve HTTPS with auto-generated certificates (default `false`)                           |
| `auto_trust` | bool | no       | With `https`, install isola's CA into the system trust store on the first interactive `up` (default `true`) |

```toml
[proxy]
enabled    = true
https      = false
auto_trust = true
```

The proxy runs until `isola proxy stop`; it is not stopped by `isola down`. You
can always run it manually with `isola proxy start` (foreground).

When `https` is on, isola generates its own CA and per-worktree certificates. So
browsers accept them, the CA has to be trusted. On the first HTTPS `isola up` in
a terminal, isola installs the CA for you (one password prompt, since it needs
sudo); it does this only once and only interactively, so a non-interactive `up`
(an agent, CI) never blocks, it just warns. If you decline the prompt, `up` still
succeeds and you can run `isola trust` later or click through the browser
warning. Set `auto_trust = false` to always install trust manually.

## `[worktrees."<branch>"]`

Per-worktree overrides. You can customize the command, fix a specific port, or
add extra environment variables.

```toml
[worktrees.main]
services.frontend.port = 3100       # Fixed port for main branch

[worktrees."feature/auth"]
services.backend.command = "go run ./cmd/server -config auth"
services.backend.env = { DEBUG = "1" }
```
