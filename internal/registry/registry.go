// Package registry maintains the machine-wide list of isola projects so the
// single shared proxy can route across all of them. Each project registers the
// state dir it serves and the proxy ports it uses; the proxy reads this to bind
// ports and resolve <branch>.<project>.localhost requests. It stores no backend
// ports: those are resolved live from each project's own state. See
// docs/adr/006-shared-proxy.md.
package registry

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

const (
	formatVersion = 1
	lockTimeout   = 10 * time.Second
)

// Project is one registered isola repo.
type Project struct {
	Name       string `json:"name"`
	StateDir   string `json:"state_dir"` // the repo's .isola directory
	ProxyPorts []int  `json:"proxy_ports"`
	HTTPS      bool   `json:"https"` // this project wants HTTPS on its ports
}

// Daemon records the single machine-wide proxy process.
type Daemon struct {
	PID     int  `json:"pid"`
	Version int  `json:"version"`
	Running bool `json:"running"`
}

type data struct {
	Version  int       `json:"version"`
	Projects []Project `json:"projects"`
}

// Store is the file-backed, lock-guarded machine-wide registry.
type Store struct {
	dir      string
	filePath string
	lockPath string
}

// GlobalDir is the per-user directory holding the registry and daemon state.
func GlobalDir() (string, error) {
	cfgDir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("locating user config dir: %w", err)
	}
	return filepath.Join(cfgDir, "isola"), nil
}

// Open creates (if needed) the global dir and returns a Store.
func Open() (*Store, error) {
	dir, err := GlobalDir()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("creating global dir: %w", err)
	}
	return &Store{
		dir:      dir,
		filePath: filepath.Join(dir, "registry.json"),
		lockPath: filepath.Join(dir, "registry.lock"),
	}, nil
}

// Dir returns the global directory path.
func (s *Store) Dir() string { return s.dir }

func (s *Store) load() (*data, error) {
	b, err := os.ReadFile(s.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return &data{Version: formatVersion}, nil
		}
		return nil, fmt.Errorf("reading registry: %w", err)
	}
	var d data
	if err := json.Unmarshal(b, &d); err != nil {
		// A corrupt registry should not wedge every project; start fresh.
		return &data{Version: formatVersion}, nil
	}
	return &d, nil
}

func (s *Store) save(d *data) error {
	d.Version = formatVersion
	b, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling registry: %w", err)
	}
	return os.WriteFile(s.filePath, b, 0600)
}

// prune drops entries whose state dir no longer exists on disk.
func prune(projects []Project) []Project {
	kept := projects[:0]
	for _, p := range projects {
		if _, err := os.Stat(p.StateDir); err == nil {
			kept = append(kept, p)
		}
	}
	return kept
}

// Register adds or refreshes this project's entry. A name already bound to a
// different state dir is a clash and returns an error asking the user to set a
// unique `project`.
func (s *Store) Register(p Project) error {
	return s.withLock(func() error {
		d, err := s.load()
		if err != nil {
			return err
		}
		var out []Project
		for _, e := range prune(d.Projects) {
			if e.StateDir == p.StateDir {
				continue // replace our own prior entry
			}
			if e.Name == p.Name {
				return fmt.Errorf("project name %q is already used by %s; set a unique `project` in .isola.toml", p.Name, e.StateDir)
			}
			out = append(out, e)
		}
		out = append(out, p)
		d.Projects = out
		return s.save(d)
	})
}

// Deregister removes the entry for stateDir, if present.
func (s *Store) Deregister(stateDir string) error {
	return s.withLock(func() error {
		d, err := s.load()
		if err != nil {
			return err
		}
		kept := prune(d.Projects)
		out := kept[:0]
		for _, e := range kept {
			if e.StateDir != stateDir {
				out = append(out, e)
			}
		}
		d.Projects = out
		return s.save(d)
	})
}

// List returns the registered projects (stale entries pruned).
func (s *Store) List() ([]Project, error) {
	var projects []Project
	err := s.withLock(func() error {
		d, err := s.load()
		if err != nil {
			return err
		}
		projects = prune(d.Projects)
		return nil
	})
	return projects, err
}

// Lookup returns the registered project with the given name.
func (s *Store) Lookup(name string) (Project, bool, error) {
	projects, err := s.List()
	if err != nil {
		return Project{}, false, err
	}
	for _, p := range projects {
		if p.Name == name {
			return p, true, nil
		}
	}
	return Project{}, false, nil
}

func (s *Store) daemonPath() string { return filepath.Join(s.dir, "proxy.json") }

// GetDaemon returns the recorded machine-wide daemon state.
func (s *Store) GetDaemon() (Daemon, error) {
	var d Daemon
	err := s.withLock(func() error {
		b, err := os.ReadFile(s.daemonPath())
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		return json.Unmarshal(b, &d)
	})
	return d, err
}

// SetDaemon records the machine-wide daemon state.
func (s *Store) SetDaemon(d Daemon) error {
	d.Version = formatVersion
	return s.withLock(func() error {
		b, err := json.MarshalIndent(d, "", "  ")
		if err != nil {
			return err
		}
		return os.WriteFile(s.daemonPath(), b, 0600)
	})
}

// HTTPSByPort returns, for each registered proxy port, whether any project using
// it wants HTTPS. A port with mixed settings resolves to HTTPS (the superset).
func (s *Store) HTTPSByPort() (map[int]bool, error) {
	projects, err := s.List()
	if err != nil {
		return nil, err
	}
	m := map[int]bool{}
	for _, p := range projects {
		for _, port := range p.ProxyPorts {
			if p.HTTPS {
				m[port] = true
			} else if _, ok := m[port]; !ok {
				m[port] = false
			}
		}
	}
	return m, nil
}

// Ports returns the sorted union of proxy ports across all registered projects.
func (s *Store) Ports() ([]int, error) {
	projects, err := s.List()
	if err != nil {
		return nil, err
	}
	seen := map[int]bool{}
	var ports []int
	for _, p := range projects {
		for _, port := range p.ProxyPorts {
			if !seen[port] {
				seen[port] = true
				ports = append(ports, port)
			}
		}
	}
	sort.Ints(ports)
	return ports, nil
}
