# Changing an isola config

This is the field reference for editing an existing `.isola.toml` (adding a
service, wiring env, adjusting the proxy). First-time setup (discovering a repo's
processes from scratch) is the separate **isola-init** skill.

> **isola does not hot-reload.** A config change is ignored until you restart the
> affected processes. After editing `.isola.toml`:
>
> ```bash
> isola down && isola up          # services (picks up command/env/service changes)
> isola proxy stop && isola up     # ALSO needed after any [proxy] change
> ```
>
> The shared proxy keeps the config it started with; `isola up` warns when the
> running proxy differs from the file.

## `[services.<name>]`

| Field        | Type         | Required | Notes |
| ------------ | ------------ | -------- | ----- |
| `command`    | string       | yes      | Start command; runs via `sh -c` in `dir` |
| `setup`      | string       | no       | Runs before `command` on each `up` (deps, migrations), in `dir` with the service's env. Keep idempotent; a failure blocks the service |
| `dir`        | string       | no       | Working dir relative to the worktree root (default: root) |
| `port_range` | `{min, max}` | no       | Per-branch port pool. Omit for a background process |
| `proxy_port` | int          | no       | Proxy listen port. Needs a `port_range`; omit both for a background process |
| `env`        | table        | no       | Per-service env; values may use `${...}` (below). There is no global `[env]` |
| `env_file`   | string       | no       | Env-file name relative to `dir` (overrides `[env_file]` `path`); `""` opts the service out |

A service is one of three shapes, by which port fields it sets:

- **Web** (`port_range` + `proxy_port`): gets `$PORT`, published at `<slug>.<project>.localhost:<proxy_port>`.
- **Internal** (`port_range` only): gets `$PORT`, reachable by siblings via `${services.<name>.direct_url}`, no browser URL.
- **Background** (neither): a worker/queue/cron. Runs and is managed (env, logs, `setup`, reconcile) with no `$PORT`, no route, no URL. Don't invent a port for something that never serves HTTP.

**Make the command bind `$PORT`.** A service that ignores it looks `running` on a
port nothing answers. Extend the existing script rather than replacing it
(`npm run dev -- --port $PORT`). **pnpm caveat:** `pnpm run dev -- --port $PORT`
does *not* work — pnpm inserts its own `--` and the flag is swallowed; use
`npx vite --port $PORT` or read `$PORT` in the tool's own config
([#9](https://github.com/cyucelen/isola/issues/9)). Use the repo's actual run
command; don't hand-write one when a script exists.

### Env references

In a service's `env` table, `${...}` resolves to this worktree's real values:

| Reference | Resolves to |
| --------- | ----------- |
| `${accessories.<name>.url}`     | the accessory's connection string |
| `${services.<name>.url}`        | a sibling's proxy URL (browser links; needs `*.localhost` to resolve) |
| `${services.<name>.direct_url}` | a sibling's `http://127.0.0.1:<port>` (server-side calls; no DNS, every OS) |
| `${services.<name>.port}`       | a sibling's allocated backend port |
| `${proxy.ca_cert}`              | isola's dev CA path (HTTPS only; empty otherwise) |

The same values are also exposed as env vars without a reference: `PORT`,
`ISOLA_BRANCH`, `ISOLA_BRANCH_SLUG`, `ISOLA_SERVICE`, and per sibling
`ISOLA_<SERVICE>_URL` / `ISOLA_<SERVICE>_DIRECT_URL` / `ISOLA_<SERVICE>_PORT`. A
bare `$` is left literal (so `p$ssw0rd` survives). See `references/dev.md` for
`.url` vs `.direct_url`.

## `[env_file]` (top-level)

isola writes each service's resolved env into a file the app reads, so dotenv
loaders/Prisma/Vite get the isolated values too. Fields (all optional): `enabled`
(default `true`; `false` stops writing), `create` (default `false`, so isola only
updates an existing file; `true` creates it), `path` (default `.env`, relative to
each service's `dir`). Ephemeral built-ins (`PORT`, `ISOLA_*`) are excluded from
the file.

## `[accessories.<name>]`

Per-worktree Postgres or Redis. Full field reference, the `name` collision rules,
and the discovery guidance are in **references/accessories.md** — read that before
adding or editing one.

## `[proxy]` (top-level)

One machine-wide daemon; these keys are per project. Fields: `enabled` (default
`true`; `false` opts this project out of auto-start), `https` (default `false`),
`auto_trust` (default `true`; with `https`, installs isola's CA on the first
interactive `up`). Enabling `https` needs a CA-trust step for non-browser clients
(Node/Bun especially) — see **references/dev.md** and `${proxy.ca_cert}`. Remember
the restart note above applies to every `[proxy]` edit.

## `[worktrees."<branch>"]` (top-level)

Per-branch overrides: `services.<svc>.command`, `services.<svc>.port` (pin a fixed
port within the range, e.g. keep `main` stable), and `services.<svc>.env`.

```toml
[worktrees.main]
services.web.port = 3100

[worktrees."feature/auth"]
services.api.command = "go run ./cmd/server -config auth"
services.api.env = { DEBUG = "1" }
```

## `project` / `copy_files` / `setup` (top-level, above any `[section]`)

- `project` — the repo's machine-wide name (namespaces proxy URLs and Redis
  ownership). Defaults to the main worktree's slugified directory name; must be a
  DNS label (lowercase, digits, hyphens). Set it only to override or to resolve a
  clash with another repo of the same name.
- `copy_files` — globs copied from the main worktree into each new worktree on
  `up`, never overwriting (default `[".env"]`; `[]` disables). This is how a
  worktree gets files git leaves behind because they're gitignored.
- `setup` — a repo-root command run once per worktree on each `up`, at the
  worktree root, **after** accessories and **before** any service's `setup`/
  `command`. The whole-worktree counterpart to a service's `setup`: use it for a
  step not tied to one service (e.g. a root `pnpm install` whose `prepare` script
  installs git hooks, generating a shared client). Gets `ISOLA_BRANCH`,
  `ISOLA_BRANCH_SLUG`, accessory URLs, and `${proxy.ca_cert}` but no `$PORT`/
  `ISOLA_SERVICE`. Keep it idempotent (runs every `up`); a non-zero exit aborts
  that worktree's `up`. Chain with `&&`.

The exhaustive human-facing reference is
[docs/configuration.md](https://github.com/cyucelen/isola/blob/main/docs/configuration.md).
