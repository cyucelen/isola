package process

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/cyucelen/isola/internal/config"
	"github.com/cyucelen/isola/internal/git"
	"github.com/cyucelen/isola/internal/port"
	"github.com/cyucelen/isola/internal/state"
)

const cleanupConfig = `
[services.web]
command = "sleep 120"
port_range = { min = 19100, max = 19199 }
proxy_port = 3000

[services.api]
command = "sleep 120"
port_range = { min = 19200, max = 19299 }
proxy_port = 3001
`

// touch creates an empty file, making its directory as needed.
func touch(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// loadConfig loads toml as a config from a fresh directory.
func loadConfig(t *testing.T, toml string) *config.Config {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, config.FileName), []byte(toml), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return cfg
}

// seedWorktree records services, ports and an accessory for branch in state, and
// creates its lock files and the logs named by label.
func seedWorktree(t *testing.T, store *state.FileStore, branch, label string, services ...string) {
	t.Helper()
	if err := store.WithLock(func() error {
		st, err := store.Load()
		if err != nil {
			return err
		}
		for i, svc := range services {
			state.SetServiceState(st, branch, svc, state.StoppedServiceState(19100+i))
			state.SetPortAssignment(st, branch, svc, 19100+i)
		}
		state.SetAccessoryState(st, branch, "db", state.RunningAccessoryState("faketest", map[string]string{"id": branch}))
		return store.Save(st)
	}); err != nil {
		t.Fatal(err)
	}
	for _, svc := range services {
		touch(t, store.ServiceLockPath(branch, svc))
		touch(t, LogPath(store.Dir(), label, svc))
	}
}

// assertCleaned checks that branch's service has no lock file, no log, no port
// assignment, and only a stopped tombstone record.
func assertCleaned(t *testing.T, store *state.FileStore, branch, label, svc string) {
	t.Helper()
	if p := LogPath(store.Dir(), label, svc); exists(p) {
		t.Errorf("log %s should be deleted", p)
	}
	if exists(store.ServiceLockPath(branch, svc)) {
		t.Errorf("lock for %s:%s should be deleted", branch, svc)
	}
	st, _ := store.Load()
	if p := state.GetPortAssignment(st, branch, svc); p != 0 {
		t.Errorf("port assignment %s:%s = %d, should be released", branch, svc, p)
	}
	if ss := state.GetServiceState(st, branch, svc); ss != nil && (ss.Status != state.StatusStopped || ss.PID != 0 || ss.Port != 0) {
		t.Errorf("%s/%s should be at most a stopped tombstone, got %+v", branch, svc, ss)
	}
}

// assertKept checks that branch's service keeps its lock file, log and port.
func assertKept(t *testing.T, store *state.FileStore, branch, label, svc string) {
	t.Helper()
	if p := LogPath(store.Dir(), label, svc); !exists(p) {
		t.Errorf("log %s must be kept", p)
	}
	if !exists(store.ServiceLockPath(branch, svc)) {
		t.Errorf("lock for %s:%s must be kept", branch, svc)
	}
	st, _ := store.Load()
	if state.GetPortAssignment(st, branch, svc) == 0 {
		t.Errorf("port assignment %s:%s must be kept", branch, svc)
	}
}

func TestCleanUpWorktreeRemovesOnlyThatWorktree(t *testing.T) {
	mgr, store := managerWithConfig(t, cleanupConfig)
	// "old" is no longer configured but still has a record, a port and files.
	seedWorktree(t, store, "feature/a", "feature-a", "web", "api", "old")
	seedWorktree(t, store, "main", "main", "web", "api")
	// "feature-ab" starts with "feature-a" but is a different Host label.
	seedWorktree(t, store, "feature/ab", "feature-ab", "web")
	// "<label>.log" has no service part, so it is not a service log of the label.
	notServiceLog := filepath.Join(store.Dir(), "logs", "feature-a.log")
	touch(t, notServiceLog)

	active := map[string]bool{"feature/a": true, "main": true, "feature/ab": true}
	if err := CleanUpWorktree(mgr.cfg, store, "feature/a", active, false, nil); err != nil {
		t.Fatalf("CleanUpWorktree: %v", err)
	}

	for _, svc := range []string{"web", "api", "old"} {
		assertCleaned(t, store, "feature/a", "feature-a", svc)
	}
	assertKept(t, store, "main", "main", "web")
	assertKept(t, store, "main", "main", "api")
	assertKept(t, store, "feature/ab", "feature-ab", "web")
	if !exists(notServiceLog) {
		t.Error(`"feature-a.log" is not a service log and must be kept`)
	}

	st, _ := store.Load()
	if state.GetAccessoryState(st, "feature/a", "db") == nil {
		t.Error("an accessory record must stay until its resource is dropped")
	}
	if !exists(filepath.Join(store.Dir(), "state.json")) {
		t.Error("state.json itself must stay")
	}
}

