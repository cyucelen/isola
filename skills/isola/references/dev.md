# Developing with isola

isola operates on **git worktrees you create yourself** (it has no `add`/`remove`
command). Each worktree's services get stable, per-branch ports and a
`<branch-slug>.<project>.localhost` URL through the shared proxy.

## Worktrees

```bash
# Create a worktree for a new or existing branch (plain git)
git worktree add ../myrepo-feature-auth -b feature/auth
git worktree add ../myrepo-main main

# List worktrees + service status, ports, PIDs, URLs
isola ls
isola ls --json      # machine-readable
```

## Start / stop services

Run these from inside a worktree (or target all worktrees with `--all`):

```bash
isola up                     # start all services for the current worktree
isola up --service frontend  # just one service
isola up --all               # start every worktree's services

isola down                    # stop the current worktree's services
isola down --service frontend # stop just one service
isola down --all              # stop everything
isola destroy                 # stop the current worktree AND drop its per-worktree databases
```

Services are detached and keep running after the command returns; use `isola down`
to stop them. There is no `restart` verb — run `isola down` then `isola up`.

## Reach services in the browser

`isola up` registers this project and ensures the single machine-wide proxy is
running, so services are reachable immediately at
`http://<branch-slug>.<project>.localhost:<proxy_port>`. For project `myapp`,
branch `feature/auth`, `proxy_port = 3000`, the frontend is at
`http://feature-auth.myapp.localhost:3000`. URLs are always project-qualified;
the bare `<branch>.localhost` form is not routed. Set `project` in `.isola.toml`
to override the default (the repo's directory name).

Two ways to reach a sibling from another service's env. `${services.<name>.url}`
(or `ISOLA_<SVC>_URL`) is the proxy URL above; it needs `*.localhost` to resolve
(browsers always do; on Linux only systemd-resolved/dnsmasq setups do), so use it
for browser links. `${services.<name>.direct_url}` (or `ISOLA_<SVC>_DIRECT_URL`)
is a direct `http://127.0.0.1:<port>` to the backend that needs no DNS and works
on every OS; use it for server-side calls between services.

The one proxy serves every project and runs until you stop it (machine-wide):

```bash
isola proxy stop             # stop the shared proxy for ALL projects
isola proxy start            # run it manually in the foreground (normally 'up' does this)
# HTTPS: set [proxy] https = true. isola trusts its CA on the first HTTPS `up`
# in a terminal; if you enabled it non-interactively, tell the user to run
# `isola trust` once.
```

## Interactive dashboard

```bash
isola dash    # TUI to start/stop/restart services and open URLs across worktrees
```

## Logs

```bash
isola logs                 # tail the current worktree's service logs
isola logs -f              # follow
isola logs -n 200          # last 200 lines
isola logs -s backend      # one service
isola logs <worktree>      # a specific worktree
```

## Cleaning up

Git has no worktree-removal hook, so isola reconciles removed worktrees
**automatically**: on the next `isola up` and on the shared proxy's background
timer (~30s) it stops a removed worktree's leftover services and drops the
per-worktree databases it provisioned (only resources isola created). To force
it immediately:

```bash
isola down --prune           # reconcile removed worktrees now
```

To tear down the **current** worktree on demand (stop its services + drop its
databases), use `isola destroy`.
