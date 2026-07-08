package process

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/cyucelen/isola/internal/accessory"
	"github.com/cyucelen/isola/internal/config"
	"github.com/cyucelen/isola/internal/git"
	"github.com/cyucelen/isola/internal/logging"
	"github.com/cyucelen/isola/internal/port"
	"github.com/cyucelen/isola/internal/state"
)

// Manager coordinates starting and stopping services across worktrees.
type Manager struct {
	cfg      *config.Config
	store    *state.FileStore
	registry *port.Registry
	mu       sync.RWMutex
	runners  map[string]*Runner // key: "branch:service"
	// accessoryEnv caches the injected env of a branch's accessories once they
	// have been successfully provisioned in this Manager's lifetime, so repeated
	// StartServices calls (e.g. per-service restarts from the TUI) don't
	// re-provision — a CLI `up` gets one Manager per run, the TUI one per session.
	accessoryEnv map[string]map[string]string // branch -> injected env
}

// NewManager creates a new process Manager.
func NewManager(cfg *config.Config, store *state.FileStore, registry *port.Registry) *Manager {
	return &Manager{
		cfg:          cfg,
		store:        store,
		registry:     registry,
		runners:      map[string]*Runner{},
		accessoryEnv: map[string]map[string]string{},
	}
}

func (m *Manager) cachedAccessoryEnv(branch string) (map[string]string, bool) {
	m.mu.RLock()
	env, ok := m.accessoryEnv[branch]
	m.mu.RUnlock()
	return env, ok
}

func (m *Manager) cacheAccessoryEnv(branch string, env map[string]string) {
	m.mu.Lock()
	m.accessoryEnv[branch] = env
	m.mu.Unlock()
}

func (m *Manager) setRunner(key string, r *Runner) {
	m.mu.Lock()
	m.runners[key] = r
	m.mu.Unlock()
}

func (m *Manager) getRunner(key string) (*Runner, bool) {
	m.mu.RLock()
	r, ok := m.runners[key]
	m.mu.RUnlock()
	return r, ok
}

func (m *Manager) deleteRunner(key string) {
	m.mu.Lock()
	delete(m.runners, key)
	m.mu.Unlock()
}

// ServiceResult describes the outcome of starting or stopping a service.
type ServiceResult struct {
	Branch  string
	Service string
	Port    int
	PID     int
	Err     error
}

// StartServices starts services for the given worktree.
// If serviceFilter is non-empty, only that service is started.
func (m *Manager) StartServices(tree *git.Worktree, serviceFilter string) []ServiceResult {
	var results []ServiceResult

	services := m.targetServices(serviceFilter)

	// First allocate all ports so cross-service env vars are available.
	portMap := map[string]int{}
	for _, svcName := range services {
		p, err := m.registry.AssignPort(tree.Branch, svcName)
		if err != nil {
			results = append(results, ServiceResult{
				Branch: tree.Branch, Service: svcName, Err: err,
			})
			continue
		}
		portMap[svcName] = p
	}

	// Build proxy port map for cross-service URLs.
	proxyPorts := map[string]int{}
	for svcName, svc := range m.cfg.Services {
		proxyPorts[svcName] = svc.ProxyPort
	}

	// Determine proxy scheme from state.
	proxyScheme := "http"
	if err := m.store.WithLock(func() error {
		st, e := m.store.Load()
		if e != nil {
			return e
		}
		if st.Proxy.HTTPS {
			proxyScheme = "https"
		}
		return nil
	}); err != nil {
		logging.Warn("failed to load proxy state for scheme: %v", err)
	}

	slug := tree.Slug()

	// Provision per-worktree accessories (databases, ...) and collect the env
	// vars they inject. If any accessory fails, do not start services against a
	// half-provisioned environment.
	accEnv, accResults, accOK := m.provisionAccessories(tree)
	results = append(results, accResults...)
	if !accOK {
		return results
	}

	for _, svcName := range services {
		p, ok := portMap[svcName]
		if !ok {
			continue // port allocation failed, already reported
		}

		// Clean up stale processes.
		m.cleanStale(tree.Branch, svcName)

		// Check if port is available. If not, the port might be held by an orphan process.
		if !IsPortAvailable(p) {
			results = append(results, ServiceResult{
				Branch: tree.Branch, Service: svcName, Port: p,
				Err: fmt.Errorf("port %d is already in use (orphan process?)", p),
			})
			continue
		}

		svc := m.cfg.Services[svcName]
		command := m.cfg.CommandForBranch(svcName, tree.Branch)
		env := m.cfg.EnvForBranch(svcName, tree.Branch)

		dir := tree.Path
		if svc.Dir != "" {
			dir = filepath.Join(tree.Path, svc.Dir)
		}

		// Validate the resolved directory stays within the worktree root.
		cleanDir := filepath.Clean(dir)
		cleanRoot := filepath.Clean(tree.Path)
		if cleanDir != cleanRoot && !strings.HasPrefix(cleanDir, cleanRoot+string(filepath.Separator)) {
			results = append(results, ServiceResult{
				Branch: tree.Branch, Service: svcName,
				Err: fmt.Errorf("service directory %q resolves outside worktree root", svc.Dir),
			})
			continue
		}

		runner := NewRunner(RunnerConfig{
			ServiceName:          svcName,
			Branch:               tree.Branch,
			BranchSlug:           slug,
			Command:              command,
			Dir:                  dir,
			Port:                 p,
			Env:                  env,
			InjectedEnv:          accEnv,
			LogDir:               filepath.Join(m.store.Dir(), "logs"),
			AllServicePorts:      portMap,
			AllServiceProxyPorts: proxyPorts,
			ProxyScheme:          proxyScheme,
		})

		pid, err := runner.Start()
		result := ServiceResult{
			Branch: tree.Branch, Service: svcName, Port: p, PID: pid, Err: err,
		}
		results = append(results, result)

		if err == nil {
			key := tree.Branch + ":" + svcName
			m.setRunner(key, runner)

			if err := m.store.WithLock(func() error {
				st, e := m.store.Load()
				if e != nil {
					return e
				}
				state.SetServiceState(st, tree.Branch, svcName, state.RunningServiceState(p, pid))
				return m.store.Save(st)
			}); err != nil {
				logging.Warn("failed to save state after starting %s/%s: %v", tree.Branch, svcName, err)
			}
		}
	}

	return results
}

