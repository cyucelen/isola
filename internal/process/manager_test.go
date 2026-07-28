package process

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cyucelen/isola/internal/accessory"
	"github.com/cyucelen/isola/internal/config"
	"github.com/cyucelen/isola/internal/git"
	"github.com/cyucelen/isola/internal/port"
	"github.com/cyucelen/isola/internal/state"
)

// A fake accessory driver ("faketest") to exercise manager provisioning without
// a real server. `fail = true` makes Provision error.
func init() {
	accessory.Register("faketest", func(name string, dec accessory.Decoder) (accessory.Accessory, error) {
		var c struct {
			Fail bool `toml:"fail"`
		}
		if err := dec(&c); err != nil {
			return nil, err
		}
		return &fakeAccessory{name: name, fail: c.Fail}, nil
	})
}

type fakeAccessory struct {
	name string
	fail bool
}

func (f *fakeAccessory) Name() string { return f.name }
func (f *fakeAccessory) Kind() string { return "faketest" }
func (f *fakeAccessory) Provision(_ context.Context, wt accessory.WorktreeInfo) (accessory.Provisioned, error) {
	if f.fail {
		return accessory.Provisioned{}, errors.New("provision boom")
	}
	return accessory.Provisioned{
		Handle: map[string]string{"id": "res-" + wt.Slug},
		URL:    "fake://" + wt.Slug,
	}, nil
}
func (f *fakeAccessory) Reset(ctx context.Context, wt accessory.WorktreeInfo) (accessory.Provisioned, error) {
	return f.Provision(ctx, wt)
}
func (f *fakeAccessory) Drop(context.Context, map[string]string) error { return nil }

// managerWithConfig writes a .isola.toml, loads it, and returns a manager.
func managerWithConfig(t *testing.T, toml string) (*Manager, *state.FileStore) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, config.FileName), []byte(toml), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	store, err := state.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return NewManager(cfg, store, port.NewRegistry(store, cfg)), store
}

func TestProvisionAccessories(t *testing.T) {
	mgr, store := managerWithConfig(t, `
[services.web]
command = "sleep 60"
port_range = { min = 19100, max = 19199 }
proxy_port = 3000

[accessories.db]
kind = "faketest"
inject = "DATABASE_URL"
`)
	tree := &git.Worktree{Path: t.TempDir(), Branch: "feature/auth"}

	env := mgr.provisionAccessories(tree)
	if env["db"] != "fake://feature-auth" {
		t.Errorf("accessory URL by name = %v", env)
	}

	// The provisioned resource must be recorded in state.
	st, _ := store.Load()
	rec := state.GetAccessoryState(st, "feature/auth", "db")
	if rec == nil || rec.Handle["id"] != "res-feature-auth" || rec.Kind != "faketest" {
		t.Errorf("state record = %+v", rec)
	}
}

func TestProvisionAccessoriesNoneConfigured(t *testing.T) {
	mgr, _ := managerWithConfig(t, `
[services.web]
command = "sleep 60"
port_range = { min = 19100, max = 19199 }
proxy_port = 3000
`)
	env := mgr.provisionAccessories(&git.Worktree{Path: t.TempDir(), Branch: "main"})
	if len(env) != 0 {
		t.Errorf("no accessories should yield empty env, got %v", env)
	}
}

func TestStartServicesProceedsOnAccessoryFailure(t *testing.T) {
	mgr, store := managerWithConfig(t, `
[services.web]
command = "sleep 60"
port_range = { min = 19100, max = 19199 }
proxy_port = 3000

[accessories.db]
kind = "faketest"
fail = true
`)
	tree := &git.Worktree{Path: t.TempDir(), Branch: "main"}

	results := mgr.StartServices(tree, "")
	defer mgr.StopServices(tree, "")

	// A failed accessory only warns; the web service must still start.
	var web *ServiceResult
	for i := range results {
		if results[i].Service == "web" {
			web = &results[i]
		}
	}
	if web == nil {
		t.Fatal("no result for web service")
	}
	if web.Err != nil {
		t.Errorf("web should start despite accessory failure, got err: %v", web.Err)
	}
	if web.PID <= 0 {
		t.Errorf("web should have a running PID, got %d", web.PID)
	}
	if _, ok := mgr.getRunner("main:web"); !ok {
		t.Error("a runner should exist for web")
	}

	st, _ := store.Load()
	if ss := state.GetServiceState(st, "main", "web"); ss == nil || ss.Status != state.StatusRunning {
		t.Errorf("web should be recorded running, got %+v", ss)
	}
}

