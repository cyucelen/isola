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
isola up                     # start defaults; arm on-demand services for this worktree
isola up --service frontend  # start this service now, even if on-demand
isola up --service api,frontend  # start a selected list immediately
isola up --all               # apply the same startup policy to every worktree

isola down                    # stop the current worktree's services
isola down --service frontend # stop just one service
isola down --service api,frontend # stop a selected list
isola down --all              # stop everything
isola destroy                 # stop the current worktree AND drop its per-worktree databases
```

Both commands accept comma-separated names or repeated flags:
`isola up --service api --service frontend`. They validate the entire list before
acting and handle duplicate names once. Root setup runs once per worktree per
`up`, even with multiple services. Combine with `--all` to apply the selection
across worktrees; omit `--all` to affect only the current worktree. Names are
flag values, not positional arguments; `--service api frontend` is invalid.

Started services are detached and keep running after the command returns; use
`isola down` to stop them. There is no `restart` verb. To restart a particular
service immediately, run `isola down --service <name>` then
`isola up --service <name>`; plain `up` only re-arms an on-demand service.

## Using on-demand services

Before an operation that depends on running services, identify the services it
uses from `.isola.toml` and the relevant app/test configuration, then inspect
`isola ls --json`. Match entries to the target worktree; the same service may be
running in another worktree. `on-demand` means armed but idle, `starting` means
setup/startup is in progress, `running` means a
process was started, and `stopped` is not armed for automatic startup.

Start the services needed for the task explicitly from that worktree. For
example, before a UI test that calls an optional API:

```bash
isola ls --json
isola up --service api,frontend
isola ls --json
# Check the API/frontend health or expected response, then run the UI test.
```

`up --service` also works for background workers and is a no-op for an already
running service. A running PID alone does not establish application readiness;
use the service's health endpoint, expected response, or startup logs before
starting dependent work. If startup fails, inspect `isola logs -s <name>` and
the setup error before retrying. Keep unrelated optional services idle; do not
remove `on_demand = true` just to use a service for the current task.

Armed services wake on a proxy request or traffic to their listed direct URL
(`direct_url` / `127.0.0.1:<port>` / `localhost:<port>`) while the shared daemon
is running. This includes internal services with a port but no `proxy_port`.
Env references alone do not start services; calling the referenced URL does.
Connect-and-close port probes do not wake them. Background services without
ports, server-first TCP protocols, and services with `[proxy].enabled = false`
need explicit startup. Starting required dependencies explicitly is still useful
before tests so setup failures are visible and readiness can be checked.

For daemon-managed on-demand services, the listed port and sibling env refs
are public listeners; the service binds a separate private `$PORT`. Use the
listed `direct_url` when calling it; do not derive an address from its logs.
Stopping the shared proxy interrupts those direct connections too.

`down --service <name>` disarms automatic startup, so subsequent requests leave
it stopped. Use `up --service <name>` when it is needed again, or plain `up` to
re-arm it. Once started, services have no automatic idle shutdown. Service
setup is deferred until startup; root setup and accessories still run on `up`.
Stopping during service setup cancels setup and prevents a later launch.

## Reach services in the browser

`isola up` registers this project and ensures the single machine-wide proxy is
running. Started services are reachable, and armed on-demand services start on
their first request, at
`http://<branch-slug>.<project>.localhost:<proxy_port>`. For project `myapp`,
branch `feature/auth`, `proxy_port = 3000`, the frontend is at
`http://feature-auth.myapp.localhost:3000`. URLs are always project-qualified;
the bare `<branch>.localhost` form is not routed. Set `project` in `.isola.toml`
to override the default (the repo's directory name).

Two ways to reach a sibling from another service's env. `${services.<name>.url}`
(or `ISOLA_<SVC>_URL`) is the proxy URL above; it needs `*.localhost` to resolve
(browsers always do; on Linux only systemd-resolved/dnsmasq setups do), so use it
for browser links. `${services.<name>.direct_url}` (or `ISOLA_<SVC>_DIRECT_URL`)
is a loopback `http://127.0.0.1:<port>` URL that needs no DNS and works on every
OS; use it for server-side calls. For on-demand services it reaches the daemon
listener, which wakes the service and forwards traffic.

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