func TestCleanUpWorktreeKeepLogs(t *testing.T) {
	mgr, store := managerWithConfig(t, cleanupConfig)
	seedWorktree(t, store, "feature/a", "feature-a", "web", "api")

	if err := CleanUpWorktree(mgr.cfg, store, "feature/a", map[string]bool{"feature/a": true}, true, nil); err != nil {
		t.Fatalf("CleanUpWorktree: %v", err)
	}
	st, _ := store.Load()
	for _, svc := range []string{"web", "api"} {
		if p := LogPath(store.Dir(), "feature-a", svc); !exists(p) {
			t.Errorf("keepLogs should keep %s", p)
		}
		if exists(store.ServiceLockPath("feature/a", svc)) {
			t.Errorf("keepLogs still deletes the lock for %s", svc)
		}
		if state.GetPortAssignment(st, "feature/a", svc) != 0 {
			t.Errorf("keepLogs still releases the port of %s", svc)
		}
	}
}

func TestCleanUpWorktreeKeepsLogsOfSharedHostLabel(t *testing.T) {
	mgr, store := managerWithConfig(t, cleanupConfig)
	// "feature/a" and "feature-a" both map to the Host label "feature-a".
	seedWorktree(t, store, "feature/a", "feature-a", "web")
	touch(t, store.ServiceLockPath("feature-a", "web"))

	active := map[string]bool{"feature/a": true, "feature-a": true}
	if err := CleanUpWorktree(mgr.cfg, store, "feature/a", active, false, nil); err != nil {
		t.Fatalf("CleanUpWorktree: %v", err)
	}
	if p := LogPath(store.Dir(), "feature-a", "web"); !exists(p) {
		t.Error("a log whose Host label a live worktree shares must be kept")
	}
	if exists(store.ServiceLockPath("feature/a", "web")) {
		t.Error("the branch's own lock file is not shared and should be deleted")
	}
	if !exists(store.ServiceLockPath("feature-a", "web")) {
		t.Error("the other branch's lock file must be kept")
	}
}

func TestCleanUpWorktreeMissingFilesAreNotErrors(t *testing.T) {
	mgr, store := managerWithConfig(t, cleanupConfig)
	// Nothing recorded, no logs directory, no lock files.
	if err := CleanUpWorktree(mgr.cfg, store, "feature/never-up", map[string]bool{"feature/never-up": true}, false, nil); err != nil {
		t.Fatalf("missing files should not be an error: %v", err)
	}
	// And a second run over an already cleaned worktree is a no-op.
	seedWorktree(t, store, "feature/a", "feature-a", "web")
	for range 2 {
		if err := CleanUpWorktree(mgr.cfg, store, "feature/a", map[string]bool{"feature/a": true}, false, nil); err != nil {
			t.Fatalf("CleanUpWorktree: %v", err)
		}
	}
}

// A service renamed or removed from config while it runs is invisible to the
// caller's StopServices, which stops configured services only. Cleanup must
// stop it rather than delete the only record of its PID.
func TestCleanUpWorktreeStopsServiceRemovedFromConfig(t *testing.T) {
	mgr, store := managerWithConfig(t, cleanupConfig)
	tree := &git.Worktree{Path: t.TempDir(), Branch: "feature/a"}
	if r := mgr.StartServices(tree, ""); r[0].Err != nil || r[1].Err != nil {
		t.Fatalf("start: %+v", r)
	}
	st, _ := store.Load()
	apiPID := state.GetServiceState(st, "feature/a", "api").PID

	// "api" is dropped from config; destroy stops only what config names.
	cfg := loadConfig(t, `
[services.web]
command = "sleep 120"
port_range = { min = 19100, max = 19199 }
proxy_port = 3000
`)
	mgr2 := NewManager(cfg, store, port.NewRegistry(store, cfg))
	for _, r := range mgr2.StopServices(tree, "") {
		if r.Err != nil {
			t.Fatalf("stop: %v", r.Err)
		}
	}
	if !IsProcessRunning(apiPID) {
		t.Fatal("precondition: api should still be running")
	}

	if err := CleanUpWorktree(cfg, store, "feature/a", map[string]bool{"feature/a": true}, false, nil); err != nil {
		t.Fatalf("CleanUpWorktree: %v", err)
	}
	if IsProcessRunning(apiPID) {
		_ = syscall.Kill(apiPID, syscall.SIGKILL)
		t.Fatal("cleanup must stop a recorded service that config no longer names")
	}
	assertCleaned(t, store, "feature/a", "feature-a", "api")
	assertCleaned(t, store, "feature/a", "feature-a", "web")
}

