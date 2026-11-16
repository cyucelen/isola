package process

import (
	"os"
	"path/filepath"
	"testing"

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

func TestCleanUpWorktreeRemovesOnlyThatWorktree(t *testing.T) {
	mgr, store := managerWithConfig(t, cleanupConfig)
	// "old" is no longer configured but still has a record, a port and files.
	seedWorktree(t, store, "feature/a", "feature-a", "web", "api", "old")
	seedWorktree(t, store, "main", "main", "web", "api")
	// "feature-ab" starts with "feature-a" but is a different Host label.
	seedWorktree(t, store, "feature/ab", "feature-ab", "web")

	active := map[string]bool{"feature/a": true, "main": true, "feature/ab": true}
	if err := CleanUpWorktree(mgr.cfg, store, "feature/a", active, false); err != nil {
		t.Fatalf("CleanUpWorktree: %v", err)
	}

	for _, svc := range []string{"web", "api", "old"} {
		if p := LogPath(store.Dir(), "feature-a", svc); exists(p) {
			t.Errorf("log %s should be deleted", p)
		}
		if p := store.ServiceLockPath("feature/a", svc); exists(p) {
			t.Errorf("lock for feature/a:%s should be deleted", svc)
		}
	}
	for _, f := range []string{
		LogPath(store.Dir(), "main", "web"),
		LogPath(store.Dir(), "main", "api"),
		LogPath(store.Dir(), "feature-ab", "web"),
		store.ServiceLockPath("main", "web"),
		store.ServiceLockPath("main", "api"),
		store.ServiceLockPath("feature/ab", "web"),
	} {
		if !exists(f) {
			t.Errorf("another worktree's file %s must be kept", f)
		}
	}

	st, _ := store.Load()
	if _, ok := st.Services["feature/a"]; ok {
		t.Error("feature/a service records should be removed")
	}
	for key := range st.PortAssignments {
		if b, _ := state.ParsePortKey(key); b == "feature/a" {
			t.Errorf("port assignment %s should be released", key)
		}
	}
	if state.GetAccessoryState(st, "feature/a", "db") == nil {
		t.Error("an accessory record must stay until its resource is dropped")
	}
	if state.GetServiceState(st, "main", "web") == nil || state.GetPortAssignment(st, "main", "web") == 0 {
		t.Error("another worktree's state must be kept")
	}
	if !exists(filepath.Join(store.Dir(), "state.json")) {
		t.Error("state.json itself must stay")
	}
}

func TestCleanUpWorktreeKeepLogs(t *testing.T) {
	mgr, store := managerWithConfig(t, cleanupConfig)
	seedWorktree(t, store, "feature/a", "feature-a", "web", "api")

	if err := CleanUpWorktree(mgr.cfg, store, "feature/a", map[string]bool{"feature/a": true}, true); err != nil {
		t.Fatalf("CleanUpWorktree: %v", err)
	}
	for _, svc := range []string{"web", "api"} {
		if p := LogPath(store.Dir(), "feature-a", svc); !exists(p) {
			t.Errorf("keepLogs should keep %s", p)
		}
		if p := store.ServiceLockPath("feature/a", svc); exists(p) {
			t.Errorf("keepLogs still deletes the lock for %s", svc)
		}
	}
	st, _ := store.Load()
	if _, ok := st.Services["feature/a"]; ok {
		t.Error("keepLogs still removes the state entries")
	}
}

func TestRemoveWorktreeFilesKeepsLogsOfSharedHostLabel(t *testing.T) {
	_, store := managerWithConfig(t, cleanupConfig)
	// "feature/a" and "feature-a" both map to the Host label "feature-a".
	seedWorktree(t, store, "feature/a", "feature-a", "web")
	touch(t, store.ServiceLockPath("feature-a", "web"))

	active := map[string]bool{"feature/a": true, "feature-a": true}
	if err := RemoveWorktreeFiles(store, "feature/a", []string{"web"}, active, false); err != nil {
		t.Fatalf("RemoveWorktreeFiles: %v", err)
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

	// Once the sharer is gone, the logs go too.
	if err := RemoveWorktreeFiles(store, "feature/a", []string{"web"}, map[string]bool{"feature/a": true}, false); err != nil {
		t.Fatalf("RemoveWorktreeFiles: %v", err)
	}
	if p := LogPath(store.Dir(), "feature-a", "web"); exists(p) {
		t.Error("without a sharing worktree the log should be deleted")
	}
}

func TestCleanUpWorktreeMissingFilesAreNotErrors(t *testing.T) {
	mgr, store := managerWithConfig(t, cleanupConfig)
	// Nothing recorded, no logs directory, no lock files.
	if err := CleanUpWorktree(mgr.cfg, store, "feature/never-up", map[string]bool{"feature/never-up": true}, false); err != nil {
		t.Fatalf("missing files should not be an error: %v", err)
	}
	// And a second run over an already cleaned worktree is a no-op.
	seedWorktree(t, store, "feature/a", "feature-a", "web")
	for range 2 {
		if err := CleanUpWorktree(mgr.cfg, store, "feature/a", map[string]bool{"feature/a": true}, false); err != nil {
			t.Fatalf("CleanUpWorktree: %v", err)
		}
	}
}
