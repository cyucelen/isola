# Developing with isola

isola operates on **git worktrees you create yourself** (it has no `add`/`remove`
command). Each worktree's services get stable, per-branch ports and a
`<branch-slug>.localhost` URL through the proxy.

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

isola down                   # stop the current worktree's services
isola down --all             # stop everything
```

Services are detached and keep running after the command returns; use `isola down`
to stop them. There is no `restart` verb — run `isola down` then `isola up`.

## Reach services in the browser

`isola up` auto-starts the reverse proxy in the background, so services are
reachable immediately at `http://<branch-slug>.localhost:<proxy_port>`. For a
branch `feature/auth` with `proxy_port = 3000`, the frontend is at
`http://feature-auth.localhost:3000`. The root `localhost:3000` routes to `main`.

The proxy runs until you stop it:

```bash
isola proxy stop             # stop the auto-started proxy
isola proxy start            # run one manually in the foreground (e.g. if you set [proxy] enabled = false)
isola proxy start --https    # HTTPS (or set [proxy] https = true; run `isola trust` once to silence warnings)
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

After removing a worktree with `git worktree remove`, drop its leftover state
(and any per-worktree databases) with:

```bash
isola down --prune
```
