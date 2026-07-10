package process

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cyucelen/isola/internal/accessory"
	"github.com/cyucelen/isola/internal/cert"
	"github.com/cyucelen/isola/internal/config"
	"github.com/cyucelen/isola/internal/copyfiles"
	"github.com/cyucelen/isola/internal/git"
	"github.com/cyucelen/isola/internal/logging"
	"github.com/cyucelen/isola/internal/port"
	"github.com/cyucelen/isola/internal/registry"
	"github.com/cyucelen/isola/internal/state"
)

// startGrace is how long StartServices waits after spawning a service before
// declaring it started, so a command that exits immediately (bad command,
// missing binary, wrong dir) is reported as a failure rather than a false "✓".
const startGrace = 400 * time.Millisecond

// Manager coordinates starting and stopping services across worktrees.
type Manager struct {
	cfg      *config.Config
	store    *state.FileStore
	registry *port.Registry
	mu       sync.RWMutex
	runners  map[string]*Runner // key: "branch:service"
	// accessoryURLs caches a branch's accessory name -> URL once successfully
	// provisioned in this Manager's lifetime, so repeated StartServices calls
	// (e.g. per-service restarts from the TUI) don't re-provision — a CLI `up`
	// gets one Manager per run, the TUI one per session.
	accessoryURLs map[string]map[string]string // branch -> accessory name -> URL
}

// NewManager creates a new process Manager.
func NewManager(cfg *config.Config, store *state.FileStore, registry *port.Registry) *Manager {
	return &Manager{
		cfg:           cfg,
		store:         store,
		registry:      registry,
		runners:       map[string]*Runner{},
		accessoryURLs: map[string]map[string]string{},
	}
}

func (m *Manager) cachedAccessoryEnv(branch string) (map[string]string, bool) {
	m.mu.RLock()
	env, ok := m.accessoryURLs[branch]
	m.mu.RUnlock()
	return env, ok
}

