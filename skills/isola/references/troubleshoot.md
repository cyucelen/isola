# Troubleshooting isola

Start with the built-in checks and logs:

```bash
isola doctor         # verify config + environment health
isola ls             # what's running, on which ports, with which PIDs
isola logs -f        # follow a worktree's service output
```

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

## URLs don't route (`<branch>.localhost` fails)

- The proxy must be running: `isola proxy start` (it runs in the foreground).
- Routing uses the branch **slug** (`feature/auth` → `feature-auth`), so the URL
  is `http://<slug>.localhost:<proxy_port>`. The bare `localhost:<proxy_port>`
  maps to `main`.
- If two branches produce the same slug, `isola up` warns about the collision —
  rename one branch, since the proxy can't disambiguate them.

## HTTPS certificate warnings

```bash
isola proxy start --https
isola trust           # install the local CA into the system trust store
```

## Database / accessory errors

- **"collides with clone_from" / maintenance database**: the resolved db `name`
  equals `clone_from` or the server db. Change the `name` template or rename the
  branch (see `references/databases.md`).
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