// A service that survives its stop keeps everything that lets a rerun or
// reconcile find and stop it: its record (with PID), port, lock and log.
func TestCleanUpWorktreeKeepsServiceThatFailsToStop(t *testing.T) {
	mgr, store := managerWithConfig(t, cleanupConfig)
	tree := &git.Worktree{Path: t.TempDir(), Branch: "feature/a"}
	if r := mgr.StartServices(tree, "web"); r[0].Err != nil {
		t.Fatalf("start: %v", r[0].Err)
	}
	defer mgr.StopServices(tree, "")
	st, _ := store.Load()
	pid := state.GetServiceState(st, "feature/a", "web").PID

	stopPID = func(int) error { return nil } // the process ignores the stop
	defer func() { stopPID = StopPID }()

	if err := CleanUpWorktree(mgr.cfg, store, "feature/a", map[string]bool{"feature/a": true}, false, nil); err == nil {
		t.Fatal("cleanup should report the service it could not stop")
	}
	st, _ = store.Load()
	if ss := state.GetServiceState(st, "feature/a", "web"); ss == nil || ss.PID != pid || !IsProcessRunning(pid) {
		t.Errorf("the running service's record must be kept, got %+v", ss)
	}
	assertKept(t, store, "feature/a", "feature-a", "web")
	// The other service, never started, is still cleaned up.
	assertCleaned(t, store, "feature/a", "feature-a", "api")
}

// Services the caller already failed to stop (destroy's StopServices errors)
// are kept the same way, without another stop attempt.
func TestCleanUpWorktreeKeepsServicesCallerFailedToStop(t *testing.T) {
	mgr, store := managerWithConfig(t, cleanupConfig)
	seedWorktree(t, store, "feature/a", "feature-a", "web", "api")

	if err := CleanUpWorktree(mgr.cfg, store, "feature/a", map[string]bool{"feature/a": true}, false, []string{"web"}); err == nil {
		t.Fatal("cleanup should report that it kept web")
	}
	assertKept(t, store, "feature/a", "feature-a", "web")
	assertCleaned(t, store, "feature/a", "feature-a", "api")
}

// An `isola up` that snapshotted the stop version before a destroy must see the
// destroy's stop and cancel, even though destroy prunes the service record.
func TestCleanUpWorktreeCancelsRacingStart(t *testing.T) {
	mgr, store := managerWithConfig(t, cleanupConfig)
	tree := &git.Worktree{Path: t.TempDir(), Branch: "feature/a"}

	// The racing start takes its snapshot before the destroy: no record yet.
	initial, _ := store.Snapshot()
	version := stopVersion(initial, tree.Branch, "web")

	mgr.StopServices(tree, "")
	if err := CleanUpWorktree(mgr.cfg, store, tree.Branch, map[string]bool{tree.Branch: true}, false, nil); err != nil {
		t.Fatalf("CleanUpWorktree: %v", err)
	}

	if err := mgr.checkStart(tree.Branch, "web", version); err == nil {
		t.Error("a start that began before the destroy must be canceled")
	}
	if _, err := mgr.prepareStart(tree.Branch, "web", 19100, false, version); err == nil {
		t.Error("prepareStart must refuse a start that began before the destroy")
	}

	// A fresh `up` after the destroy starts normally.
	if r := mgr.StartServices(tree, "web"); r[0].Err != nil {
		t.Fatalf("up after destroy: %v", r[0].Err)
	}
	mgr.StopServices(tree, "")
}

func TestParseLogName(t *testing.T) {
	for _, tc := range []struct {
		name, label, service string
		ok                   bool
	}{
		{"feature-a.web.log", "feature-a", "web", true},
		{"main.api.v2.log", "main", "api.v2", true},
		{"proxy.log", "", "", false},
		{"feature-a..log", "", "", false},
		{"feature-a.web.txt", "", "", false},
		{".web.log", "", "", false},
		{"noext", "", "", false},
	} {
		label, svc, ok := parseLogName(tc.name)
		if label != tc.label || svc != tc.service || ok != tc.ok {
			t.Errorf("parseLogName(%q) = %q, %q, %v; want %q, %q, %v", tc.name, label, svc, ok, tc.label, tc.service, tc.ok)
		}
	}
}

func TestRemoveLabelLogsSkipsNamesWithoutService(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{"proxy.log", "proxy.web.log"} {
		touch(t, filepath.Join(dir, f))
	}
	if err := removeLabelLogs(dir, "proxy", nil); err != nil {
		t.Fatal(err)
	}
	if !exists(filepath.Join(dir, "proxy.log")) {
		t.Error(`"proxy.log" has no service part and must not match label "proxy"`)
	}
	if exists(filepath.Join(dir, "proxy.web.log")) {
		t.Error(`"proxy.web.log" is a service log of label "proxy" and should be deleted`)
	}
}
