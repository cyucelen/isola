package proxy

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/cyucelen/isola/internal/config"
	"github.com/cyucelen/isola/internal/git"
	"github.com/cyucelen/isola/internal/state"
)

// Resolver maps slug + proxy_port to real backend port.
type Resolver struct {
	// cfg is the main checkout's config, swapped in place when its file
	// changes, so in-flight on-demand starts (tracked in starts) are kept. It is
	// the fallback for per-service settings when a branch's worktree config is
	// unavailable.
	cfg    atomic.Pointer[config.Config]
	store  *state.FileStore
	mu     sync.Mutex
	starts map[string]*pendingStart
	// worktrees reads per-service settings from the worktree that owns the
	// service, so a branch that changes them applies them before it merges.
	worktrees *worktreeConfigs
}

// NewResolver creates a new Resolver.
func NewResolver(cfg *config.Config, store *state.FileStore) *Resolver {
	r := &Resolver{store: store, starts: make(map[string]*pendingStart), worktrees: newWorktreeConfigs(filepath.Dir(store.Dir()))}
	r.cfg.Store(cfg)
	return r
}

// branchConfig returns the config that governs branch's services: its
// worktree's (the last one that loaded, if a later edit is broken), or the main
// checkout's when that is unavailable. owned is false for the fallback.
func (r *Resolver) branchConfig(branch string) (cfg *config.Config, owned bool) {
	if _, cfg, _ := r.worktrees.lookup(branch, false); cfg != nil {
		return cfg, true
	}
	return r.cfg.Load(), false
}

// bindsAll reports whether branch's service listens on every interface, as set
// in the worktree that has branch checked out. It reads the live config, so an
// edit rebinds on the next sync. owned is false when the value comes from the
// main checkout because the worktree's config is unavailable.
func (r *Resolver) bindsAll(branch, service string) (all, owned bool) {
	cfg, owned := r.branchConfig(branch)
	return cfg.Services[service].BindsAll(), owned
}

// setConfig replaces the main checkout's config.
func (r *Resolver) setConfig(cfg *config.Config) { r.cfg.Store(cfg) }

// Resolve returns the real backend port for a slug and proxy port.
func (r *Resolver) Resolve(slug string, proxyPort int) (int, error) {
	_, _, port, err := r.resolveTarget(slug, proxyPort)
	return port, err
}

func (r *Resolver) resolveTarget(slug string, proxyPort int) (string, string, int, error) {
	// Resolve branch and port from one snapshot to avoid inconsistency.
	st, err := r.store.Snapshot()
	if err != nil {
		return "", "", 0, err
	}
	// Find the branch whose host label matches the one in the Host header.
	// It must be derived exactly as the printed and injected URLs derive it,
	// or a worktree whose label was shortened would never be found.
	branch := ""
	for key := range st.PortAssignments {
		parts := strings.SplitN(key, ":", 2)
		if len(parts) == 2 && git.HostLabel(parts[0]) == slug {
			branch = parts[0]
			break
		}
	}
	if branch == "" {
		return "", "", 0, fmt.Errorf("no worktree found for slug %q", slug)
	}

	// Find which of the branch's services uses this proxy port, by the
	// branch's own config, so a port a branch adds or moves routes to it.
	cfg, _ := r.branchConfig(branch)
	serviceName := ""
	for name, svc := range cfg.Services {
		if svc.ProxyPort == proxyPort {
			serviceName = name
			break
		}
	}
	if serviceName == "" {
		return "", "", 0, fmt.Errorf("no service configured for proxy_port %d", proxyPort)
	}

	port := state.GetPortAssignment(st, branch, serviceName)
	if port == 0 {
		return "", "", 0, fmt.Errorf("no port assigned for %s/%s (slug: %s)", branch, serviceName, slug)
	}
	return branch, serviceName, port, nil
}

// ParseHost splits a Host header into slug and project for the qualified scheme
// "<slug>.<project>.localhost[:port]". A bare "<slug>.localhost" yields an empty
// project; "localhost" (or a non-.localhost host) yields both empty. Slugs and
// project names are single DNS labels, so the label before ".localhost" is the
// project and the one before that is the slug.
func ParseHost(host string) (slug, project string) {
	h := host
	if idx := strings.LastIndex(h, ":"); idx != -1 {
		h = h[:idx]
	}
	if !strings.HasSuffix(h, ".localhost") {
		return "", ""
	}
	rest := strings.TrimSuffix(h, ".localhost")
	if rest == "" {
		return "", ""
	}
	labels := strings.Split(rest, ".")
	if len(labels) == 1 {
		return labels[0], "" // bare <slug>.localhost
	}
	return strings.Join(labels[:len(labels)-1], "."), labels[len(labels)-1]
}
