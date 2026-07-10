package proxy

import (
	"fmt"
	"strings"

	"github.com/cyucelen/isola/internal/config"
	"github.com/cyucelen/isola/internal/git"
	"github.com/cyucelen/isola/internal/state"
)

// Resolver maps slug + proxy_port to real backend port.
type Resolver struct {
	cfg   *config.Config
	store *state.FileStore
}

// NewResolver creates a new Resolver.
func NewResolver(cfg *config.Config, store *state.FileStore) *Resolver {
	return &Resolver{cfg: cfg, store: store}
}

// Resolve returns the real backend port for a slug and proxy port.
func (r *Resolver) Resolve(slug string, proxyPort int) (int, error) {
	// Find which service uses this proxy port.
	serviceName := ""
	for name, svc := range r.cfg.Services {
		if svc.ProxyPort == proxyPort {
			serviceName = name
			break
		}
	}
	if serviceName == "" {
		return 0, fmt.Errorf("no service configured for proxy_port %d", proxyPort)
	}

	// Resolve branch and port in a single lock to avoid inconsistency.
	var branch string
	var port int
	if err := r.store.WithLock(func() error {
		st, e := r.store.Load()
		if e != nil {
			return e
		}
		// Find the branch that matches this slug.
		for key := range st.PortAssignments {
			parts := strings.SplitN(key, ":", 2)
			if len(parts) == 2 && git.BranchSlug(parts[0]) == slug {
				branch = parts[0]
				break
			}
		}
		if branch == "" {
			return fmt.Errorf("no worktree found for slug %q", slug)
		}
		port = state.GetPortAssignment(st, branch, serviceName)
		return nil
	}); err != nil {
		return 0, err
	}

	if port == 0 {
		return 0, fmt.Errorf("no port assigned for %s/%s (slug: %s)", branch, serviceName, slug)
	}
	return port, nil
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
