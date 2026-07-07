# isola - Git Worktree Server Manager

[![CI](https://github.com/cyucelen/isola/actions/workflows/ci.yaml/badge.svg)](https://github.com/cyucelen/isola/actions/workflows/ci.yaml)
[![codecov](https://codecov.io/gh/cyucelen/isola/branch/main/graph/badge.svg)](https://codecov.io/gh/cyucelen/isola)
[![Go Report Card](https://goreportcard.com/badge/github.com/cyucelen/isola)](https://goreportcard.com/report/github.com/cyucelen/isola)
[![Go Reference](https://pkg.go.dev/badge/github.com/cyucelen/isola.svg)](https://pkg.go.dev/github.com/cyucelen/isola)
![Go Version](https://img.shields.io/github/go-mod/go-version/cyucelen/isola)

**isola** automatically manages multiple dev servers per [git worktree](https://git-scm.com/docs/git-worktree) — with automatic port allocation, environment variable injection, and `*.localhost` subdomain routing via reverse proxy.

> Japanese version: [README.ja.md](./README.ja.md)

---

## Credits & when to use portree instead

isola is a fork of **[portree](https://github.com/fairy-pitta/portree)** by [fairy-pitta](https://github.com/fairy-pitta), and builds directly on its worktree-aware process management, hash-based port allocation, and `*.localhost` reverse proxy. Huge thanks to the portree authors for that foundation.

**If you just need per-worktree dev servers** — port allocation and subdomain routing — [portree](https://github.com/fairy-pitta/portree) is the simpler, focused tool. Go use it.

isola extends that model toward **full per-worktree environment isolation**: databases and other stateful accessories, cloned from a template and torn down together with the worktree.

---

## Demo

![isola workflow demo](./demo/demo-workflow.gif)

---

## Features

- **Multi-service** — Define frontend, backend, and any number of services per worktree
- **Automatic port allocation** — Hash-based port assignment (FNV32) with per-service ranges; no port conflicts across worktrees
- **Subdomain reverse proxy** — Access any worktree via `branch-name.localhost:<port>` (no `/etc/hosts` editing required)
- **HTTPS proxy** — Auto-generated certificates or custom cert/key for local HTTPS (Secure Cookies, Service Workers, etc.)
- **Environment variable injection** — `$PORT`, `$ISOLA_BRANCH`, `$ISOLA_BACKEND_URL`, etc. are injected automatically
- **TUI dashboard** — Interactive terminal UI to start, stop, restart, and monitor all services
- **Process lifecycle** — Graceful shutdown (SIGTERM → SIGKILL), log files, stale PID cleanup
- **Per-worktree overrides** — Customize commands, ports, and env vars per branch
- **AI agent friendly** — `isola ls --json` includes `url` and `direct_url` fields for automatic endpoint discovery

---

## Quick Start

### 1. Install

![Install demo](./demo/demo-install.gif)

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

### 2. Initialize

![Init demo](./demo/demo-init.gif)

```bash
cd your-project
isola init
# Creates .isola.toml in the repo root
```

### 3. Configure

Edit `.isola.toml` to match your project:

```toml
[services.frontend]
command = "pnpm run dev"
dir = "frontend"
port_range = { min = 3100, max = 3199 }
proxy_port = 3000

[services.backend]
command = "source .venv/bin/activate && python manage.py runserver 0.0.0.0:$PORT"
dir = "backend"
port_range = { min = 8100, max = 8199 }
proxy_port = 8000

[env]
NODE_ENV = "development"
```

### 4. Start services

```bash
isola up            # Start all services for the current worktree
isola up --all      # Start all services for ALL worktrees
```

### 5. Start the proxy

```bash
isola proxy start
# :3000 → frontend services
# :8000 → backend services

# Or with HTTPS
isola proxy start --https
# Auto-generated certificates for local HTTPS
```

### 6. Open in browser

```bash
isola open                    # Opens http://main.localhost:3000
isola open --service backend  # Opens http://main.localhost:8000
```

---

## Commands

| Command                      | Description                                           |
| ---------------------------- | ----------------------------------------------------- |
| `isola init`               | Create a `.isola.toml` configuration file           |
| `isola up`                 | Start services for the current worktree               |
| `isola up --all`           | Start services for all worktrees                      |
| `isola up --service`       | Start a specific service only                         |
| `isola down`               | Stop services for the current worktree                |
| `isola down --all`         | Stop services for all worktrees                       |
| `isola ls`                 | List all worktrees, services, ports, status, and PIDs |
| `isola dash`               | Open the interactive TUI dashboard                    |
| `isola proxy start`        | Start the reverse proxy (foreground)                  |
| `isola proxy start --https`| Start the reverse proxy with HTTPS (auto-generated certs) |
| `isola proxy stop`         | Stop the reverse proxy                                |
| `isola trust`              | Install the CA certificate into the system trust store|
| `isola open`               | Open the current worktree in a browser                |
| `isola doctor`             | Run diagnostic checks on config and ports             |
| `isola version`            | Print version information                             |

---

## Configuration Reference

The `.isola.toml` file lives at the root of your git repository.

### `[services.<name>]`

Define one or more services. Each worktree will run all defined services.

| Field        | Type         | Required | Description                                                 |
| ------------ | ------------ | -------- | ----------------------------------------------------------- |
| `command`    | string       | yes      | Shell command to start the service                          |
| `dir`        | string       | no       | Working directory relative to worktree root (default: root) |
| `port_range` | `{min, max}` | yes      | Port allocation range for this service                      |
| `proxy_port` | int          | yes      | Port the reverse proxy listens on for this service          |

```toml
[services.frontend]
command = "pnpm run dev"
dir = "frontend"
port_range = { min = 3100, max = 3199 }
proxy_port = 3000
```

> [!IMPORTANT]
> **Make your command bind the allocated `$PORT`.** isola injects the
> allocated port as the `PORT` environment variable, but your service must
> actually listen on it — otherwise it will start on its own default port and
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
> Vite treats everything after `--` as positional args — so `--port` is silently
> ignored and Vite falls back to `5173`. Use `npx vite --port $PORT`, or read
> `PORT` inside `vite.config.ts` as shown above. (See
> [#9](https://github.com/cyucelen/isola/issues/9).)

### `[env]`

Global environment variables injected into all services.

```toml
[env]
NODE_ENV = "development"
DATABASE_URL = "postgres://localhost/mydb"
```

### `[worktrees."<branch>"]`

Per-worktree overrides. You can customize the command, fix a specific port, or add extra environment variables.

```toml
[worktrees.main]
services.frontend.port = 3100       # Fixed port for main branch

[worktrees."feature/auth"]
services.backend.command = "python manage.py runserver --settings=myapp.auth 0.0.0.0:$PORT"
services.backend.env = { DEBUG = "1" }
```

---

## Environment Variables

isola automatically injects the following environment variables into every service process:

| Variable            | Example                                             | Description                       |
| ------------------- | --------------------------------------------------- | --------------------------------- |
| `PORT`              | `3117`                                              | Allocated port for this service   |
| `ISOLA_BRANCH`         | `feature/auth`                                      | Current branch name               |
| `ISOLA_BRANCH_SLUG`    | `feature-auth`                                      | URL-safe slug of the branch name  |
| `ISOLA_SERVICE`        | `frontend`                                          | Name of the current service       |
| `ISOLA_<SERVICE>_PORT` | `ISOLA_FRONTEND_PORT=3117`                             | Port of each sibling service      |
| `ISOLA_<SERVICE>_URL`  | `ISOLA_BACKEND_URL=http://feature-auth.localhost:8000` | Proxy URL of each sibling service |

This allows services to discover each other automatically:

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

---

## How It Works

```
┌─────────────────────────────────────────────────────────────┐
│  git repository                                             │
│                                                             │
│  main worktree          feature/auth worktree               │
│  ┌───────────────┐      ┌───────────────┐                   │
│  │ frontend :3100│      │ frontend :3117│                   │
│  │ backend  :8100│      │ backend  :8104│                   │
│  └───────────────┘      └───────────────┘                   │
│         │                      │                            │
└─────────┼──────────────────────┼────────────────────────────┘
          │                      │
    ┌─────▼──────────────────────▼─────┐
    │     isola reverse proxy        │
    │                                  │
    │  :3000  ←  *.localhost:3000      │
    │  :8000  ←  *.localhost:8000      │
    └──────────────────────────────────┘
          │                      │
          ▼                      ▼
  main.localhost:3000    feature-auth.localhost:3000
  main.localhost:8000    feature-auth.localhost:8000
```

1. **Port allocation** — Each service gets a port via `FNV32(branch:service) % range`. Stable across restarts.
2. **Process management** — Services run as child processes with process groups. Logs go to `.isola/logs/`.
3. **Reverse proxy** — One HTTP listener per `proxy_port`. Routes based on `Host` header subdomain.
4. **`*.localhost`** — Per [RFC 6761](https://tools.ietf.org/html/rfc6761), modern browsers resolve `*.localhost` to `127.0.0.1` automatically.

---

## TUI Dashboard

![TUI Dashboard demo](./demo/demo-tui.gif)

Launch with `isola dash`:

```
╭─ isola dashboard ──────────────────────────────────────────╮
│                                                               │
│  WORKTREE        SERVICE    PORT   STATUS      PID            │
│  ──────────────────────────────────────────────────────────── │
│ ▸ main           frontend   3100   ● running   12345          │
│   main           backend    8100   ● running   12346          │
│   feature/auth   frontend   3117   ○ stopped   —              │
│   feature/auth   backend    8104   ○ stopped   —              │
│                                                               │
│  Proxy: ● running (:3000, :8000)                              │
│                                                               │
│  [s] start  [x] stop  [r] restart  [o] open in browser       │
│  [a] start all  [X] stop all  [p] toggle proxy                │
│  [l] view logs  [q] quit                                      │
╰───────────────────────────────────────────────────────────────╯
```

**Key bindings:**

| Key     | Action                   |
| ------- | ------------------------ |
| `j`/`k` | Move cursor down/up      |
| `s`     | Start selected service   |
| `x`     | Stop selected service    |
| `r`     | Restart selected service |
| `o`     | Open in browser          |
| `a`     | Start all services       |
| `X`     | Stop all services        |
| `p`     | Toggle proxy             |
| `l`     | View log file path       |
| `q`     | Quit                     |

---

## Example Workflow

```bash
# You're working on a monorepo with frontend + backend
cd my-project

# Initialize isola
isola init
# Edit .isola.toml to define your services...

# Create a feature branch worktree
git worktree add ../my-project-feature-auth feature/auth

# Start services on your current branch
isola up
# Starting frontend (port 3100) for main ...
# Starting backend (port 8100) for main ...
# ✓ 2 services started for main

# Start services on ALL worktrees at once
isola up --all
# ✓ 4 services started

# Check status
isola ls
# WORKTREE        SERVICE    PORT   STATUS    PID
# main            frontend   3100   running   12345
# main            backend    8100   running   12346
# feature/auth    frontend   3117   running   12347
# feature/auth    backend    8104   running   12348

# JSON output (great for AI agents and scripts)
isola ls --json
# [{"worktree":"main","service":"frontend","port":3100,"status":"running",
#   "pid":12345,"url":"http://main.localhost:3000","direct_url":"http://localhost:3100"}, ...]

# Start the proxy
isola proxy start
# Access:
#   http://main.localhost:3000          → frontend (main)
#   http://main.localhost:8000          → backend (main)
#   http://feature-auth.localhost:3000  → frontend (feature/auth)
#   http://feature-auth.localhost:8000  → backend (feature/auth)

# Or start with HTTPS (for Secure Cookies, Service Workers, etc.)
isola proxy start --https
# Auto-generates certificates in .isola/certs/
# Access via https://main.localhost:3000

# Trust the CA to remove browser warnings
isola trust

# Open in browser
isola open
# Opening http://main.localhost:3000 ...

# Or use the TUI
isola dash

# When done
isola down --all
# ✓ 4 services stopped
```

---

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

---

## Troubleshooting

### Service fails to start

- Check the log file at `.isola/logs/<branch-slug>.<service>.log` for error output.
- Verify the `command` in `.isola.toml` runs correctly when executed manually.
- Ensure the working `dir` exists relative to the worktree root.

### Port conflict

- Run `isola doctor` to check for port conflicts.
- If a port is already in use, isola uses linear probing to find the next available port in the range.
- If the entire range is exhausted, widen the `port_range` in `.isola.toml`.

### Stale processes

- Run `isola doctor` to detect stale PIDs in the state file.
- Use `isola down --all` to clean up and stop all services.
- If a process was killed externally, `isola ls` will show it as `stopped` automatically.

### Proxy not routing correctly

- Ensure the proxy is running with `isola proxy start`.
- Verify your browser resolves `*.localhost` — modern browsers do this per RFC 6761.
- Check that the target service is actually running with `isola ls`.
- The proxy routes based on the `Host` header subdomain, so access via `http://<branch-slug>.localhost:<proxy_port>`.

### HTTPS issues

- Auto-generated certificates are stored in `.isola/certs/` when using `isola proxy start --https`.
- Run `isola trust` to install the CA certificate into your system trust store and eliminate browser warnings.
- To use custom certificates, pass `isola proxy start --cert <path> --key <path>` (both flags are required together).
- To verify with curl: `curl --cacert .isola/certs/ca.crt https://main.localhost:3000`.

---

## Platform Support

| Platform | Status | Notes |
| -------- | ------ | ----- |
| **macOS** | Fully supported | Primary development platform |
| **Linux** | Fully supported | Tested on Ubuntu, Debian, Fedora |
| **Windows** | Experimental | Basic functionality works; file locking uses alternative implementation. Please report issues. |

---

## FAQ

### Does `*.localhost` work in all browsers?

Modern browsers (Chrome, Firefox, Edge, Safari) resolve `*.localhost` to `127.0.0.1` per [RFC 6761](https://tools.ietf.org/html/rfc6761). No `/etc/hosts` editing or DNS configuration is needed.

### What happens if two worktrees hash to the same port?

isola uses linear probing — if the hash-derived port is already taken, it tries the next port in the range until it finds a free one.

### Can I use isola without the proxy?

Yes. `isola up` starts your services with allocated ports. You can access them directly at `localhost:<port>`. The proxy is optional.

### Where are logs stored?

Service logs are written to `.isola/logs/<branch-slug>.<service>.log` in the main worktree's root.

### Where is state stored?

Runtime state (PIDs, port assignments) is stored in `.isola/state.json` with file-level locking for concurrent access safety.

### Can I run different commands per branch?

Yes, use `[worktrees."branch-name"]` overrides in `.isola.toml`:

```toml
[worktrees."feature/auth"]
services.backend.command = "python manage.py runserver --settings=auth 0.0.0.0:$PORT"
services.backend.env = { DEBUG = "1" }
```

---

## Project Structure

```
isola/
├── main.go                      # Entry point
├── cmd/                         # CLI commands (cobra)
│   ├── root.go                  # Root command + repo/config detection
│   ├── init.go                  # isola init
│   ├── up.go                    # isola up
│   ├── down.go                  # isola down
│   ├── ls.go                    # isola ls
│   ├── dash.go                  # isola dash
│   ├── proxy.go                 # isola proxy start|stop
│   ├── trust.go                 # isola trust
│   ├── open.go                  # isola open
│   └── version.go               # isola version
├── internal/
│   ├── cert/cert.go             # CA + server certificate auto-generation
│   ├── config/config.go         # .isola.toml loading & validation
│   ├── git/
│   │   ├── repo.go              # Repo root / common dir detection
│   │   └── worktree.go          # Worktree listing & branch slugs
│   ├── state/store.go           # JSON state persistence with flock
│   ├── port/
│   │   ├── allocator.go         # FNV32 hash-based port allocation
│   │   └── registry.go          # Port assignment management
│   ├── process/
│   │   ├── runner.go            # Single process lifecycle
│   │   └── manager.go           # Multi-service orchestration
│   ├── proxy/
│   │   ├── resolver.go          # Slug + port → backend resolution
│   │   └── server.go            # HTTP/HTTPS reverse proxy
│   ├── browser/open.go          # OS-aware browser opening
│   └── tui/                     # Bubble Tea TUI dashboard
│       ├── app.go               # Top-level model
│       ├── dashboard.go         # Table rendering
│       ├── keys.go              # Key bindings
│       ├── messages.go          # Custom messages
│       └── styles.go            # Lip Gloss styles
├── Makefile
├── .goreleaser.yaml
└── .github/workflows/
    ├── ci.yaml
    └── release.yaml
```

---

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

---

## License

MIT License. See [LICENSE](./LICENSE) for details.
