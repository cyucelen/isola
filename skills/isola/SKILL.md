---
name: isola
description: Use isola to run and isolate per-git-worktree dev environments — stable per-branch ports, *.localhost reverse-proxy routing, and per-worktree databases. Use when a repo has a .isola.toml, when starting/stopping services across branches, reaching a branch's URL, isolating databases per worktree, editing an existing .isola.toml (add/change a service, env, or proxy), or debugging isola.
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
isola up [--all]              # start default services, arm on-demand services, auto-start proxy
isola up --service X,Y        # start selected services immediately, including on-demand services
isola down [--all] [--service X,Y]  # stop selected services, or all if omitted
isola down --prune            # tear down removed worktrees
isola destroy                  # stop + drop the current worktree's services and databases
isola ls [--json]              # worktrees, services, ports, status, URLs
isola dash                     # interactive TUI
isola proxy start              # run the proxy manually (up auto-starts it); [proxy] enabled=false to opt out; HTTPS via [proxy] https=true
isola proxy stop               # stop the proxy (it is never auto-stopped)
isola logs [worktree] [-f] [-n N] [-s svc]
isola accessory ls [--json] | up|reset|drop [name]  # per-worktree databases (up = bring up / reuse)
                               # ls --json gives the real db name (never derive it from the branch)
isola doctor                   # health checks
isola update [--check]         # update isola to the latest release (self-update or via package manager)
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

## Before using a service

Check `.isola.toml` and `isola ls --json` for the target worktree before running
service-dependent tests, calling an API, or opening a UI. `on_demand = true`
means normal `up` leaves the service idle; an `on-demand` status is expected,
not a failed start. A listed port or URL does not mean it is listening.

When the task needs idle services, run `isola up --service api,frontend` (using
their configured names) from that worktree, then check their readiness before
using them. `--service` also accepts repeated flags. Start any other services
the operation needs too (such as an API or worker), while leaving unrelated
optional services idle. Prefer this targeted start to changing `on_demand` or
bringing up every worktree. An armed service can also start on its first proxy
or direct-port request while the shared daemon is running. Background workers
without ports still need explicit startup. Keep using the listed public port;
on-demand services bind the separate `$PORT` supplied by isola.
See [references/dev.md](references/dev.md#using-on-demand-services) for the workflow.

## References

Read the file that matches the task (don't load them all up front):

- **references/dev.md** — day-to-day: starting needed on-demand services,
  worktrees, `up`/`down`/`ls`/`dash`/`proxy`/`logs`.
- **references/configuration.md** — editing an existing `.isola.toml` (add a
  service, wire env, adjust the proxy) and the restart-to-apply rule.
- **references/accessories.md** — per-worktree Postgres and Redis via `[accessories]` and `isola accessory …`.
- **references/troubleshoot.md** — failed starts, ports, proxy/slug issues, database errors.

This skill covers running an already-configured repo, including **changing its
config** (see references/configuration.md; isola does not hot-reload, so restart
after editing). First-time setup — discovering a repo's processes and writing
`.isola.toml` from scratch — is the separate **isola-init** skill.
