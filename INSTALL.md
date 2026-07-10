# Installing isola

isola is a single Go binary that runs on **macOS** and **Linux**. (Windows is
experimental; see [Platform Support](README.md#platform-support).)

This document is only about getting the `isola` binary onto your machine. Once it
is installed, configure it for your project with the
[Quick Start](README.md#quick-start), and install the
[agent skill](README.md#quick-start) (`npx skills add cyucelen/isola`) so your
coding agents can operate it.

## Prerequisites

- **git** (isola drives git worktrees).
- To install with `go install` or from source: **Go 1.25+**.
- Per-worktree database [accessories](README.md#accessories) connect to your own
  Postgres or Redis server; isola never manages the server, so install and run
  those yourself if you use them.

## Install

Pick the method that fits your system.

### Homebrew (macOS or Linux)

```bash
brew install cyucelen/tap/isola
```

### Go

```bash
go install github.com/cyucelen/isola@latest
```

Make sure your Go bin directory is on `PATH`:

```bash
export PATH="$(go env GOPATH)/bin:$PATH"
```

### From source

```bash
git clone https://github.com/cyucelen/isola.git
cd isola
make build                 # produces ./isola
sudo mv isola /usr/local/bin/   # or anywhere on your PATH
```

## Verify

```bash
isola version
```

## After installing

- **Configure your project**: `isola init`, then edit `.isola.toml` (see the
  [Configuration Reference](docs/configuration.md)).
- **Let agents drive it**: `npx skills add cyucelen/isola` installs the agent
  skill.
- **HTTPS (optional)**: when you enable HTTPS, isola trusts its own CA for you on
  the first `isola up` in a terminal (one password prompt). Run `isola trust`
  yourself if you set it up non-interactively (see
  [`[proxy]`](docs/configuration.md#proxy)).
- **Shell completion**: bash, zsh, fish, and PowerShell are supported (see the
  [README](README.md#shell-completion)).

If something goes wrong, run `isola doctor`, and see
[Troubleshooting](docs/troubleshooting.md).