// TestStartServicesFailsDependentsOfAFailedAccessory covers the diagnosis
// problem behind the reported bug: a service that reads an accessory URL used to
// start with that variable empty, so a naming or connection failure surfaced as
// "the API is not reachable" instead of as a broken accessory. Dependents now
// fail with the reason; independent services still start.
func TestStartServicesFailsDependentsOfAFailedAccessory(t *testing.T) {
	mgr, _ := managerWithConfig(t, `
[services.api]
command = "sleep 60"
port_range = { min = 19100, max = 19199 }
proxy_port = 3000
env = { DATABASE_URL = "${accessories.db.url}" }

[services.web]
command = "sleep 60"
port_range = { min = 19200, max = 19299 }
proxy_port = 3001

[accessories.db]
kind = "faketest"
fail = true
`)
	tree := &git.Worktree{Path: t.TempDir(), Branch: "main"}

	results := mgr.StartServices(tree, "")
	defer mgr.StopServices(tree, "")

	byName := map[string]ServiceResult{}
	for _, r := range results {
		byName[r.Service] = r
	}

	api, ok := byName["api"]
	if !ok {
		t.Fatal("no result for api service")
	}
	if api.Err == nil {
		t.Fatal("api reads ${accessories.db.url} and must not start without it")
	}
	for _, want := range []string{`"db"`, "not started"} {
		if !strings.Contains(api.Err.Error(), want) {
			t.Errorf("api error %v should mention %q", api.Err, want)
		}
	}
	if api.PID > 0 {
		t.Errorf("api should not be running, got PID %d", api.PID)
	}
	if _, running := mgr.getRunner("main:api"); running {
		t.Error("no runner should exist for api")
	}

	web, ok := byName["web"]
	if !ok {
		t.Fatal("no result for web service")
	}
	if web.Err != nil {
		t.Errorf("web does not use the accessory and should start: %v", web.Err)
	}
}

func TestStartServicesReportsUnconfiguredAccessoryRef(t *testing.T) {
	mgr, _ := managerWithConfig(t, `
[services.api]
command = "sleep 60"
port_range = { min = 19100, max = 19199 }
proxy_port = 3000
env = { DATABASE_URL = "${accessories.typo.url}" }
`)
	tree := &git.Worktree{Path: t.TempDir(), Branch: "main"}

	results := mgr.StartServices(tree, "")
	defer mgr.StopServices(tree, "")

	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	if results[0].Err == nil || !strings.Contains(results[0].Err.Error(), "not configured") {
		t.Errorf("err = %v, want it to report the accessory as not configured", results[0].Err)
	}
}

func TestAccessoryRefs(t *testing.T) {
	got := accessoryRefs(map[string]string{
		"DATABASE_URL": "${accessories.db.url}",
		"REDIS_URL":    "redis://${accessories.cache.url}/0",
		"PLAIN":        "no refs here",
		"OTHER":        "${accessories.db.url} again, plus ${ISOLA_BRANCH}",
		"NOT_A_URL":    "${accessories.db.password}",
		"NO_NAME":      "${accessories..url}",
	})
	want := []string{"cache", "db"}
	if len(got) != len(want) {
		t.Fatalf("accessoryRefs = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("accessoryRefs = %v, want %v", got, want)
		}
	}
}

