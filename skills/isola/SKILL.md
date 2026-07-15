---
name: isola
description: Use isola to run and isolate per-git-worktree dev environments — stable per-branch ports, *.localhost reverse-proxy routing, and per-worktree databases. Use when a repo has a .isola.toml, when starting/stopping services across branches, reaching a branch's URL, isolating databases per worktree, or debugging isola.
---

# isola

isola runs each service in `.isola.toml` **per git worktree** with automatic
per-branch port allocation and `<branch-slug>.<project>.localhost` reverse-proxy routing,
and can give each worktree its own database. It spawns your existing dev
commands directly (no Docker) and connects to your existing Postgres — it never
manages a server itself. Worktrees are created with plain `git worktree`; isola
has no `add`/`remove`.

## Command map

```bash
isola init                     # create .isola.toml
isola up [--all] [--service X] # start services + auto-start the proxy (background)
isola down [--all] [--service X] [--prune]  # stop services; --prune tears down removed worktrees
isola destroy                  # stop + drop the current worktree's services and databases
isola ls [--json]              # worktrees, services, ports, status, URLs
isola dash                     # interactive TUI
isola proxy start              # run the proxy manually (up auto-starts it); [proxy] enabled=false to opt out; HTTPS via [proxy] https=true
isola proxy stop               # stop the proxy (it is never auto-stopped)
isola logs [worktree] [-f] [-n N] [-s svc]
isola accessory ls|up|reset|drop [name]           # per-worktree databases (up = bring up / reuse)
isola doctor                   # health checks
```

Service URLs (one machine-wide proxy): `http://<branch-slug>.<project>.localhost:<proxy_port>`.
Built-in env: `PORT`, `ISOLA_BRANCH`, `ISOLA_BRANCH_SLUG`, `ISOLA_SERVICE`,
`ISOLA_<SVC>_PORT`, `ISOLA_<SVC>_URL`, `ISOLA_<SVC>_DIRECT_URL`. Declare your own
per service under `[services.<name>].env`, referencing `${accessories.<name>.url}`,
`${services.<name>.url}`, `${services.<name>.direct_url}`, or
`${services.<name>.port}`; each service's env is delivered to its process and
(per `[env_file]`) its env file (see references/dev.md for `.url` vs
`.direct_url`). On `up`, gitignored files matching `copy_files` (default
`[".env"]`, a top-level key) are copied from the main worktree into each
worktree, never overwriting.

## References

Read the file that matches the task (don't load them all up front):

- **references/dev.md** — day-to-day: worktrees, `up`/`down`/`ls`/`dash`/`proxy`/`logs`.
- **references/accessories.md** — per-worktree Postgres and Redis via `[accessories]` and `isola accessory …`.
- **references/troubleshoot.md** — failed starts, ports, proxy/slug issues, database errors.

This skill covers running an already-configured repo. First-time setup (writing
`.isola.toml`) is the separate **isola-init** skill.
