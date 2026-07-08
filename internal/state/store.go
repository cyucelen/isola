package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cyucelen/isola/internal/logging"
)

const (
	// StatusRunning indicates a running service or proxy.
	StatusRunning = "running"
	// StatusStopped indicates a stopped service or proxy.
	StatusStopped = "stopped"
)

const lockTimeout = 10 * time.Second

// ServiceState represents the runtime state of a single service in a worktree.
type ServiceState struct {
	Port      int    `json:"port"`
	PID       int    `json:"pid"`
	Status    string `json:"status"` // StatusRunning, StatusStopped
	StartedAt string `json:"started_at"`
}

// ProxyState represents the runtime state of the reverse proxy.
type ProxyState struct {
	PID    int    `json:"pid"`
	Status string `json:"status"`
	HTTPS  bool   `json:"https,omitempty"`
}

// AccessoryState records a per-worktree resource isola provisioned (e.g. a
// database), so teardown knows exactly what it created and may drop. Handle is
// the driver's opaque record (e.g. {"database": "myapp_x"}) passed back to Drop.
type AccessoryState struct {
	Kind      string            `json:"kind"`   // driver kind, e.g. "postgres"
	Handle    map[string]string `json:"handle"` // driver-defined teardown record
	CreatedAt string            `json:"created_at"`
}

// State represents the full persisted state.
type State struct {
	// Services maps branch -> service name -> ServiceState.
	Services map[string]map[string]*ServiceState `json:"services"`
	Proxy    ProxyState                          `json:"proxy"`
	// PortAssignments maps "branch:service" -> port.
	PortAssignments map[string]int `json:"port_assignments"`
	// Accessories maps branch -> accessory name -> AccessoryState.
	Accessories map[string]map[string]*AccessoryState `json:"accessories,omitempty"`
}

// FileStore manages reading and writing state to a JSON file with file locking.
type FileStore struct {
	dir      string
	filePath string
	lockPath string
}

// NewFileStore creates a new FileStore. The dir is typically .isola/ under the main worktree root.
func NewFileStore(dir string) (*FileStore, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("creating state directory: %w", err)
	}
	return &FileStore{
		dir:      dir,
		filePath: filepath.Join(dir, "state.json"),
		lockPath: filepath.Join(dir, "state.lock"),
	}, nil
}

// Load reads the state from disk. Returns an empty state if the file doesn't exist.
func (s *FileStore) Load() (*State, error) {
	data, err := os.ReadFile(s.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return emptyState(), nil
		}
		return nil, fmt.Errorf("reading state: %w", err)
	}

	var st State
	if err := json.Unmarshal(data, &st); err != nil {
		logging.Warn("corrupt state file, starting fresh: %v", err)
		return emptyState(), nil
	}
	if st.Services == nil {
		st.Services = map[string]map[string]*ServiceState{}
	}
	if st.PortAssignments == nil {
		st.PortAssignments = map[string]int{}
	}
	if st.Accessories == nil {
		st.Accessories = map[string]map[string]*AccessoryState{}
	}
	return &st, nil
}

// Save writes the state to disk.
func (s *FileStore) Save(st *State) error {
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling state: %w", err)
	}
	return os.WriteFile(s.filePath, data, 0600)
}

// Dir returns the state directory path.
func (s *FileStore) Dir() string {
	return s.dir
}

// SetServiceState updates the state for a specific branch and service.
func SetServiceState(st *State, branch, service string, ss *ServiceState) {
	if st.Services[branch] == nil {
		st.Services[branch] = map[string]*ServiceState{}
	}
	st.Services[branch][service] = ss
}

// GetServiceState returns the state for a specific branch and service, or nil.
func GetServiceState(st *State, branch, service string) *ServiceState {
	if m, ok := st.Services[branch]; ok {
		return m[service]
	}
	return nil
}

// PortKey returns the state key for a branch+service port assignment.
func PortKey(branch, service string) string {
	return branch + ":" + service
}

// ParsePortKey splits a port key back into branch and service.
// Returns the original key as branch with an empty service if no separator is found.
func ParsePortKey(key string) (branch, service string) {
	branch, service, _ = strings.Cut(key, ":")
	return branch, service
}

// SetPortAssignment records a port assignment.
func SetPortAssignment(st *State, branch, service string, port int) {
	st.PortAssignments[PortKey(branch, service)] = port
}

// GetPortAssignment returns the assigned port, or 0 if not found.
func GetPortAssignment(st *State, branch, service string) int {
	return st.PortAssignments[PortKey(branch, service)]
}

// RunningServiceState creates a running ServiceState.
func RunningServiceState(port, pid int) *ServiceState {
	return &ServiceState{
		Port:      port,
		PID:       pid,
		Status:    StatusRunning,
		StartedAt: time.Now().Format(time.RFC3339),
	}
}

// StoppedServiceState creates a stopped ServiceState.
func StoppedServiceState(port int) *ServiceState {
	return &ServiceState{
		Port:   port,
		Status: StatusStopped,
	}
}

// OrphanedBranches returns branches present in state but not in the given set of active branches.
func OrphanedBranches(st *State, activeBranches map[string]bool) []string {
	var orphaned []string
	for branch := range st.Services {
		if !activeBranches[branch] {
			orphaned = append(orphaned, branch)
		}
	}
	return orphaned
}

func emptyState() *State {
	return &State{
		Services:        map[string]map[string]*ServiceState{},
		PortAssignments: map[string]int{},
		Accessories:     map[string]map[string]*AccessoryState{},
	}
}

// SetAccessoryState records the resource provisioned for a branch+accessory.
func SetAccessoryState(st *State, branch, accessory string, as *AccessoryState) {
	if st.Accessories == nil {
		st.Accessories = map[string]map[string]*AccessoryState{}
	}
	if st.Accessories[branch] == nil {
		st.Accessories[branch] = map[string]*AccessoryState{}
	}
	st.Accessories[branch][accessory] = as
}

// GetAccessoryState returns the recorded state for a branch+accessory, or nil.
func GetAccessoryState(st *State, branch, accessory string) *AccessoryState {
	if m, ok := st.Accessories[branch]; ok {
		return m[accessory]
	}
	return nil
}

// BranchAccessories returns the accessory records for a branch (nil if none).
func BranchAccessories(st *State, branch string) map[string]*AccessoryState {
	return st.Accessories[branch]
}

// RunningAccessoryState creates an AccessoryState for a freshly provisioned resource.
func RunningAccessoryState(kind string, handle map[string]string) *AccessoryState {
	return &AccessoryState{
		Kind:      kind,
		Handle:    handle,
		CreatedAt: time.Now().Format(time.RFC3339),
	}
}

// RecordAccessory persists the resource provisioned for a branch+accessory under
// the store lock.
func (s *FileStore) RecordAccessory(branch, accessory, kind string, handle map[string]string) error {
	return s.WithLock(func() error {
		st, err := s.Load()
		if err != nil {
			return err
		}
		SetAccessoryState(st, branch, accessory, RunningAccessoryState(kind, handle))
		return s.Save(st)
	})
}

// ForgetAccessory removes the recorded state for a branch+accessory under the
// store lock, pruning the branch entry when it becomes empty.
func (s *FileStore) ForgetAccessory(branch, accessory string) error {
	return s.WithLock(func() error {
		st, err := s.Load()
		if err != nil {
			return err
		}
		if st.Accessories[branch] != nil {
			delete(st.Accessories[branch], accessory)
			if len(st.Accessories[branch]) == 0 {
				delete(st.Accessories, branch)
			}
		}
		return s.Save(st)
	})
}
