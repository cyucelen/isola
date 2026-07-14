package port

import (
	"testing"

	"github.com/cyucelen/isola/internal/config"
	"github.com/cyucelen/isola/internal/state"
)

func newTestRegistry(t *testing.T) *Registry {
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

	return NewRegistry(store, cfg)
}

func TestRegistryAssignPort(t *testing.T) {
	t.Run("port in range", func(t *testing.T) {
		reg := newTestRegistry(t)
		port, err := reg.AssignPort("main", "web")
		if err != nil {
			t.Fatalf("AssignPort() error: %v", err)
		}
		if port < 3100 || port > 3199 {
			t.Errorf("AssignPort() = %d, not in [3100, 3199]", port)
		}
	})

	t.Run("idempotent", func(t *testing.T) {
		reg := newTestRegistry(t)
		first, err := reg.AssignPort("main", "web")
		if err != nil {
			t.Fatal(err)
		}
		second, err := reg.AssignPort("main", "web")
		if err != nil {
			t.Fatal(err)
		}
		if first != second {
			t.Errorf("AssignPort not idempotent: %d != %d", first, second)
		}
	})

	t.Run("different pairs differ", func(t *testing.T) {
		reg := newTestRegistry(t)
		a, err := reg.AssignPort("main", "web")
		if err != nil {
			t.Fatal(err)
		}
		b, err := reg.AssignPort("feature/auth", "web")
		if err != nil {
			t.Fatal(err)
		}
		if a == b {
			t.Errorf("different branches got same port %d", a)
		}
	})
}

func TestRegistryReassignReturnsSamePort(t *testing.T) {
	reg := newTestRegistry(t)

	assigned, err := reg.AssignPort("main", "web")
	if err != nil {
		t.Fatal(err)
	}
	if assigned == 0 {
		t.Fatal("AssignPort returned 0")
	}

	// A second AssignPort for the same branch+service reuses the assignment.
	again, err := reg.AssignPort("main", "web")
	if err != nil {
		t.Fatalf("second AssignPort error: %v", err)
	}
	if again != assigned {
		t.Errorf("re-assign = %d, want %d (should reuse)", again, assigned)
	}
}
