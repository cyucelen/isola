<!-- prettier-ignore -->
<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/logo-dark.svg">
    <img alt="isola" src="assets/logo.svg" width="240">
  </picture>
</p>

<h1 align="center">isola</h1>

**Run many isolated dev environments on one machine, one per git worktree.**
Each worktree gets its own services (stable ports + `*.localhost` URLs), its own
environment variables, and its own database cloned from a template. No Docker,
no `/etc/hosts`, no port juggling, and it's built for the age of parallel AI
coding agents.

## Why isola

Git [worktrees](https://git-scm.com/docs/git-worktree) let you check out several
branches at once. But the moment you *run* them, they collide.

Picture three things in flight: an AI agent refactoring on one branch, a spike on
another, a hotfix on `main`.

- They all try to bind `:3000` and `:5432`.
- They migrate and seed the **same** dev database, so one branch's schema change
breaks another, and a destructive test on one wipes data the others still need.
- You're juggling `.env` files, hunting down stray processes, and guessing which
server is which.

**isola gives each worktree its own running environment:** processes, ports,
`*.localhost` URLs, injected env vars, and its own database (cloned from a
template, dropped with the worktree). So N branches, or N agents, run fully
isolated, side by side, and clean up after themselves.

Point your coding agents at it and each one gets a real, working, isolated stack
to build and test against, without stepping on anything else's ports or data.

## Features

- **Per-worktree isolation**: each git worktree runs as its own environment with its own processes, ports, URLs, env vars, and database
- **Isolated databases**: every worktree gets its own database, created on `up` and dropped with the worktree (Postgres cloned from a template, Redis by logical DB; the driver model extends to more)
- **Automatic port allocation**: deterministic hash-based ports (FNV32) with per-service ranges; no conflicts across worktrees
- **Subdomain reverse proxy**: reach any worktree at `branch.project.localhost:<port>`, HTTP or HTTPS, no `/etc/hosts` editing. One machine-wide proxy auto-starts on `isola up` and serves every project at once
- **Environment injection**: `$PORT`, `$ISOLA_BRANCH`, `$ISOLA_BACKEND_URL`, `$DATABASE_URL`, etc. injected automatically so services (and databases) wire themselves up
- **AI-agent friendly**: `isola ls --json` exposes every endpoint, and isola ships an installable [Agent Skill](#quick-start) (`npx skills add cyucelen/isola`)
- **TUI dashboard**: interactive terminal UI to start, stop, restart, and monitor every service across worktrees
- **Zero dependencies**: a single Go binary that drives your existing toolchain and your existing Postgres; no Docker
- **Per-worktree overrides**: customize commands, ports, and env vars per branch

## Demo

![isola workflow demo](./demo/demo-workflow.gif)

## Quick Start

### Set it up with your coding agent (recommended)

Point your agent at your repo and it installs isola, learns it from the skill,
and writes a working `.isola.toml` for your services. Paste this into Claude
Code, Cursor, or Codex:

```text
Set up isola for this project. Work from what THIS repo and my machine actually use; don't assume a language, framework, or OS.

1. Install the isola CLI by following https://github.com/cyucelen/isola/blob/main/INSTALL.md (covers macOS and Linux). Pick the method that fits my system.
2. Add the agent skill so you know how to drive it: `npx skills add cyucelen/isola`.
3. Discover the long-running dev processes: inspect the repo (package.json scripts, Makefile, Procfile, docker-compose.yml, mix.exs, Cargo.toml, manage.py, Gemfile, go.mod, etc.) for every service I'd start by hand (web app, API, worker, ...). There may be one or several.
4. Run `isola init`, then edit `.isola.toml`: one [services.<name>] per service with its dir, real start command, a unique port_range, and a proxy_port. Each command MUST bind the injected $PORT.
5. Per-worktree databases: for Postgres or Redis, add an [accessories] block so each worktree gets its own isolated instance. If the project needs a stateful service isola doesn't support yet (MySQL, MongoDB, a queue, ...), do NOT fake it: tell me which one, leave it out, and point me to https://github.com/cyucelen/isola/blob/main/docs/writing-an-accessory.md so I can contribute a driver.
6. Verify: `isola up`, then `isola ls`, and fix anything that isn't running.

If configuration fails, read https://github.com/cyucelen/isola/blob/main/docs/configuration.md.
```

Now you can run several agents at once, each with its own real, working stack to
build and test against, without them fighting over ports or corrupting a shared
database, and without you narrating a single command.

### Or set it up manually

#### 1. Install

```bash
# Homebrew
brew install cyucelen/tap/isola

# Go install
go install github.com/cyucelen/isola@latest

# Or build from source
git clone https://github.com/cyucelen/isola.git
cd isola
make build
```

See [INSTALL.md](INSTALL.md) for prerequisites and platform notes.

#### 2. Initialize

```bash
cd your-project
isola init
# Creates .isola.toml in the repo root
```

#### 3. Configure

Edit `.isola.toml` to match your project:

```toml
[services.frontend]
command = "pnpm run dev"
dir = "frontend"
port_range = { min = 3100, max = 3199 }
proxy_port = 3000

[services.backend]
command = "go run ./cmd/server"
dir = "backend"
port_range = { min = 8100, max = 8199 }
proxy_port = 8000

[env]
NODE_ENV = "development"
```

To give each worktree its own database, add an `[accessories.primary]` block
(see [Accessories](#accessories)).

#### 4. Start services

```bash
isola up            # this worktree's services (also auto-starts the proxy)
isola up --all      # every worktree's services
```

Your services are now reachable at
`http://<branch-slug>.<project>.localhost:<proxy_port>` (e.g.
`http://main.myapp.localhost:3000`), where `<project>` is this repo's name
(default: its directory). One machine-wide proxy serves every project and runs
until `isola proxy stop`; HTTPS and opting out live under
[`[proxy]`](docs/configuration.md#proxy). Manage everything interactively with `isola dash`.

If you set up by hand, install the agent skill too so your agents can drive isola
day to day: `npx skills add cyucelen/isola`.

## Commands

| Command                     | Description                                               |
| --------------------------- | --------------------------------------------------------- |
| `isola init`                | Create a `.isola.toml` configuration file                 |
| `isola up`                  | Start services for the current worktree                   |
| `isola up --all`            | Start services for all worktrees                          |
| `isola up --service`        | Start a specific service only                             |
| `isola down`                | Stop services for the current worktree                    |
| `isola down --all`          | Stop services for all worktrees                           |
| `isola ls`                  | List all worktrees, services, ports, status, and PIDs     |
| `isola dash`                | Open the interactive TUI dashboard                        |
| `isola proxy start`         | Start the reverse proxy (foreground)                      |
| `isola proxy start --https` | Start the reverse proxy with HTTPS (auto-generated certs) |
| `isola proxy stop`          | Stop the reverse proxy                                    |
| `isola trust`               | Install the CA certificate into the system trust store    |
| `isola doctor`              | Run diagnostic checks on config and ports                 |
| `isola version`             | Print version information                                 |

## Accessories

An **accessory** is an isolated stateful dependency isola creates per worktree,
brought up on `isola up` and dropped on `isola down --prune`. isola connects to
your existing server (it never manages the server itself) and injects a
connection string into your services, into both the process environment and, if
the worktree has one, its `.env`. Two kinds ship today:

| Kind | Each worktree gets | Typical inject |
| ---------- | ------------------------------------------------------------------------------------- | -------------- |
| `postgres` | Its own database, physically cloned from a seeded template (via pgx, no `psql` needed) | `DATABASE_URL` |
| `redis`    | Its own numbered logical DB, allocated collision-free and flushed on reset/drop        | `REDIS_URL`    |

```toml
[accessories.primary]
kind       = "postgres"
server_url = "postgres://postgres@localhost:5432/postgres"
clone_from = "myapp_dev"                    # seeded template, cloned per worktree
name       = "myapp_${ISOLA_BRANCH_SLUG}"
inject     = "DATABASE_URL"

[accessories.cache]
kind       = "redis"
server_url = "redis://localhost:6379"
inject     = "REDIS_URL"
```

Manage them out of band with `isola accessory ls|up|reset|drop [name]`. See the
[Configuration Reference](docs/configuration.md#accessoriesname) for every field
of each kind, and [Writing a new accessory](docs/writing-an-accessory.md) to add
your own.

## Environment Variables

A service's environment is assembled from several sources. When the same
variable is set in more than one, the later one wins, so the effective
precedence is (highest first):

1. **Accessory connection strings** (`DATABASE_URL`, `REDIS_URL`, …) that isola
 injects for the worktree's isolated database or cache. Authoritative, so
 per-worktree isolation holds. See [Accessories](#accessories).
2. **isola built-ins**: the auto-injected variables listed below.
3. **Your config**: `[worktrees."<branch>"]` overrides beat per-service
 `[services.<name>].env`, which beats global `[env]`.
4. **Your shell**: variables inherited from the environment isola runs in.

Separately, `.env`-style files are copied into each worktree by
[`copy_files`](docs/configuration.md#copy_files), a layer your app reads directly. isola keeps the two
in sync for accessories: it writes each accessory's `inject` key into the copied
`.env` (only those keys), so whether a tool reads the process environment or the
`.env` file, it gets this worktree's isolated database or cache.

### Injected by isola

isola sets these on every service process:

| Variable               | Example                                                | Description                       |
| ---------------------- | ------------------------------------------------------ | --------------------------------- |
| `PORT`                 | `3117`                                                 | Allocated port for this service   |
| `ISOLA_BRANCH`         | `feature/auth`                                         | Current branch name               |
| `ISOLA_BRANCH_SLUG`    | `feature-auth`                                         | URL-safe slug of the branch name  |
| `ISOLA_SERVICE`        | `frontend`                                             | Name of the current service       |
| `ISOLA_<SERVICE>_PORT` | `ISOLA_FRONTEND_PORT=3117`                             | Port of each sibling service      |
| `ISOLA_<SERVICE>_URL`  | `ISOLA_BACKEND_URL=http://feature-auth.myapp.localhost:8000` | Proxy URL of each sibling service |

So services can discover each other automatically:

```js
// next.config.js
module.exports = {
  async rewrites() {
    return [
      {
        source: "/api/:path*",
        destination: `${process.env.ISOLA_BACKEND_URL}/api/:path*`,
      },
    ];
  },
};
```

### Setting your own

Global variables go under `[env]`; per-service (`[services.<name>].env`) and
per-worktree (`[worktrees."<branch>"].services.<name>.env`) overrides are in the
[Configuration Reference](docs/configuration.md). Values may reference
`${VAR}`, expanded against the injected variables above and your shell (e.g.
`API_URL = "${ISOLA_BACKEND_URL}"`); a bare `$` is left literal, so a password
like `p$ssw0rd` survives unchanged.

## Configuration

Everything lives in a single `.isola.toml` at the root of your repository:
services, global environment, per-worktree accessories, the reverse proxy, and
file copying. The [**Configuration Reference**](docs/configuration.md) documents
every block and field; the [Quick Start](#quick-start) above covers the common
case.

## How It Works

```
  project "myapp"                    project "api"
  ┌───────────────────────┐          ┌───────────────────────┐
  │ main   frontend :3100 │          │ main   web      :3140 │
  │ feat   frontend :3117 │          │ dev    web      :3151 │
  └───────────┬───────────┘          └───────────┬───────────┘
              │      (each project registers in ~/.isola/registry)
              └──────────────┬───────────────────┘
              ┌──────────────▼──────────────┐
              │  isola proxy (machine-wide) │
              │  binds :3000, :8000, ...    │
              │  routes by <branch>.<proj>  │
              └──────────────┬──────────────┘
                             ▼
  main.myapp.localhost:3000     main.api.localhost:3000
  feat.myapp.localhost:3000     dev.api.localhost:3000
```

1. **Port allocation**: each service gets a backend port via `FNV32(branch:service) % range`, skipping ports already in use so projects never share one. Stable across restarts.
2. **Process management**: services run as child processes with process groups. Logs go to `.isola/logs/`.
3. **Shared proxy**: one machine-wide daemon reads `~/.isola/registry`, binds the union of every project's `proxy_port`s, and routes each request by its `<branch>.<project>.localhost` host to that project's backend (resolved live from the project's own state).
4. `**.localhost**`: per [RFC 6761](https://tools.ietf.org/html/rfc6761), modern browsers resolve `*.localhost` (including `<branch>.<project>.localhost`) to `127.0.0.1` automatically.

## TUI Dashboard

Launch it with `isola dash`:

![TUI Dashboard demo](./demo/demo-tui.gif)

## Shell Completion

isola supports shell completion for bash, zsh, fish, and PowerShell.

**bash:**

```bash
source <(isola completion bash)
# Or for persistent use:
isola completion bash > /etc/bash_completion.d/isola
```

**zsh:**

```bash
isola completion zsh > "${fpath[1]}/_isola"
# You may need to start a new shell for this to take effect.
```

**fish:**

```bash
isola completion fish | source
# Or for persistent use:
isola completion fish > ~/.config/fish/completions/isola.fish
```

**PowerShell:**

```powershell
isola completion powershell | Out-String | Invoke-Expression
# Or for persistent use:
isola completion powershell > isola.ps1
# and add ". isola.ps1" to your PowerShell profile.
```

## Troubleshooting

Start with `isola doctor`, which checks your config and ports. The
[Troubleshooting guide](docs/troubleshooting.md) covers the common failures and
their fixes: services that won't start, port conflicts, stale processes, proxy
routing, and HTTPS.

## Platform Support

| Platform    | Status          | Notes                                                                                          |
| ----------- | --------------- | ---------------------------------------------------------------------------------------------- |
| **macOS**   | Fully supported | Primary development platform                                                                   |
| **Linux**   | Fully supported | Tested on Ubuntu, Debian, Fedora                                                               |
| **Windows** | Experimental    | Basic functionality works; file locking uses alternative implementation. Please report issues. |

## FAQ

### Can I use isola without the proxy?

Yes. `isola up` starts your services on their allocated ports; reach them
directly at `localhost:<port>`. The proxy (and its `*.localhost` routing) is
optional; disable auto-start with `[proxy] enabled = false`.

### Where does isola keep its files?

All under `.isola/` in the main worktree: service logs at
`.isola/logs/<branch-slug>.<service>.log`, runtime state (PIDs, port
assignments) in `.isola/state.json` (file-locked for concurrent access), and
auto-generated HTTPS certs in `.isola/certs/`.

## Contributing

1. Fork the repository
2. Create your feature branch (`git checkout -b feature/amazing`)
3. Commit your changes (`git commit -m 'feat: add amazing feature'`)
4. Push to the branch (`git push origin feature/amazing`)
5. Open a Pull Request

```bash
# Development
make build      # Build binary
make test       # Run tests with race detector
make lint       # Run golangci-lint
make all        # fmt + vet + lint + test + build
```

## Credits &amp; prior art

isola is a fork of [**portree**](https://github.com/fairy-pitta/portree) by [fairy-pitta](https://github.com/fairy-pitta), and builds directly on its worktree-aware process management, hash-based port allocation, and `*.localhost` reverse proxy. Huge thanks to the portree authors for that foundation.

**If you just need per-worktree dev servers** (port allocation and subdomain routing, without databases or other stateful isolation), [portree](https://github.com/fairy-pitta/portree) is the simpler, focused tool. isola extends that model toward **full per-worktree environment isolation**: databases and other stateful accessories, cloned from a template and torn down together with the worktree.

## License

MIT License. See [LICENSE](./LICENSE) for details.
