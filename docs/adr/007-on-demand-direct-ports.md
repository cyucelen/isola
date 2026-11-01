# ADR-007: Direct-Port Activation and Stop Intent

## Status

Accepted

## Context

An idle service cannot receive a direct connection unless another process owns
its advertised port. The service and an activation listener cannot both bind
that port. Direct URLs must remain stable across starts, and sibling env refs
must keep working without DNS or a qualified Host header.

Startup also runs arbitrary service setup commands. Holding a lifecycle lock
through setup and letting `down` time out acquiring that lock loses stop intent:
a long install can launch the service after the stop command has returned.

## Decision

For `on_demand = true` services with ports and automatic proxy management enabled:

- Keep `Port` and persisted port assignments as the public address. The shared
  daemon binds IPv4 and available IPv6 loopback listeners at that address.
- Allocate a separate ephemeral `BackendPort` at startup and pass it as `$PORT`
  to setup and the child. Public service refs (`.port`, `.direct_url`, and
  `ISOLA_<SERVICE>_PORT`) keep the public port. Reserve private ports in state so
  another service's public allocation cannot claim one during setup.
- Relay direct TCP streams without changing their contents, supporting HTTP and
  upgraded WebSocket streams. Qualified HTTP/HTTPS routing shares the same wake
  operation and targets the private backend directly.
- Wait for client bytes before waking an idle service so port-availability
  probes do not trigger startup. Server-first protocols require an explicit
  start; already-started services forward immediately.
- Publish listener readiness with the daemon PID. CLI and dashboard starts
  register the project, ensure the daemon is running, and wait for readiness;
  stale records from a prior daemon do not count. Stopping services or removing
  registry/state entries releases listeners and their connections on the next
  daemon sync. The daemon reconstructs listeners from live state after restart.
- With automatic proxy management disabled, explicit starts retain the original
  direct-bind behavior. Background services without ports remain explicit-only.

For service lifecycle operations:

- Persist a monotonically increasing `StopVersion` before waiting for the
  service lock. Capture it when starting and reject starts invalidated by a stop.
  Capture the entire selected service list before processing its first service,
  so a stop also cancels services queued behind another service's setup.
- Run service setup in a cancellable process group, polling stop intent across
  processes. Stop cancels the entire setup group. Recheck stop intent before
  spawning the service; the lifecycle lock serializes the final start/stop.
- Preserve stop versions across state transitions. Represent setup as `starting`
  so listeners and the dashboard can distinguish it from stopped services.
- Return a nonzero CLI exit status when any selected stop fails.

## Consequences

Direct addresses remain stable and can wake internal services that have no
`proxy_port`. Direct access to daemon-managed on-demand services now depends on
the shared daemon, even once the child is running. Services must bind `$PORT`,
which differs from the public port shown by `ls`. Direct URLs remain HTTP;
qualified HTTPS termination remains the shared proxy's responsibility.

Private ports are allocated using the OS ephemeral pool and recorded before
setup. As with ordinary child-process port allocation, there remains a bind
race with unrelated external processes between allocation and the child binding.

Stopping during service setup now cancels that setup instead of waiting for it
to finish. A failed or canceled start leaves the service stopped; an explicit
start or another `up` can retry it. Root setup remains a worktree-wide step.