func (m *Manager) cacheAccessoryEnv(branch string, env map[string]string) {
	m.mu.Lock()
	m.accessoryURLs[branch] = env
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
	// AlreadyRunning is true when start was a no-op because the service was
	// already running (a live PID in state). Not an error, not a fresh start.
	AlreadyRunning bool
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

	// The scheme for ISOLA_<SVC>_URL follows this project's proxy config; the
	// shared daemon serves the project's ports over that scheme.
	proxyScheme := "http"
	caCertPath := ""
	if m.cfg.Proxy.HTTPS {
		proxyScheme = "https"
		caCertPath = m.ensureCACert()
	}

	slug := tree.Slug()

	// Bring up per-worktree accessories (databases, ...) and collect the env vars
	// they inject. A failed accessory only warns and is skipped; services still
	// start (without that accessory's env), so a down dependency never blocks
	// working on the rest of the worktree.
	acc := m.provisionAccessories(tree)

	for _, svcName := range services {
		p, ok := portMap[svcName]
		if !ok {
			continue // port allocation failed, already reported
		}

		// Clean up stale processes (dead PIDs recorded as running).
		m.cleanStale(tree.Branch, svcName)

		// If the service is already running, starting again is a no-op: report it
		// as already-up rather than the spurious "port in use" error that a naive
		// availability check would raise against the service's own process.
		if pid := m.runningPID(tree.Branch, svcName); pid > 0 {
			results = append(results, ServiceResult{
				Branch: tree.Branch, Service: svcName, Port: p, PID: pid, AlreadyRunning: true,
			})
			continue
		}

		// Check if port is available. If not, the port is held by a foreign
		// process (an orphan, or something outside isola).
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

		// The directory must exist, or `sh -c` fails with a confusing
		// "fork/exec /bin/sh: no such file or directory" that blames the shell.
		if info, statErr := os.Stat(cleanDir); statErr != nil || !info.IsDir() {
			results = append(results, ServiceResult{
				Branch: tree.Branch, Service: svcName,
				Err: fmt.Errorf("dir %q does not exist (relative to the worktree root)", svc.Dir),
			})
			continue
		}

		runner := NewRunner(RunnerConfig{
			ServiceName:          svcName,
			Branch:               tree.Branch,
			BranchSlug:           slug,
			Project:              m.cfg.Project,
			Command:              command,
			Dir:                  dir,
			Port:                 p,
			Env:                  env,
			AccessoriesByName:    acc,
			LogDir:               filepath.Join(m.store.Dir(), "logs"),
			AllServicePorts:      portMap,
			AllServiceProxyPorts: proxyPorts,
			ProxyScheme:          proxyScheme,
			CACertPath:           caCertPath,
		})

		// Write the service's resolved env into its env file (accessory URLs,
		// ${...} refs, [env]) so tools that read the file directly, not just the
		// process environment, see this worktree's isolated values.
		m.writeEnvFile(tree, svcName, dir, cleanRoot, runner.FileEnv())

		pid, err := runner.Start()
		if err == nil {
			// Don't claim success for a service that dies on startup: wait a
			// short grace and, if the process is already gone, report a failure
			// pointing at the logs instead of a false "✓ started".
			select {
			case <-runner.Done():
				err = fmt.Errorf("%s exited immediately; check `isola logs %s`", svcName, tree.Branch)
			case <-time.After(startGrace):
			}
		}
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

// ensureCACert makes sure isola's dev CA exists and returns its path, so
// services can trust sibling HTTPS certs via NODE_EXTRA_CA_CERTS. It targets the
// same cert dir the proxy daemon uses; EnsureCerts is idempotent, so generating
// here (services start before the daemon) is safe and the daemon reuses it.
// Returns "" on error (warned), leaving the CA env unset.
func (m *Manager) ensureCACert() string {
	dir, err := registry.GlobalDir()
	if err != nil {
		logging.Warn("HTTPS CA unavailable for service env: %v", err)
		return ""
	}
	paths, err := cert.EnsureCerts(filepath.Join(dir, "certs"))
	if err != nil {
		logging.Warn("preparing HTTPS CA for service env: %v", err)
		return ""
	}
	return paths.CACert
}

// runningPID returns the live PID of a service recorded as running, or 0 if it
// is not currently running.
func (m *Manager) runningPID(branch, service string) int {
	var pid int
	if err := m.store.WithLock(func() error {
		st, err := m.store.Load()
		if err != nil {
			return err
		}
		ss := state.GetServiceState(st, branch, service)
		if ss != nil && ss.Status == state.StatusRunning && ss.PID > 0 && IsProcessRunning(ss.PID) {
			pid = ss.PID
		}
		return nil
	}); err != nil {
		logging.Warn("failed to read state for %s/%s: %v", branch, service, err)
	}
	return pid
}

// provisionAccessories brings up every configured accessory for the worktree,
// recording each in state and returning the env vars to inject into services.
// A failure never blocks service start: it is logged as a warning and the
// service simply runs without that accessory's injected var (so the app falls
// back to its own config). With no accessories configured it is a no-op. Once a
// branch's accessories all succeed the env is cached for this Manager's
// lifetime, so later calls return it without touching the server; a partial
// result is not cached, so a transient failure is retried on the next call.
func (m *Manager) provisionAccessories(tree *git.Worktree) map[string]string {
	if env, ok := m.cachedAccessoryEnv(tree.Branch); ok {
		return env
	}

	env := map[string]string{}

	accs, err := accessory.BuildAll(m.cfg)
	if err != nil {
		logging.Warn("skipping accessories for %s: %v", tree.Branch, err)
		return env
	}
	if len(accs) == 0 {
		m.cacheAccessoryEnv(tree.Branch, env)
		return env
	}

	names := make([]string, 0, len(accs))
	for name := range accs {
		names = append(names, name)
	}
	sort.Strings(names)

	wt := accessory.FromWorktree(tree, m.cfg.Project)

	complete := true
	for _, name := range names {
		a := accs[name]
		ctx, cancel := context.WithTimeout(context.Background(), accessory.OpTimeout)
		prov, err := a.Provision(ctx, wt)
		cancel()
		if err != nil {
			logging.Warn("accessory %s (%s) for %s could not be brought up; continuing without its env: %v",
				name, a.Kind(), tree.Branch, err)
			complete = false
			continue
		}
		env[name] = prov.URL
		if err := m.store.RecordAccessory(tree.Branch, name, a.Kind(), prov.Handle); err != nil {
			logging.Warn("failed to record accessory state %s/%s: %v", tree.Branch, name, err)
		}
		logging.Info("Bringing up %s (%s) for %s ...", name, a.Kind(), tree.Branch)
	}
	if complete {
		m.cacheAccessoryEnv(tree.Branch, env)
	}
	return env
}

// writeEnvFile upserts a service's resolved env into its env file per the
// [env_file] policy, so tools that read the file directly (not just the process
// environment) see this worktree's isolated values. Best-effort: it never blocks
// the service. cleanRoot is the cleaned worktree root; the resolved file must
// stay within it.
func (m *Manager) writeEnvFile(tree *git.Worktree, svcName, dir, cleanRoot string, fileEnv map[string]string) {
	name := m.cfg.ServiceEnvFile(svcName)
	if name == "" || len(fileEnv) == 0 {
		return
	}
	target := filepath.Clean(filepath.Join(dir, name))
	if target != cleanRoot && !strings.HasPrefix(target, cleanRoot+string(filepath.Separator)) {
		logging.Warn("env_file %q for %s resolves outside the worktree; skipping", name, svcName)
		return
	}
	changed, err := copyfiles.UpsertEnv(target, fileEnv, m.cfg.EnvFile.Create)
	if err != nil {
		logging.Warn("writing env file for %s/%s: %v", tree.Branch, svcName, err)
		return
	}
	if len(changed) > 0 {
		rel, relErr := filepath.Rel(tree.Path, target)
		if relErr != nil {
			rel = name
		}
		logging.Info("Set %s in %s for %s/%s", strings.Join(changed, ", "), rel, tree.Branch, svcName)
	}
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