// provisionAccessories provisions every configured accessory for the worktree,
// recording each in state and returning the env vars to inject into services.
// The bool is false if any accessory failed (in which case callers should not
// start services). Successes are logged; failures are returned as error
// ServiceResults. With no accessories configured it is a no-op. Once a branch's
// accessories provision successfully the result is cached for this Manager's
// lifetime, so later calls return it without touching the server.
func (m *Manager) provisionAccessories(tree *git.Worktree) (map[string]string, []ServiceResult, bool) {
	if env, ok := m.cachedAccessoryEnv(tree.Branch); ok {
		return env, nil, true
	}

	env := map[string]string{}

	accs, err := accessory.BuildAll(m.cfg)
	if err != nil {
		return env, []ServiceResult{{Branch: tree.Branch, Service: "accessories", Err: err}}, false
	}
	if len(accs) == 0 {
		m.cacheAccessoryEnv(tree.Branch, env)
		return env, nil, true
	}

	names := make([]string, 0, len(accs))
	for name := range accs {
		names = append(names, name)
	}
	sort.Strings(names)

	wt := accessory.FromWorktree(tree)

	var results []ServiceResult
	ok := true
	for _, name := range names {
		a := accs[name]
		ctx, cancel := context.WithTimeout(context.Background(), accessory.OpTimeout)
		prov, err := a.Provision(ctx, wt)
		cancel()
		if err != nil {
			results = append(results, ServiceResult{
				Branch: tree.Branch, Service: "accessory:" + name, Err: err,
			})
			ok = false
			continue
		}
		for k, v := range prov.Env {
			env[k] = v
		}
		if err := m.store.RecordAccessory(tree.Branch, name, a.Kind(), prov.Handle); err != nil {
			logging.Warn("failed to record accessory state %s/%s: %v", tree.Branch, name, err)
		}
		logging.Info("Provisioning %s (%s) for %s ...", name, a.Kind(), tree.Branch)
	}
	if ok {
		m.cacheAccessoryEnv(tree.Branch, env)
	}
	return env, results, ok
}

// StopServices stops services for the given worktree.
func (m *Manager) StopServices(tree *git.Worktree, serviceFilter string) []ServiceResult {
	var results []ServiceResult
	services := m.targetServices(serviceFilter)

	for _, svcName := range services {
		key := tree.Branch + ":" + svcName
		result := ServiceResult{Branch: tree.Branch, Service: svcName}

		// Try runner first.
		if runner, ok := m.getRunner(key); ok {
			result.Err = runner.Stop()
			m.deleteRunner(key)
		} else {
			// Fall back to PID from state.
			if err := m.store.WithLock(func() error {
				st, e := m.store.Load()
				if e != nil {
					return e
				}
				ss := state.GetServiceState(st, tree.Branch, svcName)
				if ss != nil && ss.PID > 0 && IsProcessRunning(ss.PID) {
					result.PID = ss.PID
					result.Err = StopPID(ss.PID)
				}
				return nil
			}); err != nil {
				result.Err = err
			}
		}

		// Update state to stopped.
		if err := m.store.WithLock(func() error {
			st, e := m.store.Load()
			if e != nil {
				return e
			}
			ss := state.GetServiceState(st, tree.Branch, svcName)
			portVal := 0
			if ss != nil {
				portVal = ss.Port
			}
			state.SetServiceState(st, tree.Branch, svcName, state.StoppedServiceState(portVal))
			return m.store.Save(st)
		}); err != nil {
			logging.Warn("failed to update state after stopping %s/%s: %v", tree.Branch, svcName, err)
		}

		results = append(results, result)
	}

	return results
}

// cleanStale checks if a previously recorded process is dead and cleans up state.
func (m *Manager) cleanStale(branch, service string) {
	if err := m.store.WithLock(func() error {
		st, err := m.store.Load()
		if err != nil {
			return err
		}
		ss := state.GetServiceState(st, branch, service)
		if ss != nil && ss.Status == state.StatusRunning && ss.PID > 0 && !IsProcessRunning(ss.PID) {
			state.SetServiceState(st, branch, service, state.StoppedServiceState(ss.Port))
			return m.store.Save(st)
		}
		return nil
	}); err != nil {
		logging.Warn("failed to clean stale state for %s/%s: %v", branch, service, err)
	}
}

// targetServices returns sorted service names, optionally filtered.
func (m *Manager) targetServices(filter string) []string {
	if filter != "" {
		if _, ok := m.cfg.Services[filter]; ok {
			return []string{filter}
		}
		return nil
	}
	names := make([]string, 0, len(m.cfg.Services))
	for name := range m.cfg.Services {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// StatusAll returns the full state for display.
func (m *Manager) StatusAll() (*state.State, error) {
	var st *state.State
	err := m.store.WithLock(func() error {
		var e error
		st, e = m.store.Load()
		return e
	})
	return st, err
}
