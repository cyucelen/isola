---
name: isola
description: Use isola to run and isolate per-git-worktree dev environments — stable per-branch ports, *.localhost reverse-proxy routing, and per-worktree databases. Use when a repo has a .isola.toml, when starting/stopping services across branches, reaching a branch's URL, isolating databases per worktree, or debugging isola.
---

# isola

isola runs each service in `.isola.toml` **per git worktree** with automatic
per-branch port allocation and `<branch-slug>.localhost` reverse-proxy routing,
and can give each worktree its own database. It spawns your existing dev
commands directly (no Docker) and connects to your existing Postgres — it never
manages a server itself. Worktrees are created with plain `git worktree`; isola
has no `add`/`remove`.

## Command map

```bash
isola init                     # create .isola.toml
isola up [--all] [--service X] # start services + auto-start the proxy (background)
isola down [--all] [--prune]   # stop services; --prune tears down deleted worktrees
isola ls [--json]              # worktrees, services, ports, status, URLs
isola dash                     # interactive TUI
isola proxy start [--https]    # run the proxy manually (up auto-starts it); [proxy] enabled=false to opt out
isola proxy stop               # stop the proxy (it is never auto-stopped)
isola logs [worktree] [-f] [-n N] [-s svc]
isola accessory ls|provision|reset|drop [name]    # per-worktree databases
isola doctor                   # health checks
```

Service URLs: `http://<branch-slug>.localhost:<proxy_port>` (bare `localhost` = `main`).
Injected env: `PORT`, `ISOLA_BRANCH`, `ISOLA_BRANCH_SLUG`, `ISOLA_SERVICE`,
`ISOLA_<SVC>_PORT`, `ISOLA_<SVC>_URL`, plus any accessory `inject` var.

## References

Read the file that matches the task (don't load them all up front):

- **references/setup.md** — creating `.isola.toml`, defining services, HTTPS, env vars.
- **references/dev.md** — day-to-day: worktrees, `up`/`down`/`ls`/`dash`/`proxy`/`logs`.
- **references/databases.md** — per-worktree Postgres via `[accessories]` and `isola accessory …`.
- **references/troubleshoot.md** — failed starts, ports, proxy/slug issues, database errors.