func TestStartServicesReportsImmediateExit(t *testing.T) {
	mgr, store := managerWithConfig(t, `
[services.web]
command = "true"
port_range = { min = 19100, max = 19199 }
proxy_port = 3000
`)
	tree := &git.Worktree{Path: t.TempDir(), Branch: "main"}

	results := mgr.StartServices(tree, "")
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Err == nil || !strings.Contains(results[0].Err.Error(), "exited immediately") {
		t.Errorf("a command that exits at once must be reported as failed, got err: %v", results[0].Err)
	}
	// It must not be recorded as running.
	st, _ := store.Load()
	if ss := state.GetServiceState(st, "main", "web"); ss != nil && ss.Status == state.StatusRunning {
		t.Errorf("a service that exited must not be recorded running, got %+v", ss)
	}
}

func TestStartServicesReportsMissingDir(t *testing.T) {
	mgr, _ := managerWithConfig(t, `
[services.web]
command = "sleep 60"
dir = "does-not-exist"
port_range = { min = 19100, max = 19199 }
proxy_port = 3000
`)
	tree := &git.Worktree{Path: t.TempDir(), Branch: "main"}
	defer mgr.StopServices(tree, "")

	results := mgr.StartServices(tree, "")
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Err == nil || !strings.Contains(results[0].Err.Error(), "does not exist") {
		t.Errorf("a missing service dir must be reported clearly, got err: %v", results[0].Err)
	}
}

func TestRootSetupRunsAtRootBeforeServices(t *testing.T) {
	// The service command only succeeds if the root setup ran first (it checks
	// for the marker), and the marker is written with a relative path, so its
	// presence at the worktree root also proves root setup ran there.
	mgr, _ := managerWithConfig(t, `
setup = "echo ran > root-setup-marker"

[services.web]
command = "test -f root-setup-marker && sleep 60"
port_range = { min = 19100, max = 19199 }
proxy_port = 3000
`)
	root := t.TempDir()
	tree := &git.Worktree{Path: root, Branch: "main"}
	defer mgr.StopServices(tree, "")

	if err := mgr.RunRootSetup(tree); err != nil {
		t.Fatalf("RunRootSetup: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "root-setup-marker")); err != nil {
		t.Fatalf("root setup should run at the worktree root, marker missing: %v", err)
	}

	results := mgr.StartServices(tree, "")
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Err != nil {
		t.Errorf("web should start after root setup ran, got err: %v", results[0].Err)
	}
}

func TestRootSetupNonZeroExitReturnsError(t *testing.T) {
	// A non-zero root setup returns an error; `up` uses it to abort before
	// starting any of the worktree's services.
	mgr, _ := managerWithConfig(t, `
setup = "exit 3"

[services.web]
command = "sleep 60"
port_range = { min = 19100, max = 19199 }
proxy_port = 3000
`)
	tree := &git.Worktree{Path: t.TempDir(), Branch: "main"}

	if err := mgr.RunRootSetup(tree); err == nil {
		t.Fatal("expected RunRootSetup to fail on a non-zero exit")
	}
}

func TestRootSetupNoneConfiguredIsNoOp(t *testing.T) {
	mgr, _ := managerWithConfig(t, `
[services.web]
command = "sleep 60"
port_range = { min = 19100, max = 19199 }
proxy_port = 3000
`)
	tree := &git.Worktree{Path: t.TempDir(), Branch: "main"}

	if err := mgr.RunRootSetup(tree); err != nil {
		t.Errorf("RunRootSetup with no top-level setup should be a no-op, got: %v", err)
	}
}

func TestTargetServices(t *testing.T) {
	cfg := &config.Config{
		Services: map[string]config.ServiceConfig{
			"web":    {Command: "npm start"},
			"api":    {Command: "go run ."},
			"worker": {Command: "python worker.py"},
		},
	}
	store, _ := state.NewFileStore(t.TempDir())
	m := NewManager(cfg, store, nil)

	t.Run("no filter returns sorted", func(t *testing.T) {
		got := m.targetServices("")
		if len(got) != 3 {
			t.Fatalf("targetServices() returned %d, want 3", len(got))
		}
		if got[0] != "api" || got[1] != "web" || got[2] != "worker" {
			t.Errorf("targetServices() = %v, want [api, web, worker]", got)
		}
	})

	t.Run("filter exists", func(t *testing.T) {
		got := m.targetServices("web")
		if len(got) != 1 || got[0] != "web" {
			t.Errorf("targetServices(web) = %v, want [web]", got)
		}
	})

	t.Run("filter not found", func(t *testing.T) {
		got := m.targetServices("nonexistent")
		if got != nil {
			t.Errorf("targetServices(nonexistent) = %v, want nil", got)
		}
	})
}

func TestMutexHelpers(t *testing.T) {
	cfg := &config.Config{
		Services: map[string]config.ServiceConfig{},
	}
	store, _ := state.NewFileStore(t.TempDir())
	m := NewManager(cfg, store, nil)

	// Initially no runner.
	_, ok := m.getRunner("main:web")
	if ok {
		t.Error("expected getRunner to return false for missing key")
	}

	// Set a runner.
	r := NewRunner(RunnerConfig{ServiceName: "web"})
	m.setRunner("main:web", r)

	got, ok := m.getRunner("main:web")
	if !ok {
		t.Error("expected getRunner to return true after setRunner")
	}
	if got != r {
		t.Error("expected getRunner to return the same runner")
	}

	// Delete.
	m.deleteRunner("main:web")
	_, ok = m.getRunner("main:web")
	if ok {
		t.Error("expected getRunner to return false after deleteRunner")
	}

	// Delete non-existent key should not panic.
	m.deleteRunner("nonexistent")
}

func newTestManager(t *testing.T) (*Manager, *state.FileStore) {
	t.Helper()
	dir := t.TempDir()
	store, err := state.NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		Services: map[string]config.ServiceConfig{
			"web": {
				Command:   "sleep 60",
				PortRange: config.PortRange{Min: 19100, Max: 19199},
				ProxyPort: 3000,
			},
		},
		Worktrees: map[string]config.WTOverride{},
	}

	registry := port.NewRegistry(store, cfg)
	mgr := NewManager(cfg, store, registry)
	return mgr, store
}

