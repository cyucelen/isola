# Configuring isola for a repository

Configure isola for **this** repository and verify it. Work from what the repo
actually uses; don't assume a language, framework, or OS. Full field reference:
[Configuration Reference](https://github.com/cyucelen/isola/blob/main/docs/configuration.md).

## 1. Install the isola CLI if missing

Check whether it's already installed:

```bash
command -v isola && isola version
```

If that prints nothing, install it by following
[install.md](install.md) (Homebrew on macOS, `go install`, or from source), then
re-run the check. Don't proceed until `isola version` works.

## 2. Discover the dev processes

List every long-running process you'd normally start by hand: a web app, an API,
a worker, etc. Look at whatever the repo actually has: `package.json` scripts, a
`Makefile`, `Procfile`, `docker-compose.yml`, `mix.exs`, `Cargo.toml`,
`manage.py`, `Gemfile`, `go.mod`. There may be one service or several.

## 3. Create and edit `.isola.toml`

Run `isola init`, then define one `[services.<name>]` per process:

```toml
[services.web]
command    = "npm run dev"                # runs via `sh -c`
dir        = "apps/web"                    # relative to the repo root ("" = root)
port_range = { min = 3100, max = 3199 }    # a stable per-branch port lives here
proxy_port = 3000                          # the URL you hit: <branch>.<project>.localhost:3000
```

- Each command **must bind the injected `$PORT`** (adapt per framework: `next dev
  -p $PORT`, `server.port` in `vite.config.ts`, `0.0.0.0:$PORT` for Django, etc.).
  A service that ignores `$PORT` will look `running` on a port nothing answers.
- Give each service a unique `port_range` and `proxy_port`.
- `project` defaults to the repo's directory name; set it at the top only to
  override that or resolve a clash with another repo of the same name.

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
dev CA path, HTTPS only; see step 7). `services.<name>.url` routes through the
proxy (use for browser links); `services.<name>.direct_url` is a direct
`http://127.0.0.1:<port>` (use for server-side calls between services: no DNS,
works on every OS). isola delivers each service's env into its **process**
and (unless disabled) into its **env file**, so tools that read `.env` /
`.env.local` directly get the same isolated values. A service can set its own
`env_file = "..."` (relative to `dir`); the default is `.env`.

## 5. Per-worktree databases (accessories)

If the project uses Postgres or Redis, add an accessory so each worktree gets its
own isolated instance, and reference it from the services that need it:

```toml
[accessories.database]
kind       = "postgres"
server_url = "postgres://postgres@localhost:5432/postgres"   # your existing server
clone_from = "myapp_dev"                                     # a seeded, quiescent template db
name       = "myapp_${ISOLA_BRANCH_SLUG}"

[services.api.env]
DATABASE_URL = "${accessories.database.url}"
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
2. `isola ls` shows every service `running` with a port and a
   `<branch>.<project>.localhost:<proxy_port>` URL.
3. Each service URL actually answers. Don't trust `running` alone: a service can
   report `running` on a port nothing listens on when its command ignores
   `$PORT` (step 3). Confirm with a real request, e.g.:

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

## 7. HTTPS (optional)

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
