package port

import (
	"github.com/cyucelen/isola/internal/config"
	"github.com/cyucelen/isola/internal/state"
)

// Registry manages port assignments backed by state.
type Registry struct {
	store *state.FileStore
	cfg   *config.Config
}

// NewRegistry creates a new port Registry.
func NewRegistry(store *state.FileStore, cfg *config.Config) *Registry {
	return &Registry{store: store, cfg: cfg}
}

// AssignPort allocates a port for the given branch and service.
// If any port was previously assigned, it is reused.
func (r *Registry) AssignPort(branch, service string) (int, error) {
	var port int
	err := r.store.WithLock(func() error {
		st, err := r.store.Load()
		if err != nil {
			return err
		}

		// Check for existing assignment.
		existing := state.GetPortAssignment(st, branch, service)
		if existing > 0 {
			port = existing
			return nil
		}

		// Check for fixed port override.
		fixedPort := r.cfg.FixedPortForBranch(service, branch)

		// Build used ports set.
		used := make(map[int]bool, len(st.PortAssignments))
		for _, p := range st.PortAssignments {
			used[p] = true
		}

		svc := r.cfg.Services[service]
		allocated, err := Allocate(branch, service, svc, fixedPort, used)
		if err != nil {
			return err
		}

		state.SetPortAssignment(st, branch, service, allocated)
		port = allocated
		return r.store.Save(st)
	})
	return port, err
}
