# isola

isola runs and isolates per-git-worktree development environments: each worktree
gets its own dev-server processes (with stable ports and `*.localhost` routing)
and its own stateful dependencies. This glossary fixes the language the codebase,
CLI, and docs must use consistently.

## Language

### Core

**Worktree**:
A git worktree — isola's unit of isolation. Each worktree gets its own services,
ports, URLs, and accessories.
_Avoid_: branch (a worktree tracks a branch but is not the same thing)

**Service**:
A dev-server process isola runs for a worktree, declared under `[services]`.
_Avoid_: app, server (bare), process

**Accessory**:
An isolated, per-worktree stateful dependency (database, cache, …) that isola
provisions so each worktree runs against its own copy. The general concept that
`[accessories]` configures and the `isola accessory` command operates on.
_Avoid_: db, database (that's one kind), addon, resource, dependency, sidecar, service

### Accessory model

**Kind**:
The discriminator that selects an accessory's driver (e.g. `postgres`). Built-in
kinds use bare names; third-party kinds are namespaced `vendor/name`.
_Avoid_: type

**Driver**:
The implementation behind an accessory Kind.
_Avoid_: plugin, provider, backend

**Template**:
The seed database/state an accessory copies from when provisioning a worktree's
resource (the `clone_from` source). Kept quiescent — never run against.
_Avoid_: source, base, seed, master

**Handle**:
The opaque, driver-defined record of what provisioning created for a worktree,
persisted so isola can later drop (or reset) the resource without re-reading
config. isola only ever tears down resources it holds a Handle for.
_Avoid_: resource id, ref

### Accessory lifecycle (canonical verbs)

**Provision**:
Create — or reuse if already present — a worktree's accessory resource and expose
it to that worktree's services.
_Avoid_: clone, create, setup

**Reset**:
Restore a worktree's accessory resource to its Template baseline.
_Avoid_: refresh, reseed, rebuild

**Drop**:
Tear down a worktree's accessory resource.
_Avoid_: delete, destroy, remove, teardown

## Multi-project (shared proxy)

**Project**:
A single isola-managed repository, identified by the `project` name in
`.isola.toml` (default: the main-worktree directory basename). Namespaces
routing and Redis ownership so multiple repos coexist on one machine.
_Avoid_: app, service, workspace, repo (repo is the VCS artifact; the Project is
the isola-managed unit)

**Registry**:
The machine-wide list of registered Projects (`~/.isola/registry.json`):
`{project, stateDir, proxyPorts}` per Project. The shared proxy reads it to route
across Projects; it stores no backend ports (those are resolved live from each
Project's state).
_Avoid_: index, catalog, directory

**Daemon**:
The single machine-wide reverse proxy process serving every Project, bound to the
union of all Projects' proxy ports. Distinct from a per-repo proxy.
_Avoid_: server, service, gateway

**Qualified URL**:
A project-qualified address `<branch-slug>.<project>.localhost:<port>`. Always
required; the bare `<branch-slug>.localhost` form is not routed.
_Avoid_: subdomain, vanity URL
