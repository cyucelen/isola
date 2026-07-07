# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- `isola completion` command for bash, zsh, fish, and powershell
- `--json` flag on `isola ls` and `isola version` for machine-readable output
- `--verbose` / `--quiet` global flags with leveled logging (`internal/logging` package)
- `isola doctor` command for environment diagnostics
- Homebrew tap distribution (`brew install cyucelen/tap/isola`)
- Pre-commit hook (`.githooks/pre-commit`) running vet, lint, and short tests
- `make setup-hooks` target to configure git hooks
- Branch slug collision detection with warnings on `isola up`
- Comprehensive tests for Runner lifecycle, Manager integration, and ProxyServer
- CHANGELOG.md

### Fixed

- Process lifecycle race: `done` channel now initialized before `cmd.Start()`
- Double `cmd.Wait()` crash replaced with single-goroutine done channel
- File descriptor leak in proxy server (listeners now tracked and closed)
- `sync.Mutex` upgraded to `sync.RWMutex` in Manager for concurrent reads
- `WithLock` errors in resolver, manager, and TUI no longer silently swallowed
- Windows browser open (`cmd /c start` instead of `rundll32`)
- `os.Getwd()` error handling in TUI start/stop all actions
- Service status check before opening browser in TUI
- All `errcheck` lint violations resolved across 8 files
- golangci-lint CI configuration for Go 1.25 compatibility (action v9, lint v2.8)
- TOCTOU race condition in port allocator documented

### Changed

- Renamed project from `gws` to `isola`
- Go test matrix reduced to Go 1.25 only (matches go.mod requirement)

## [0.1.0] - Initial Release

### Added

- Core process manager: start, stop, restart services per worktree
- Automatic port allocation via FNV32 hash with configurable ranges
- Reverse proxy with subdomain-based routing (`<slug>.localhost:<port>`)
- Interactive TUI dashboard (`isola dash`) with Bubble Tea
- TOML configuration (`.isola.toml`) with validation
- Per-worktree and per-branch service overrides
- Cross-service environment variables (`ISOLA_<SVC>_PORT`, `ISOLA_<SVC>_URL`)
- Service log files in `.isola/logs/`
- File-based state persistence with file locking
- Commands: `init`, `up`, `down`, `ls`, `dash`, `proxy start/stop`, `open`, `version`
- Cross-platform support: Linux, macOS, Windows (amd64, arm64)
- GoReleaser-based release pipeline with GitHub Actions CI
