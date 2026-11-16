# ADR-008: Direct-Port Listener Interfaces

## Status

Accepted

## Context

[ADR-007](007-on-demand-direct-ports.md) moved an on-demand service's public
direct port from the service to the shared daemon. Before that, a service bound
its own port on whatever interface it chose; many dev servers bind every
interface, so a physical phone on the same Wi-Fi reached them at
`<LAN IP>:<port>`. Once the daemon owns the port, the interface is isola's
choice, and ADR-007's loopback-only listeners cut those devices off while
simulators on the same machine kept working. ADR-007 recorded no reason for
loopback-only.

## Decision

- Loopback stays the default. The daemon is machine-wide and starts processes
  on request, so a listener reachable from the network lets any peer on any of
  the machine's networks (VPN, public Wi-Fi) run a project's setup and command.
  That should be the exception a repo asks for, not the baseline.
- Opt in per service with `bind = "all"` (default `"loopback"`), not with a
  `[proxy]` key. The daemon serves every project, so a proxy-wide key set by one
  repo would expose every project's on-demand services, and even within one
  repo it would expose services that only siblings call.
- `bind = "all"` listens on the unspecified addresses, `0.0.0.0` (tcp4) and
  `::` (tcp6, IPv6-only so the two do not collide), not on a specific LAN
  address. That is what a dev server binding all interfaces does, it includes
  loopback so local clients and `direct_url` keep working, and it survives the
  LAN address changing (DHCP renewals, switching networks) without tracking
  interfaces. Hosts without IPv6 keep the IPv4 listener, as on loopback.
- The daemon reads `bind` from the live config of the worktree that has the
  service's branch checked out, not from state and not from the main checkout,
  so a branch that adds `bind = "all"` exposes its own ports before it merges,
  and other worktrees keep their own setting. Each worktree's config is cached
  and reloaded when its file changes, so a valid edit applies within a second
  without `up` or a restart. A changed value is handled like a changed port:
  the listener and its open connections close and the new one binds. An
  invalid edit keeps the previous config and is logged once. If the worktree
  is gone (its services are stopped by the next reconcile) or its config has
  never loaded, a listener that is already open keeps its bind, and a new one
  takes the main checkout's setting.
- `bind` is a config error unless the service is `on_demand` with a
  `port_range`; anywhere else the service binds its own port and the key could
  not take effect.
- A listener that fails to bind withdraws its readiness record, so `isola up`
  reports the failure instead of a port nothing listens on.

## Consequences

Physical devices can reach and wake opted-in services again. The exposure is
wider than the same services had when they bound their own ports:

- **Idle worktrees are reachable.** The daemon holds the port open for every
  worktree where the service is armed, not only where it runs, and a network
  peer's first bytes start that worktree's setup and command.
- **Peers look local.** The daemon dials the service from `127.0.0.1`, so the
  service cannot tell a network client from a local one. A service with
  `bind = "all"` must not grant extra trust to loopback peers (debug pages,
  admin routes, skipped auth).
- **IPv6 is included** even if the service used to bind IPv4 only.

Limiting network listeners to running services would remove the first point
but stop a phone from waking an idle service, which is the reason to use
on-demand services here; the opt-in is per service instead, and documented as
suited to trusted networks.

Proxy URLs (`proxy_port`) remain loopback-only. An older daemon ignores `bind`
until it is restarted; `isola up` warns when the running daemon is a different
build. On macOS with the firewall enabled, the `isola` binary, not the service,
is the process that must be allowed to accept incoming connections.
