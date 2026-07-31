package proxy

import (
	"testing"

	"github.com/cyucelen/isola/internal/config"
	"github.com/cyucelen/isola/internal/git"
	"github.com/cyucelen/isola/internal/state"
)

// --- State-backed tests ---

func setupResolver(t *testing.T) (*Resolver, *state.FileStore) {
	t.Helper()
	dir := t.TempDir()
	store, err := state.NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		Services: map[string]config.ServiceConfig{
			"web": {
				Command:   "npm start",
				PortRange: config.PortRange{Min: 3100, Max: 3199},
				ProxyPort: 3000,
			},
		},
		Worktrees: map[string]config.WTOverride{},
	}

	// Set up state with a known port assignment
	st := &state.State{
		Services:        map[string]map[string]*state.ServiceState{},
		PortAssignments: map[string]int{},
	}
	state.SetPortAssignment(st, "feature/auth", "web", 3150)
	if err := store.Save(st); err != nil {
		t.Fatal(err)
	}

	return NewResolver(cfg, store), store
}

func TestResolverResolve(t *testing.T) {
	resolver, _ := setupResolver(t)

	port, err := resolver.Resolve("feature-auth", 3000)
	if err != nil {
		t.Fatalf("Resolve() error: %v", err)
	}
	if port != 3150 {
		t.Errorf("Resolve() = %d, want 3150", port)
	}
}

// TestResolverResolvesFittedHostLabel is the routing half of the long-branch
// regression: the Host header can only carry the fitted label (a browser will not
// resolve a longer one), so the resolver has to match on the same derivation the
// printed URL uses. Matching on the raw slug would leave the worktree
// unroutable — services up, every URL a 404 from the proxy.
func TestResolverResolvesFittedHostLabel(t *testing.T) {
	const branch = "dependabot/npm_and_yarn/services/manager-dashboard/ai-sdk/react-4.0.40"
	dir := t.TempDir()
	store, err := state.NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Services: map[string]config.ServiceConfig{
			"web": {Command: "npm start", PortRange: config.PortRange{Min: 3100, Max: 3199}, ProxyPort: 3000},
		},
		Worktrees: map[string]config.WTOverride{},
	}
	st := &state.State{
		Services:        map[string]map[string]*state.ServiceState{},
		PortAssignments: map[string]int{},
	}
	state.SetPortAssignment(st, branch, "web", 3151)
	if err := store.Save(st); err != nil {
		t.Fatal(err)
	}
	resolver := NewResolver(cfg, store)

	label := git.HostLabel(branch)
	if len(label) > 63 {
		t.Fatalf("host label %q is %d bytes; it could never reach the proxy", label, len(label))
	}
	port, err := resolver.Resolve(label, 3000)
	if err != nil {
		t.Fatalf("Resolve(%q) error: %v", label, err)
	}
	if port != 3151 {
		t.Errorf("Resolve(%q) = %d, want 3151", label, port)
	}

	// The unfitted slug is not a host anything can ask for, so it must not resolve.
	if _, err := resolver.Resolve(git.BranchSlug(branch), 3000); err == nil {
		t.Error("the over-long slug should not resolve; only the fitted label is routable")
	}
}

func TestResolverResolveUnknownSlug(t *testing.T) {
	resolver, _ := setupResolver(t)

	_, err := resolver.Resolve("unknown-slug", 3000)
	if err == nil {
		t.Fatal("Resolve() expected error for unknown slug")
	}
}

func TestParseHost(t *testing.T) {
	cases := []struct {
		host, slug, project string
	}{
		{"main.projA.localhost:3000", "main", "projA"},
		{"feature-auth.projA.localhost:8000", "feature-auth", "projA"},
		{"main.localhost:3000", "main", ""}, // bare: no project
		{"localhost:3000", "", ""},
		{"example.com", "", ""},
		{"main.projA.localhost", "main", "projA"}, // no port
	}
	for _, c := range cases {
		slug, project := ParseHost(c.host)
		if slug != c.slug || project != c.project {
			t.Errorf("ParseHost(%q) = (%q, %q), want (%q, %q)", c.host, slug, project, c.slug, c.project)
		}
	}
}
