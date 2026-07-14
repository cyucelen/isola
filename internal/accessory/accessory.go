// Package accessory models per-worktree stateful dependencies (databases,
// caches, ...) that isola provisions alongside dev servers. Each accessory is
// backed by a driver keyed on its config "kind"; drivers self-register here so
// adding a kind touches no shared code. See docs/adr/005-accessories.md.
package accessory

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/cyucelen/isola/internal/config"
	"github.com/cyucelen/isola/internal/expand"
	"github.com/cyucelen/isola/internal/git"
)

// URLWithPath returns base with its path replaced by "/"+seg, without mutating
// the shared parsed URL. Drivers use it to point a server URL at a per-worktree
// database or logical DB index.
func URLWithPath(base *url.URL, seg string) string {
	u := *base
	u.Path = "/" + seg
	return u.String()
}

// OpTimeout bounds a single accessory operation (provision/reset/drop). Callers
// wrap the context with it so every Kind inherits a uniform deadline instead of
// each driver implementing its own. Generous, because cloning a Template can be
// a physical copy.
const OpTimeout = 10 * time.Minute

// WorktreeInfo carries the per-worktree identity a driver needs to derive
// resource names and connection strings.
type WorktreeInfo struct {
	Project string // repo's project name, namespaces cross-project resources
	Branch  string // e.g. "feature/auth"
	Slug    string // URL-safe slug, e.g. "feature-auth"
}

// FromWorktree builds a WorktreeInfo from a git worktree and its project name.
func FromWorktree(wt *git.Worktree, project string) WorktreeInfo {
	return WorktreeInfo{Project: project, Branch: wt.Branch, Slug: wt.Slug()}
}

// Expand interpolates ${VAR} references in s using the worktree's identity
// (ISOLA_BRANCH, ISOLA_BRANCH_SLUG), any extra vars, then the process
// environment. It matches service env expansion byte-for-byte.
func (wt WorktreeInfo) Expand(s string, extra map[string]string) string {
	return expand.Braces(s, func(name string) string {
		switch name {
		case "ISOLA_BRANCH":
			return wt.Branch
		case "ISOLA_BRANCH_SLUG":
			return wt.Slug
		}
		if v, ok := extra[name]; ok {
			return v
		}
		return os.Getenv(name)
	})
}

// Provisioned is the outcome of provisioning an accessory for a worktree.
type Provisioned struct {
	// Handle is the opaque, driver-defined record of what was created. It is
	// persisted in state and handed back to Drop, so teardown never depends on
	// re-reading config and multi-resource drivers are supported.
	Handle map[string]string
	// URL is the connection string this accessory exposes for the worktree.
	// Services reference it explicitly as ${accessories.<name>.url}.
	URL string
}

// Accessory is a driver instance bound to one [accessories.<name>] config entry.
// The core contract is Provision + Drop; Reset is the optional Resettable
// capability, since only Kinds with a Template have a baseline to reset to.
type Accessory interface {
	// Name is the config key (e.g. "primary").
	Name() string
	// Kind is the driver discriminator (e.g. "postgres").
	Kind() string
	// Provision creates the per-worktree resource, reusing it if already
	// present, and returns its Handle plus the connection URL to expose (as
	// ${accessories.<name>.url}).
	Provision(ctx context.Context, wt WorktreeInfo) (Provisioned, error)
	// Drop tears down the resource identified by the persisted Handle. It does
	// not need a live worktree, so it is safe to call on prune.
	Drop(ctx context.Context, handle map[string]string) error
}

// Resettable is implemented by Kinds that can restore a worktree's resource to
// its Template baseline. Kinds without a Template do not implement it.
type Resettable interface {
	Reset(ctx context.Context, wt WorktreeInfo) (Provisioned, error)
}

// Decoder decodes a driver's config fields into a driver-owned struct. It is
// handed to factories so each driver owns its schema without the config or
// accessory packages knowing its fields.
type Decoder func(interface{}) error

// Factory builds a driver instance from its config name and a Decoder.
type Factory func(name string, dec Decoder) (Accessory, error)

var registry = map[string]Factory{}

// Register makes a driver available under kind. Drivers call this from init().
// Registering the same kind twice panics, as it indicates a build-time mistake.
func Register(kind string, f Factory) {
	if _, dup := registry[kind]; dup {
		panic(fmt.Sprintf("accessory: kind %q registered twice", kind))
	}
	registry[kind] = f
}

// kinds returns the registered kinds, sorted.
func kinds() []string {
	ks := make([]string, 0, len(registry))
	for k := range registry {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

// BuildAll instantiates every accessory declared in cfg, decoding each one's
// driver-specific fields lazily. The returned map is keyed by config name.
func BuildAll(cfg *config.Config) (map[string]Accessory, error) {
	out := make(map[string]Accessory, len(cfg.Accessories))
	for name, prim := range cfg.Accessories {
		kind, err := cfg.AccessoryKind(prim)
		if err != nil {
			return nil, fmt.Errorf("accessory %q: reading kind: %w", name, err)
		}
		f, ok := registry[kind]
		if !ok {
			if strings.Contains(kind, "/") {
				return nil, fmt.Errorf("accessory %q: third-party driver kind %q is not supported yet", name, kind)
			}
			return nil, fmt.Errorf("accessory %q: unknown kind %q (known kinds: %v)", name, kind, kinds())
		}
		prim := prim
		dec := func(v interface{}) error { return cfg.Meta.PrimitiveDecode(prim, v) }
		a, err := f(name, dec)
		if err != nil {
			return nil, fmt.Errorf("accessory %q: %w", name, err)
		}
		out[name] = a
	}
	return out, nil
}
