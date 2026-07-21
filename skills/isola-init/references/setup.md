# Configuring isola for a repository

Configure isola for **this** repository and verify it. Work from what the repo
actually uses; don't assume a language, framework, or OS. Full field reference:
[Configuration Reference](https://github.com/cyucelen/isola/blob/main/docs/configuration.md).

## 1. Install the isola CLI if missing

Check whether it's already installed:

```bash
command -v isola && isola version
```

If that prints nothing, install it by following [install.md](install.md)
(Homebrew on macOS, `.deb`/`.rpm`/AUR on Linux, or `go install`), then re-run the
check. Don't proceed until `isola version` works. If the skill isn't active and
you reached this guide by URL, install.md is alongside it at
https://github.com/cyucelen/isola/blob/main/skills/isola-init/references/install.md

## 2. Discover the project and its environment

List every long-running process you'd normally start by hand: a web app, an API,
a worker, etc. Look at whatever the repo actually has: `package.json` scripts, a
`Makefile`, `Procfile`, `docker-compose.yml`, `mix.exs`, `Cargo.toml`,
`manage.py`, `Gemfile`, `go.mod`. There may be one service or several. Also note
how each is started and what stateful dependencies it needs (a database, a cache)
and how those actually run on *this* machine. Work it out from the repo and the
system in front of you; don't assume a language, framework, or tool.

## 3. Create and edit `.isola.toml`

Run `isola init`, then define one `[services.<name>]` per process:

```toml
[services.web]
command    = "npm run dev"                # runs via `sh -c`
dir        = "apps/web"                    # relative to the repo root ("" = root)
port_range = { min = 3100, max = 3199 }    # a stable per-branch port lives here
proxy_port = 3000                          # the URL you hit: <branch>.<project>.localhost:3000
```

- **Use the project's own run command; don't invent one.** If `package.json` has
  `"dev": "vite"`, the service `command` is `npm run dev` (use the repo's package
  manager: pnpm/yarn/bun), taken from `package.json` scripts, `Procfile`,
  `Makefile`, or compose. Don't assume a stack, and don't hand-write a raw
  `npx vite …` when a script already exists.
- A service that serves HTTP must **listen on the injected `$PORT`**. Many dev
  servers already read `PORT` from the environment (Node's `process.env.PORT`,
  Rails, Django, etc.) and need no change. When one needs a flag, **extend the
  existing script** rather than replacing it: `npm run dev -- --port $PORT`
  (everything after `--` passes through to the underlying tool). **pnpm caveat:**
  `pnpm run dev -- --port $PORT` does *not* work: pnpm inserts its own `--`, the
  flag is swallowed, and the tool silently falls back to its default port (this
  bit Vite in testing, [#9](https://github.com/cyucelen/isola/issues/9)). Under
  pnpm, run the tool directly (`npx vite --port $PORT`) or make it read `$PORT`
  from its own config (Vite: `server: { port: Number(process.env.PORT) }` in
  `vite.config.ts`). A service that ignores `$PORT` looks `running` on a port
  nothing answers, so always verify with a real request (step 6). Only hand-write
  a raw command when the project has no script for it.
- **A background process that doesn't listen** (a worker, queue consumer, cron
  loop) omits **both** `port_range` and `proxy_port`. isola still runs and manages
  it (env, logs, `setup`, reconcile), but allocates no `$PORT` and gives it no URL.
  Don't invent a port for something that never serves HTTP.
- If a fresh worktree needs a prep step before the app runs (a new working dir
  has no `node_modules`, un-run migrations, or ungenerated code), add
  `setup = "..."` to the service (e.g. `setup = "npm install"`). It runs before
  `command` on each `up`, in the service's `dir` with its env (so migrations see
  the per-worktree `DATABASE_URL`). Keep it idempotent; a failed setup blocks
  that service.
- Give each listening service a unique `port_range` and `proxy_port`.
- `project` defaults to the repo's directory name; set it at the top only to
  override that or resolve a clash with another repo of the same name.
- **Gitignored files are copied into each new worktree** on `up`. `copy_files`
  (a top-level key, default `[".env"]`) lists globs copied from the main worktree
  (git omits gitignored files from new worktrees), never overwriting; `[]`
  disables it. Keep it above the `[sections]`. This is how a worktree gets a
  `.env` that lives only in the main checkout.
- **Per-branch overrides** live in `[worktrees."<branch>"]`: override a service's
  `command`, pin a fixed `port` (within its `port_range`), or add `env`. Useful
  for keeping `main` on a stable port.

## 4. Wire each service's environment

Declare env **per service** (there is no global `[env]`). Values can reference
isola-provided sources with `${...}`:

```toml
[services.web.env]
API_URL        = "${services.api.url}"          # sibling's proxy URL (browser)
API_DIRECT_URL = "${services.api.direct_url}"   # sibling's loopback URL (server-side)
```

Reference namespace: `accessories.<name>.url`, `services.<name>.url`,
`services.<name>.direct_url`, `services.<name>.port`, and `proxy.ca_cert` (the
dev CA path, HTTPS only; see step 8). `services.<name>.url` routes through the
proxy (use for browser links); `services.<name>.direct_url` is a direct
`http://127.0.0.1:<port>` (use for server-side calls between services: no DNS,
works on every OS). isola delivers each service's env into its **process**
and (unless disabled) into its **env file**, so tools that read `.env` /
`.env.local` directly get the same isolated values. Only the explicit `${...}`
form is expanded; a bare `$` is left literal, so a value like `p$ssw0rd` survives
unchanged.

Control the env file with a top-level `[env_file]` block (all optional):
`enabled` (default `true`; set `false` to stop writing env files), `create`
(default `false`, so isola only updates a file that already exists; `true` creates
it), and `path` (default `.env`, relative to each service's `dir`). A single
service overrides the name with its own `env_file = "..."`, or opts out with
`env_file = ""`.

## 5. Per-worktree databases (accessories)

If the project uses Postgres or Redis, add an accessory so each worktree gets its
own isolated instance, and reference it from the services that need it:

```toml
[accessories.database]
kind       = "postgres"
server_url = "postgres://USER:PASS@HOST:PORT/postgres"   # existing server, maintenance db
clone_from = "myapp_dev"                                 # the dev database to copy per worktree
name       = "myapp_${ISOLA_BRANCH_SLUG}"

[services.api.env]
DATABASE_URL = "${accessories.database.url}"
```

**Discover the real connection; do not copy the example.** Find the connection
string the project already uses (`.env`/`.env.local`, ORM or framework config)
and work out how its database actually runs and is reachable on *this* machine.
Don't assume a stack: it might be docker-compose, a natively installed Postgres,
a shared dev server, or a managed/cloud instance. Don't assume a default port
either; confirm what is actually listening. A stray copy of `localhost:5432` can
silently hit a different server. `server_url` must point at the running server's
maintenance database (one the configured user can connect to and use to create
and drop databases, usually `postgres`); `clone_from` is the existing dev database
to copy. isola terminates lingering connections to `clone_from` automatically
before cloning, so it need not be idle. If the app connects as a different
role/host than the admin `server_url`, set `url = "postgres://app:app@HOST:PORT/${db}"`
(`${db}` expands to the resolved `name`).

For **Redis** the shape is simpler (no template; each worktree gets a numbered
logical DB, and the injected URL is the server URL with that index as its path,
e.g. `redis://host:6379/3`):

```toml
[accessories.cache]
kind       = "redis"
server_url = "redis://localhost:6379"   # rediss:// works too
# databases = 16                         # set only if the server exposes other than 16 DBs
```

isola connects to your existing server (it never manages one). If the project
needs a stateful service isola doesn't support yet (MySQL, MongoDB, a queue, …),
don't fake it: see
[Writing a new accessory](https://github.com/cyucelen/isola/blob/main/docs/writing-an-accessory.md)
or open an issue.

## 6. Start and verify

```bash
isola up            # starts this worktree's services and the shared proxy
isola ls            # ports, status, and the <branch>.<project>.localhost URLs
```

The setup is verified when **all** of these hold:

1. `isola doctor` passes every check (config file with the right service count,
   each `proxy_port` available, state healthy, worktrees consistent).
2. `isola ls` shows every service `running`. Listening services have a port and a
   `<branch>.<project>.localhost:<proxy_port>` URL; a background process shows
   `running` with no port or URL, which is expected.
3. Each service URL actually answers. Don't trust `running` alone: a service can
   report `running` on a port nothing listens on when its command ignores
   `$PORT` (step 3). Confirm with a real request (skip this for a background
   process, which serves nothing; check `isola logs` instead), e.g.:

   ```bash
   curl -sSf -o /dev/null -w '%{http_code}\n' http://<branch>.<project>.localhost:<proxy_port>
   ```

   A connection error or isola's own "service not running" page means the
   service isn't actually up; a normal status code (or a redirect) means it is.
   In a browser, Chrome and Firefox resolve `*.localhost` automatically.
4. The isolated `env` values landed. isola always injects them into the
   service **process**, so a service that reads its config from the environment
   just works. isola writes an **env file** only when one already exists (or you
   set `create = true` under `[env_file]`) — by default it does not create one.
   So: if the service relies on a `.env`/`.env.local` file, confirm that file
   exists in the worktree and every `${...}` is resolved to a real URL/port (not
   left literal); if it reads the environment directly, the working service is
   itself the confirmation.

Then add `.isola/` to `.gitignore` (per-clone state: ports, PIDs, logs, never
committed) and commit `.isola.toml` so new worktrees inherit the config:

```bash
grep -qxF '.isola/' .gitignore 2>/dev/null || echo '.isola/' >> .gitignore
git add .isola.toml .gitignore && git commit -m "Configure isola"
```

Committing is enough for local worktrees (`git worktree add` off a branch that has
`.isola.toml` gets a working setup); push only to share the config or to create
worktrees from the remote.

## 7. Automate new worktrees (git hook)

Install a `post-checkout` hook so every new worktree starts itself (`isola up` on
creation). Do it for this clone:

```bash
isola hooks install
```

The hook runs `isola up` only on a brand-new worktree and no-ops when there is no
`.isola.toml`, so it is safe to leave on. To share it with the team instead, run
`isola hooks install --shared` (it commits a `.githooks/` dir and sets
`core.hooksPath`; each clone runs that once). Running `--shared` after a local
install is harmless: git uses the shared hook and the local one goes dormant.
Remove anytime with `isola hooks uninstall`.

## 8. HTTPS (optional)

By default the proxy serves **HTTP**, which needs no certificates and no trust
setup. Only enable HTTPS if the app requires it (secure cookies, `crypto.subtle`,
etc.). Turn it on with a `[proxy]` table:

```toml
[proxy]
https = true
```

Then apply it. **isola does not hot-reload a running proxy or running services**,
so after changing `[proxy]` (or any config) you must restart, or the change is
silently ignored:

```bash
isola down && isola proxy stop && isola up
```

On the first HTTPS `isola up`, isola generates its own CA. With `auto_trust`
enabled (the default) it offers to trust it on an interactive terminal (one
`sudo` prompt); if you decline or the session is non-interactive (an agent/CI),
`up` still succeeds and prints a warning to run `isola trust` later. Setting
`auto_trust = false` opts out of that entirely, so `up` stays silent about
trust: you are expected to wire `${proxy.ca_cert}` (below) or run `isola trust`
yourself.

**The HTTPS gotcha:** isola's CA is a private root, so a client that isn't a
browser and isn't using the system trust store rejects the certificate until the
CA is trusted. This bites **server-side calls between your own services** (e.g. a
Node `fetch()` or Go `http.Get()` from `web` to `api` throws a TLS / `unable to
get local issuer` error) even though each endpoint looks fine in a browser. Do
**not** verify inter-service HTTPS with `curl -k`: the `-k` bypasses cert checks
and gives a false "it works."

To fix it without `sudo`, isola exposes the CA path as the reference
`${proxy.ca_cert}` (empty unless `https` is on). Wire it into whichever CA
variable the service's own runtime reads, in that service's `env` (isola does not
guess the runtime, so nothing is set unless you ask):

```toml
[services.web.env]                       # a Node/Bun service
NODE_EXTRA_CA_CERTS = "${proxy.ca_cert}" # additive: safe, keeps public TLS working

[services.api.env]                       # a Go/Python/curl-based service
SSL_CERT_FILE = "${proxy.ca_cert}"       # REPLACES the system bundle: only for a
                                         # service that never calls public HTTPS
```

`isola trust` (one `sudo`) installs the CA into the **system** store and is a
complement, not a full substitute: it covers browsers and runtimes that read the
system store (Go, curl, Ruby, ...). It is **not** enough for **Node or Bun**,
which ignore the OS trust store and use a bundled CA list — a Node service still
needs `NODE_EXTRA_CA_CERTS = "${proxy.ca_cert}"` even after `isola trust` (or run
Node >= 22 with `--use-system-ca`). macOS Python (certifi) is similar. So: use
`${proxy.ca_cert}` for Node/Bun (and macOS Python); `isola trust` handles the
rest and the browser.

Note: `isola trust` installs into the **system** store only. **Firefox** uses
its own store and will keep warning; use a Chromium browser or Safari, or import
the CA into Firefox manually.

If any check fails, see
[Troubleshooting](https://github.com/cyucelen/isola/blob/main/docs/troubleshooting.md).

## 9. Tell the user what's next

Finish by giving the user a short, readable summary of where things stand and
what they can do next. Tailor it to what you actually set up and keep it concise.
Cover:

- **It's running.** The services and their URLs (from `isola ls`).
- **What you committed and why.** `.isola.toml` is committed so every new
  worktree inherits the setup; `.isola/` is gitignored (per-clone state).
- **Try it yourself.** Create an isolated worktree and watch it come up on its
  own ports, URLs, and database:

  ```bash
  git worktree add ../<name> -b <name>
  ```

  With the git hook installed it starts on its own; otherwise `cd ../<name> &&
  isola up`. Remove it with `git worktree remove ../<name>` and isola tears down
  the leftover services and database.
- **HTTPS, if they want it.** Offer to turn it on. You can do the config yourself:
  set `https = true` under `[proxy]` and restart (`isola down && isola proxy stop
  && isola up`). Then guide the one manual step you can't do for them: trusting
  the dev CA with `isola trust` (a single `sudo` prompt), or accepting the prompt
  on the first interactive `up`. Tell them to **restart the browser afterwards**:
  browsers read the system trust store at startup, so one already open when the CA
  was installed keeps warning until it is fully quit and reopened. For Node/Bun
  services that call each other over HTTPS, also add
  `NODE_EXTRA_CA_CERTS = "${proxy.ca_cert}"`. Don't run `sudo` on their behalf;
  walk them through it.
- **Day to day.** `isola ls` (status + URLs), `isola dash` (TUI), `isola down`
  (stop this worktree), `isola destroy` (stop + drop this worktree's database).
