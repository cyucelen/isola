# Troubleshooting

When something misbehaves, `isola doctor` is the first stop: it checks your
config and ports and flags stale state. The sections below cover the common
failures and their fixes.

## Service fails to start

- Check the log file at `.isola/logs/<branch-slug>.<service>.log` for error output.
- Verify the `command` in `.isola.toml` runs correctly when executed manually.
- Ensure the working `dir` exists relative to the worktree root.

## Port conflict

- Run `isola doctor` to check for port conflicts.
- If the range is exhausted, widen `port_range` in `.isola.toml`.

## Stale processes

- Run `isola doctor` to detect stale PIDs in the state file.
- Use `isola down --all` to clean up and stop all services.
- If a process was killed externally, `isola ls` will show it as `stopped` automatically.

## Proxy not routing correctly

- Ensure the shared proxy is running (`isola up` starts it, or run `isola proxy start`).
- Verify your browser resolves `*.localhost` (all modern browsers do), including the three-label `<branch>.<project>.localhost`.
- Check that the target service is actually running with `isola ls`.
- URLs are project-qualified: access via `http://<branch-slug>.<project>.localhost:<proxy_port>` (a bare `<branch>.localhost` is not routed). `<project>` defaults to the repo's directory name; set it with `project` in `.isola.toml`.

## HTTPS issues

- Auto-generated certificates are stored in `.isola/certs/` when HTTPS is on.
- **Browser warnings / `SSL certificate problem`?** The CA is not trusted yet. isola tries to install it on the first HTTPS `isola up` in a terminal, but a non-interactive `up` (an agent, CI) skips that. Run `isola trust` once in a terminal to install the CA, or click through the browser warning. Disable the auto behavior with `[proxy] auto_trust = false`.
- The CA and certificates are shared machine-wide, under isola's global state dir (`ca.crt` in `~/.isola/certs`), so `isola trust` runs once and covers every project.
- To verify with curl: `curl --cacert <that-ca.crt> https://main.myapp.localhost:3000`.
