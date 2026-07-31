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
	"github.com/cyucelen/isola/internal/slug"
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
// The Slug is the unbounded branch slug, not the worktree's host label: a
// resource name is fitted to its own budget by ExpandWithin, and hashing the
// full slug there keeps names stable and maximally distinguishing. Fitting the
// input twice (to 63 for a hostname, then again for the name) would both lose
// readable characters and rename every resource provisioned before this.
func FromWorktree(wt *git.Worktree, project string) WorktreeInfo {
	return WorktreeInfo{Project: project, Branch: wt.Branch, Slug: git.BranchSlug(wt.Branch)}
}

// Expand interpolates ${VAR} references in s using the worktree's identity
// (ISOLA_BRANCH, ISOLA_BRANCH_SLUG), any extra vars, then the process
// environment. It matches service env expansion byte-for-byte.
func (wt WorktreeInfo) Expand(s string, extra map[string]string) string {
	return wt.expand(s, extra, func() string { return wt.Slug })
}

// ExpandWithin expands s like Expand but guarantees the result fits maxBytes,
// shortening the ${ISOLA_BRANCH_SLUG} substitution with slug.Fit when the full
// expansion would overflow. Drivers use it wherever the thing being named has a
// length limit (a Postgres identifier's 63 bytes, say); each passes its own
// budget, since the limits differ per resource.
//
// A name that already fits is returned byte-identical to Expand, so worktrees
// keep the resources they already have. When even a fitted slug cannot make the
// name fit — the template's fixed text alone is too long, or there is no slug in
// it to shorten — ExpandWithin reports that instead of returning an over-long
// name: truncating to the limit is what would hand two worktrees the same
// resource, and Postgres in particular truncates over-long identifiers itself
// with only a NOTICE.
func (wt WorktreeInfo) ExpandWithin(s string, maxBytes int, extra map[string]string) (string, error) {
	full := wt.Expand(s, extra)
	if len(full) <= maxBytes {
		return full, nil
	}

	// Measure everything the template contributes apart from the slug, and how
	// many times the slug appears, by expanding it to nothing.
	slugRefs := 0
	fixed := len(wt.expand(s, extra, func() string { slugRefs++; return "" }))
	if slugRefs == 0 {
		return "", fmt.Errorf("%q resolves to %d bytes, over the %d-byte limit, and has no ${ISOLA_BRANCH_SLUG} to shorten", s, len(full), maxBytes)
	}
	if fixed >= maxBytes {
		return "", fmt.Errorf("%q needs %d bytes before ${ISOLA_BRANCH_SLUG} is substituted, over the %d-byte limit; shorten the template", s, fixed, maxBytes)
	}
	budget := (maxBytes - fixed) / slugRefs
	if budget < slug.MinFit {
		return "", fmt.Errorf("%q leaves only %d of the %d-byte limit for ${ISOLA_BRANCH_SLUG} (needs at least %d); shorten the template", s, budget, maxBytes, slug.MinFit)
	}

	fitted := wt.expand(s, extra, func() string { return slug.Fit(wt.Slug, budget) })
	if len(fitted) > maxBytes {
		return "", fmt.Errorf("%q resolves to %d bytes even with a shortened branch slug, over the %d-byte limit", s, len(fitted), maxBytes)
	}
	return fitted, nil
}

// expand is Expand with the ISOLA_BRANCH_SLUG substitution supplied by the
// caller, so ExpandWithin can measure the rest of the template and count the
// slug's occurrences without duplicating the resolution rules.
func (wt WorktreeInfo) expand(s string, extra map[string]string, branchSlug func() string) string {
	return expand.Braces(s, func(name string) string {
		switch name {
		case "ISOLA_BRANCH":
			return wt.Branch
		case "ISOLA_BRANCH_SLUG":
			return branchSlug()
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

// Resource is the structured view of a Handle, for machine-readable output
// (`isola accessory ls --json`). A Handle is all strings because state is; a
// Resource restores the types a consumer expects — a Redis logical database is a
// number — so nothing has to be parsed back out of a display string.
type Resource map[string]any

// ResourceShaper renders a persisted Handle as a Resource. It returns an error
// when the Handle does not hold what its kind expects, so a caller can report the
// record as unreadable rather than emit a half-built resource.
type ResourceShaper func(handle map[string]string) (Resource, error)

var resourceShapers = map[string]ResourceShaper{}

// RegisterResource declares how kind renders its Handle as structured data.
// Drivers call it from init() alongside Register, so a kind owns the shape of its
// own JSON and no shared code switches on kind names. Registering the same kind
// twice panics, as it indicates a build-time mistake.
func RegisterResource(kind string, f ResourceShaper) {
	if _, dup := resourceShapers[kind]; dup {
		panic(fmt.Sprintf("accessory: resource shape for kind %q registered twice", kind))
	}
	resourceShapers[kind] = f
}

// DescribeResource returns the structured form of a recorded Handle. It reports an
// error for a kind that declared no shape (a third-party driver, or one this build
// no longer has) as well as for a Handle its own shaper rejects, so a caller can
// say so instead of guessing at the fields.
func DescribeResource(kind string, handle map[string]string) (Resource, error) {
	f, ok := resourceShapers[kind]
	if !ok {
		return nil, fmt.Errorf("kind %q declares no resource shape", kind)
	}
	return f(handle)
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
