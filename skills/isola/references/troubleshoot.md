# Troubleshooting isola

Start with the built-in checks and logs:

```bash
isola doctor         # verify config + environment health
isola ls             # what's running, on which ports, with which PIDs
isola logs -f        # follow a worktree's service output
```

## What `isola doctor` checks

Each line is one check; a ✗ points at the fix:

- **git installed**, **inside git repository** — prerequisites.
- **config file** — `.isola.toml` parses (also shows the service count).
- **proxy port N (svc) available** — each `proxy_port` is free (shows "served by
  isola's proxy" when isola's own daemon is the one holding it).
- **state file healthy** — flags a service recorded `running` whose PID is dead.
- **worktree state consistent** — flags branches in state with no worktree on
  disk (orphans) and tells you to run `isola down --prune`.

## A service won't start / "port already in use"

Ports are hash-allocated per `branch:service` within each service's
`port_range`, so they're stable across restarts. If a start fails with a port
already in use, an orphan process is likely holding it:

```bash
isola down            # stop this worktree's services
isola ls              # confirm nothing is still marked running
isola up
```

If state looks stale after deleting worktrees, run `isola down --prune`.

## URLs don't route

- The shared proxy must be running (`isola up` starts it; `isola proxy start`
  runs it in the foreground).
- URLs are **project-qualified**: `http://<slug>.<project>.localhost:<proxy_port>`
  where `<slug>` is the branch slug (`feature/auth` → `feature-auth`) and
  `<project>` is the repo's name (default: its directory; set via `project` in
  `.isola.toml`). A bare `<slug>.localhost` is not routed and 404s with a hint.
- Confirm the project is registered: it registers on `isola up` and deregisters
  on `isola down --all`.
- If two branches produce the same slug, `isola up` warns about the collision —
  rename one branch, since the proxy can't disambiguate them.
- **Branded 502 vs 404.** A branded **502 "Service not answering"** means the
  worktree is registered with an assigned port but nothing is listening there:
  the service is ignoring `$PORT`, still starting, or crashed (check
  `isola logs`). A **404** means an unknown project or an unrouted bare
  `<slug>.localhost`.
- **Edited `[proxy]` and nothing changed?** The shared daemon keeps the config it
  started with. After changing `[proxy]` (`https`, ports), restart it with
  `isola proxy stop && isola up`; `isola up` warns when the running proxy differs.

## HTTPS certificate warnings

isola trusts its CA on the first HTTPS `isola up` in a terminal. A non-interactive
`up` skips that, so if browsers warn, the user should run `isola trust` once:

```bash
isola trust           # install the local CA into the system trust store
```

Then **restart the browser**. Browsers read the system trust store at startup,
so one that was already open when `isola trust` ran keeps warning until it is
fully quit and reopened (quit the whole app, not just the tab or window). If a
user reports the CA is trusted but the browser still warns, this is almost
always the cause.

## Database / accessory errors

- **"collides with clone_from" / maintenance database**: the resolved db `name`
  equals `clone_from` or the server db. Change the `name` template or rename the
  branch (see `references/accessories.md`).
- **An accessory can't be brought up**: isola warns and starts services anyway,
  but without that accessory's injected var (e.g. `DATABASE_URL`), so your app
  falls back to its own config — watch for it connecting to the wrong database.
  Check the Postgres `server_url` is reachable and `clone_from` exists; `isola
  doctor` and the warning name the cause. Re-run `isola up` to retry.
- **`CREATE DATABASE ... TEMPLATE` fails on a busy template**: `clone_from` must
  be quiescent; don't run services against it. isola terminates lingering
  connections before cloning, but keep it a seed-only database.
- **Prune left databases behind**: if `isola down --prune` reports resources it
  couldn't drop, they're retained for retry — fix the server/config and prune
  again. Use `isola accessory ls` to see what's still recorded.
- **"no free Redis logical database (all N in use)"**: every logical DB on the
  server is claimed by a worktree. Raise the server's `databases` directive and
  set a matching `databases = N` on the accessory.
