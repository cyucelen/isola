# Installing isola

isola is a single Go binary that runs on **macOS** and **Linux**. (Windows is
experimental; see [Platform Support](https://github.com/cyucelen/isola/blob/main/README.md#platform-support).)

This document is only about getting the `isola` binary onto your machine. Once it
is installed, configure it for your project with the
[Quick Start](https://github.com/cyucelen/isola/blob/main/README.md#quick-start), and install the
[agent skill](https://github.com/cyucelen/isola/blob/main/README.md#quick-start) (`npx skills add cyucelen/isola`) so your
coding agents can operate it.

## Prerequisites

- **git** (isola drives git worktrees).
- To install with `go install` or from source: **Go 1.25+**.
- Per-worktree database [accessories](https://github.com/cyucelen/isola/blob/main/README.md#accessories) connect to your own
  Postgres or Redis server; isola never manages the server, so install and run
  those yourself if you use them.

## Install

Pick the method that fits your system.

### macOS (Homebrew)

```bash
brew install cyucelen/tap/isola
```

### Linux (packages)

`.deb` and `.rpm` packages are attached to each
[release](https://github.com/cyucelen/isola/releases/latest):

```bash
sudo dpkg -i isola_*_linux_amd64.deb   # Debian/Ubuntu
sudo rpm -i  isola_*_linux_amd64.rpm   # Fedora/RHEL
yay -S isola-bin                        # Arch (AUR)
```

### Go (any platform)

```bash
go install github.com/cyucelen/isola@latest
```

This lands the binary in your Go bin directory (no `sudo`); make sure it is on
`PATH`:

```bash
export PATH="$(go env GOPATH)/bin:$PATH"
```

### From source

```bash
git clone https://github.com/cyucelen/isola.git
cd isola
make build                          # produces ./isola (embeds the version string)
mv isola "$(go env GOPATH)/bin/"    # no sudo, if that dir is on PATH
# or, system-wide:  sudo mv isola /usr/local/bin/
```

## Verify

The install is good when all three succeed:

```bash
isola version          # prints a version string (binary is on PATH and runs)
isola --help           # lists all commands (up, down, destroy, ls, dash, logs, proxy, accessory, hooks, orca, doctor, trust, ...)
isola doctor           # from any git repo: the "git installed" check passes
```

`isola doctor` also reports config, port, and state checks; before you have run
`isola init` those will show as not-yet-configured, which is expected at this
stage. The only install-time requirement it verifies is that **git** is present.
If `isola` is not found after `go install`, your Go bin directory is not on
`PATH` (see the Go section above).

## After installing

- **Configure your project**: `isola init`, then edit `.isola.toml` (see the
  [Configuration Reference](https://github.com/cyucelen/isola/blob/main/docs/configuration.md)).
- **Let agents drive it**: `npx skills add cyucelen/isola` installs the agent
  skill.
- **HTTPS (optional)**: when you enable HTTPS, isola trusts its own CA for you on
  the first `isola up` in a terminal (one password prompt). Run `isola trust`
  yourself if you set it up non-interactively (see
  [`[proxy]`](https://github.com/cyucelen/isola/blob/main/docs/configuration.md#proxy)).
  Restart your browser after trust is installed so it picks up the new CA.
- **Shell completion**: bash, zsh, fish, and PowerShell are supported (see the
  [README](https://github.com/cyucelen/isola/blob/main/README.md#shell-completion)).

If something goes wrong, run `isola doctor`, and see
[Troubleshooting](https://github.com/cyucelen/isola/blob/main/docs/troubleshooting.md).