func TestManagerStartStopServices(t *testing.T) {
	mgr, store := newTestManager(t)

	tree := &git.Worktree{
		Path:   t.TempDir(),
		Branch: "main",
	}

	// Start services.
	results := mgr.StartServices(tree, "web")
	if len(results) != 1 {
		t.Fatalf("StartServices returned %d results, want 1", len(results))
	}
	r := results[0]
	if r.Err != nil {
		t.Fatalf("StartServices error: %v", r.Err)
	}
	if r.PID <= 0 {
		t.Errorf("expected positive PID, got %d", r.PID)
	}
	if r.Port < 19100 || r.Port > 19199 {
		t.Errorf("port %d out of expected range [19100, 19199]", r.Port)
	}
	if r.Branch != "main" {
		t.Errorf("branch = %q, want %q", r.Branch, "main")
	}
	if r.Service != "web" {
		t.Errorf("service = %q, want %q", r.Service, "web")
	}

	// Verify state was persisted.
	var st *state.State
	_ = store.WithLock(func() error {
		var e error
		st, e = store.Load()
		return e
	})
	ss := state.GetServiceState(st, "main", "web")
	if ss == nil {
		t.Fatal("expected service state to be persisted")
	}
	if ss.Status != state.StatusRunning {
		t.Errorf("state status = %q, want %q", ss.Status, state.StatusRunning)
	}
	if ss.PID != r.PID {
		t.Errorf("state PID = %d, want %d", ss.PID, r.PID)
	}

	// Verify runner is tracked.
	_, ok := mgr.getRunner("main:web")
	if !ok {
		t.Error("expected runner to be tracked in manager")
	}

	// Stop services.
	stopResults := mgr.StopServices(tree, "web")
	if len(stopResults) != 1 {
		t.Fatalf("StopServices returned %d results, want 1", len(stopResults))
	}
	if stopResults[0].Err != nil {
		t.Fatalf("StopServices error: %v", stopResults[0].Err)
	}

	// Give OS time to clean up.
	time.Sleep(200 * time.Millisecond)

	// Verify runner was removed.
	_, ok = mgr.getRunner("main:web")
	if ok {
		t.Error("expected runner to be removed after stop")
	}

	// Verify state was updated to stopped.
	_ = store.WithLock(func() error {
		var e error
		st, e = store.Load()
		return e
	})
	ss = state.GetServiceState(st, "main", "web")
	if ss == nil {
		t.Fatal("expected service state after stop")
	}
	if ss.Status != state.StatusStopped {
		t.Errorf("state status after stop = %q, want %q", ss.Status, state.StatusStopped)
	}
}

