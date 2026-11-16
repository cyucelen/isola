# ADR-007: Direct-Port Activation and Stop Intent

## Status

Accepted

> **Update:** direct listeners stay loopback-only by default, and a per-service
> `bind = "all"` opts a service into every interface. See
> [Listener interfaces](#listener-interfaces) below.

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

## Listener interfaces

### Context

Before on-demand, a service bound its own port on whatever interface it chose;
many dev servers bind every interface, so a physical phone on the same Wi-Fi
reached them at `<LAN IP>:<port>`. Once the daemon owns the public port, the
interface is isola's choice, and the original loopback-only binding cut those
devices off while simulators kept working.

### Decision

- Loopback stays the default. The daemon is machine-wide and starts processes
  on request, so a listener reachable from the network lets any peer on any
  of the machine's networks (VPN, public Wi-Fi) run a project's setup and
  command. That should be the exception a repo asks for, not the baseline.
- Opt in per service with `bind = "all"` (default `"loopback"`), not with a
  `[proxy]` key. The daemon serves every project, so a proxy-wide key set by one
  repo would expose every project's on-demand services, and even within one
  repo it would expose services that only siblings call. A per-service key
  matches what the service itself would have bound.
- `bind = "all"` listens on the unspecified addresses, `0.0.0.0` (tcp4) and
  `::` (tcp6, IPv6-only so the two do not collide), not on a specific LAN
  address. That matches a dev server binding all interfaces, includes loopback
  so local clients and `direct_url` keep working, and survives the LAN address
  changing (DHCP renewals, switching networks) without tracking interfaces.
  Hosts without IPv6 keep the IPv4 listener, as on loopback.
- The daemon reads `bind` from the project's live config on each sync, not from
  state, so an edit applies within a second without `up` or a restart. A
  changed value is handled like a changed port: the listener and its open
  connections close and the new one binds.
- `bind` is a config error unless the service is `on_demand` with a
  `port_range`; anywhere else the service binds its own port and the key could
  not take effect.

### Consequences

Physical devices can reach and wake opted-in services again, at the exposure
those services had when they bound their own ports, applied to every worktree's
port for the service. An idle service with `bind = "all"` now wakes on network
traffic as well as local traffic. Proxy URLs (`proxy_port`) remain
loopback-only, and an older daemon ignores `bind` until it is restarted. On
macOS with the firewall enabled, the `isola` binary, not the service, is the
process that must be allowed to accept incoming connections.
