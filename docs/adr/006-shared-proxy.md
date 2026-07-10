# ADR-006: Shared Machine-Wide Reverse Proxy for Multi-Project Routing

## Status

Accepted

## Context

isola's reverse proxy (ADR-003) is scoped to a single repository: `isola up`
auto-starts a proxy that binds each configured `proxy_port` (e.g. `:3000`) and
routes `<branch-slug>.localhost:<port>` to that repo's worktree backends, using
that repo's state to resolve slugs.

A `proxy_port` is a machine-wide OS resource, but the proxy only knows one repo.
So when a second isola project runs on the same machine and uses the same
`proxy_port`:

- Only one proxy can bind `:3000`. The second project's proxy cannot start.
  (ADR-003's launcher now detects this and fails with a clear message rather
  than a misleading timeout, but the two projects still cannot coexist on the
  port.)
- Even if they could share the listener, a proxy started by project A resolves
  slugs against A's state only, so B's `main.localhost:3000` would 404.
- `main.localhost:3000` is inherently ambiguous once two projects both have a
  `main` branch on the same port.

Other machine-wide resources leak across projects too: two projects sharing one
Redis server allocate logical DBs keyed only on the branch slug, so two projects
that both have `main` silently share one logical DB (data bleed). Service
`port_range`s can also overlap across projects, but that is config-controlled
and out of scope here.

We want multiple isola projects to coexist on one machine with stable,
predictable URLs and no cross-project data bleed.

## Decision

**One shared machine-wide proxy, project-qualified routing.**

1. **Project identity.** Each repo has a `project` name, set via `project` in
   `.isola.toml`, defaulting to the main-worktree directory basename. A basename
   clash between two repos is detected and requires an explicit `project`. This
   name namespaces both proxy routing and Redis ownership.

2. **Global registry.** `~/.isola/registry.json` (XDG-aware), file-locked like
   the per-repo state. It stores only a list of registered projects:
   `{project, stateDir, proxyPorts[]}`. It does **not** cache backend ports; the
   daemon resolves live by loading each project's own state per request, reusing
   the existing per-repo resolver, so routing is always fresh. `isola up`
   registers/refreshes its entry; `down`/prune deregisters; entries whose
   `stateDir` no longer exists are pruned lazily.

3. **Machine-wide daemon.** The first `isola up` that needs a proxy starts a
   detached, repo-independent proxy. It listens on the union of all registered
   projects' `proxy_port`s and opens new listeners as projects register new
   ports. Global daemon state lives in `~/.isola/proxy.json` (PID, ports, format
   version). `isola proxy stop` stops the machine-wide daemon and warns that it
   affects every project. On a format-version mismatch, a newer isola restarts
   the daemon rather than talking to an older one.

4. **Routing scheme.** Requests are always project-qualified:
   `<branch-slug>.<project>.localhost:<port>`. The daemon parses project and
   slug from the Host header, finds the project's `stateDir` in the registry,
   loads its state, resolves the slug to a backend port, and proxies. A bare
   `<branch-slug>.localhost:<port>` returns 404 with a hint to use the qualified
   form. URLs never change based on what else is running.

5. **Redis ownership.** The per-worktree Redis logical-DB owner marker becomes
   `<project>:<slug>` instead of `<slug>`, so a shared Redis server never bleeds
   across projects.

## Consequences

Positive:

- Any number of isola projects coexist on one machine and one set of
  `proxy_port`s. No per-project port coordination.
- URLs are stable and unambiguous: a project's URLs never change when another
  project starts or stops.
- Redis isolation holds across projects on a shared server.
- The registry stays small and never stale-caches routing, because backend
  resolution reads each project's live state.

Negative / costs:

- Single-project URLs get longer: `main.localhost:3000` becomes
  `main.projA.localhost:3000`. The bare form no longer routes.
- The proxy is now a shared machine service: a daemon crash affects every
  project, and daemon lifecycle (start, stop, version upgrades) must be managed
  globally rather than per repo.
- `<branch>.<project>.localhost` is a three-label `.localhost` name; modern
  Chrome and Firefox resolve it, but some older setups (e.g. Safari without a
  resolver helper) may need configuration.
- `isola proxy stop` is now machine-wide, a behavior change from the per-repo
  proxy.