// A single-service `up --service X` must still resolve X's cross-service env
// refs (${services.<sibling>.port|direct_url}) against the sibling's allocated
// port, not leave them empty. Regression test for the scoping bug where the
// interpolation context was built only from the in-flight (filtered) service.
func TestStartServicesSingleServiceResolvesSiblingPort(t *testing.T) {
	dir := t.TempDir()
	store, err := state.NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		Services: map[string]config.ServiceConfig{
			"api": {
				Command:   "sleep 60",
				PortRange: config.PortRange{Min: 19200, Max: 19299},
				ProxyPort: 8000,
			},
			"dashboard": {
				Command:   "sleep 60",
				PortRange: config.PortRange{Min: 19300, Max: 19399},
				ProxyPort: 3000,
				Env: map[string]string{
					"API_PORT":       "${services.api.port}",
					"API_DIRECT_URL": "${services.api.direct_url}",
				},
			},
		},
		Worktrees: map[string]config.WTOverride{},
	}

	registry := port.NewRegistry(store, cfg)
	mgr := NewManager(cfg, store, registry)
	tree := &git.Worktree{Path: t.TempDir(), Branch: "main"}

	// api's port is already allocated, as it would be for a running sibling,
	// before we bring up dashboard on its own.
	apiPort, err := registry.AssignPort("main", "api")
	if err != nil {
		t.Fatalf("AssignPort(api): %v", err)
	}

	// Start ONLY dashboard.
	results := mgr.StartServices(tree, "dashboard")
	if len(results) != 1 {
		t.Fatalf("StartServices returned %d results, want 1", len(results))
	}
	if r := results[0]; r.Err != nil {
		t.Fatalf("StartServices(dashboard): %v", r.Err)
	}
	t.Cleanup(func() { mgr.StopServices(tree, "dashboard") })

	runner, ok := mgr.getRunner("main:dashboard")
	if !ok {
		t.Fatal("expected dashboard runner to be tracked")
	}

	env := runner.FileEnv()
	wantPort := fmt.Sprintf("%d", apiPort)
	if env["API_PORT"] != wantPort {
		t.Errorf("API_PORT = %q, want %q (sibling port must resolve under --service)", env["API_PORT"], wantPort)
	}
	if !strings.Contains(env["API_DIRECT_URL"], wantPort) {
		t.Errorf("API_DIRECT_URL = %q, want it to contain sibling port %q", env["API_DIRECT_URL"], wantPort)
	}
}

func TestManagerStartBackgroundProcess(t *testing.T) {
	dir := t.TempDir()
	store, err := state.NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	// A worker with neither port_range nor proxy_port: a first-class background
	// process. It should run and be managed, but get no port and no $PORT.
	envFile := filepath.Join(t.TempDir(), "worker.env")
	cfg := &config.Config{
		Services: map[string]config.ServiceConfig{
			"worker": {
				Command: fmt.Sprintf("printenv > %s; sleep 60", envFile),
			},
		},
		Worktrees: map[string]config.WTOverride{},
	}

	registry := port.NewRegistry(store, cfg)
	mgr := NewManager(cfg, store, registry)
	tree := &git.Worktree{Path: t.TempDir(), Branch: "main"}

	results := mgr.StartServices(tree, "worker")
	if len(results) != 1 {
		t.Fatalf("StartServices returned %d results, want 1", len(results))
	}
	r := results[0]
	if r.Err != nil {
		t.Fatalf("StartServices error: %v", r.Err)
	}
	if r.PID <= 0 {
		t.Errorf("expected positive PID, got %d", r.PID)
	}
	if r.Port != 0 {
		t.Errorf("background process got port %d, want 0", r.Port)
	}

	// The command should have run with the isola env but no PORT.
	var env []byte
	for i := 0; i < 50; i++ {
		if env, err = os.ReadFile(envFile); err == nil && len(env) > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(env) == 0 {
		t.Fatal("background process did not write its environment")
	}
	envStr := string(env)
	if strings.Contains(envStr, "\nPORT=") || strings.HasPrefix(envStr, "PORT=") {
		t.Errorf("background process should not receive $PORT, got env:\n%s", envStr)
	}
	if !strings.Contains(envStr, "ISOLA_SERVICE=worker") {
		t.Errorf("background process missing ISOLA_SERVICE, got env:\n%s", envStr)
	}

	// A background process is fully managed: stopping it must succeed like any
	// other service.
	stop := mgr.StopServices(tree, "worker")
	if len(stop) != 1 {
		t.Fatalf("StopServices returned %d results, want 1", len(stop))
	}
	if stop[0].Err != nil {
		t.Errorf("stopping a background process errored: %v", stop[0].Err)
	}
}

func TestManagerCleanStale(t *testing.T) {
	dir := t.TempDir()
	store, err := state.NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		Services: map[string]config.ServiceConfig{
			"web": {
				Command:   "sleep 60",
				PortRange: config.PortRange{Min: 19200, Max: 19299},
				ProxyPort: 3000,
			},
		},
		Worktrees: map[string]config.WTOverride{},
	}

	// Write stale state with a non-existent PID.
	st := &state.State{
		Services:        map[string]map[string]*state.ServiceState{},
		PortAssignments: map[string]int{},
	}
	state.SetServiceState(st, "main", "web", state.RunningServiceState(19200, 99999999))
	if err := store.Save(st); err != nil {
		t.Fatal(err)
	}

	mgr := NewManager(cfg, store, port.NewRegistry(store, cfg))

	// cleanStale should detect the dead PID and update state.
	mgr.cleanStale("main", "web")

	_ = store.WithLock(func() error {
		var e error
		st, e = store.Load()
		return e
	})
	ss := state.GetServiceState(st, "main", "web")
	if ss == nil {
		t.Fatal("expected service state after cleanStale")
	}
	if ss.Status != state.StatusStopped {
		t.Errorf("state status after cleanStale = %q, want %q", ss.Status, state.StatusStopped)
	}
}

func TestManagerStatusAll(t *testing.T) {
	dir := t.TempDir()
	store, err := state.NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		Services: map[string]config.ServiceConfig{},
	}
	mgr := NewManager(cfg, store, nil)

	st, err := mgr.StatusAll()
	if err != nil {
		t.Fatalf("StatusAll error: %v", err)
	}
	if st == nil {
		t.Fatal("StatusAll returned nil state")
	}
}

func TestManagerStopWithoutStart(t *testing.T) {
	mgr, _ := newTestManager(t)

	tree := &git.Worktree{
		Path:   t.TempDir(),
		Branch: "nonexistent",
	}

	// Stopping services that were never started should not error.
	results := mgr.StopServices(tree, "web")
	if len(results) != 1 {
		t.Fatalf("StopServices returned %d results, want 1", len(results))
	}
	if results[0].Err != nil {
		t.Errorf("StopServices error: %v", results[0].Err)
	}
}
