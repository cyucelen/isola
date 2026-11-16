package process

import (
	"path/filepath"
	"testing"

	"github.com/cyucelen/isola/internal/git"
	"github.com/cyucelen/isola/internal/state"
)

func TestReconcileOrphansTearsDownRemovedWorktree(t *testing.T) {
	mgr, store := managerWithConfig(t, `
[services.web]
command = "sleep 120"
port_range = { min = 19100, max = 19199 }
proxy_port = 3000
`)
	tree := &git.Worktree{Path: t.TempDir(), Branch: "feature/gone"}
	results := mgr.StartServices(tree, "")
	if len(results) != 1 || results[0].Err != nil {
		t.Fatalf("service should start: %+v", results)
	}

	// The worktree is gone (not in the active set) -> full teardown.
	res, err := ReconcileOrphans(mgr.cfg, store, map[string]bool{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.StoppedServices) != 1 || res.StoppedServices[0] != "feature/gone/web" {
		t.Fatalf("StoppedServices = %v, want [feature/gone/web]", res.StoppedServices)
	}
	if len(res.PrunedBranches) != 1 || res.PrunedBranches[0] != "feature/gone" {
		t.Fatalf("PrunedBranches = %v, want [feature/gone]", res.PrunedBranches)
	}

	// State for the removed branch is pruned entirely.
	st, _ := store.Load()
	if ss := state.GetServiceState(st, "feature/gone", "web"); ss != nil {
		t.Errorf("orphaned service state should be pruned, got %+v", ss)
	}
	if _, ok := st.Services["feature/gone"]; ok {
		t.Error("orphaned branch should be removed from state")
	}
}

func TestReconcileOrphansDropsAccessories(t *testing.T) {
	// The "faketest" driver (registered in manager_test) records a handle on
	// Provision and its Drop is a no-op, so reconcile should report it dropped.
	mgr, store := managerWithConfig(t, `
[services.web]
command = "sleep 120"
port_range = { min = 19100, max = 19199 }
proxy_port = 3000

[accessories.db]
kind = "faketest"
`)
	tree := &git.Worktree{Path: t.TempDir(), Branch: "feature/gone"}
	if r := mgr.StartServices(tree, ""); r[0].Err != nil {
		t.Fatalf("start: %v", r[0].Err)
	}

	res, err := ReconcileOrphans(mgr.cfg, store, map[string]bool{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.DroppedAccessories) != 1 || res.DroppedAccessories[0] != "feature/gone/db" {
		t.Fatalf("DroppedAccessories = %v, want [feature/gone/db]", res.DroppedAccessories)
	}
	st, _ := store.Load()
	if rec := state.GetAccessoryState(st, "feature/gone", "db"); rec != nil {
		t.Errorf("dropped accessory record should be pruned, got %+v", rec)
	}
}

func TestReconcileOrphansKeepsLiveWorktrees(t *testing.T) {
	mgr, store := managerWithConfig(t, `
[services.web]
command = "sleep 120"
port_range = { min = 19100, max = 19199 }
proxy_port = 3000
`)
	tree := &git.Worktree{Path: t.TempDir(), Branch: "main"}
	defer mgr.StopServices(tree, "")
	if r := mgr.StartServices(tree, ""); r[0].Err != nil {
		t.Fatalf("start: %v", r[0].Err)
	}

	// main still has a worktree -> must NOT be touched.
	res, err := ReconcileOrphans(mgr.cfg, store, map[string]bool{"main": true})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.StoppedServices) > 0 || len(res.DroppedAccessories) > 0 || len(res.PrunedBranches) > 0 {
		t.Errorf("a live worktree must not be reconciled, got %+v", res)
	}
	st, _ := store.Load()
	if ss := state.GetServiceState(st, "main", "web"); ss == nil || ss.Status != state.StatusRunning {
		t.Errorf("live service should stay running, got %+v", ss)
	}
}

func TestReconcileOrphansDeletesOrphanFiles(t *testing.T) {
	mgr, store := managerWithConfig(t, `
[services.web]
command = "sleep 120"
port_range = { min = 19100, max = 19199 }
proxy_port = 3000
`)
	gone := &git.Worktree{Path: t.TempDir(), Branch: "feature/gone"}
	live := &git.Worktree{Path: t.TempDir(), Branch: "main"}
	defer mgr.StopServices(live, "")
	for _, tree := range []*git.Worktree{gone, live} {
		if r := mgr.StartServices(tree, ""); r[0].Err != nil {
			t.Fatalf("start %s: %v", tree.Branch, r[0].Err)
		}
	}
	goneLog := LogPath(store.Dir(), "feature-gone", "web")
	liveLog := LogPath(store.Dir(), "main", "web")
	goneLock := store.ServiceLockPath("feature/gone", "web")
	liveLock := store.ServiceLockPath("main", "web")
	for _, f := range []string{goneLog, liveLog, goneLock, liveLock} {
		if !exists(f) {
			t.Fatalf("starting a service should create %s", f)
		}
	}

	if _, err := ReconcileOrphans(mgr.cfg, store, map[string]bool{"main": true}); err != nil {
		t.Fatal(err)
	}
	if exists(goneLog) || exists(goneLock) {
		t.Error("an orphaned worktree's log and lock file should be deleted")
	}
	if !exists(liveLog) || !exists(liveLock) {
		t.Error("a live worktree's log and lock file must be kept")
	}
}

// An orphan whose Host label a live worktree shares ("feature/a" and
// "feature-a" both map to "feature-a") must not take the live worktree's logs
// with it. Its own lock file still goes.
func TestReconcileOrphansKeepsLogsOfSharedHostLabel(t *testing.T) {
	mgr, store := managerWithConfig(t, cleanupConfig)
	seedWorktree(t, store, "feature/a", "feature-a", "web")
	seedWorktree(t, store, "feature-a", "feature-a", "web")

	res, err := ReconcileOrphans(mgr.cfg, store, map[string]bool{"feature-a": true})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.PrunedBranches) != 1 || res.PrunedBranches[0] != "feature/a" {
		t.Fatalf("PrunedBranches = %v, want [feature/a]", res.PrunedBranches)
	}
	if exists(store.ServiceLockPath("feature/a", "web")) {
		t.Error("the orphan's own lock file should be deleted")
	}
	assertKept(t, store, "feature-a", "feature-a", "web")
}

// A service that survives its stop keeps its record (with PID), port, lock and
// log, so the next pass finds it and tries again.
func TestReconcileOrphansKeepsServiceThatFailsToStop(t *testing.T) {
	mgr, store := managerWithConfig(t, cleanupConfig)
	tree := &git.Worktree{Path: t.TempDir(), Branch: "feature/gone"}
	if r := mgr.StartServices(tree, "web"); r[0].Err != nil {
		t.Fatalf("start: %v", r[0].Err)
	}
	defer mgr.StopServices(tree, "")
	st, _ := store.Load()
	pid := state.GetServiceState(st, "feature/gone", "web").PID

	stopPID = func(int) error { return nil } // the process ignores the stop
	defer func() { stopPID = StopPID }()

	res, err := ReconcileOrphans(mgr.cfg, store, map[string]bool{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.StoppedServices) != 0 {
		t.Errorf("a service that is still running is not stopped, got %v", res.StoppedServices)
	}
	st, _ = store.Load()
	ss := state.GetServiceState(st, "feature/gone", "web")
	if ss == nil || ss.PID != pid || ss.Status != state.StatusRunning {
		t.Fatalf("the running service's record must be restored, got %+v", ss)
	}
	assertKept(t, store, "feature/gone", "feature-gone", "web")

	// With the stop working again, the next pass finishes the job.
	stopPID = StopPID
	if _, err := ReconcileOrphans(mgr.cfg, store, map[string]bool{}); err != nil {
		t.Fatal(err)
	}
	if IsProcessRunning(pid) {
		t.Error("the next pass should stop the service")
	}
	assertCleaned(t, store, "feature/gone", "feature-gone", "web")
}

// Logs no per-branch cleanup reaches are swept once no live worktree or state
// record owns their label: logs kept by --keep-logs whose worktree is gone, and
// logs from before isola deleted them.
func TestReconcileSweepsStrayLogs(t *testing.T) {
	mgr, store := managerWithConfig(t, cleanupConfig)
	logs := filepath.Join(store.Dir(), "logs")
	// A destroyed worktree's tombstone keeps its label until the worktree goes.
	seedWorktree(t, store, "feature/kept", "feature-kept", "web")
	for _, f := range []string{
		"old-branch.web.log",   // no record, no worktree: stray
		"main.web.log",         // live worktree
		"feature-kept.web.log", // recorded branch (destroyed with --keep-logs)
		"proxy.log",            // not a service log
	} {
		touch(t, filepath.Join(logs, f))
	}

	active := map[string]bool{"main": true, "feature/kept": true}
	if _, err := ReconcileOrphans(mgr.cfg, store, active); err != nil {
		t.Fatal(err)
	}
	if exists(filepath.Join(logs, "old-branch.web.log")) {
		t.Error("a log no worktree or record owns should be swept")
	}
	for _, f := range []string{"main.web.log", "feature-kept.web.log", "proxy.log"} {
		if !exists(filepath.Join(logs, f)) {
			t.Errorf("%s must be kept", f)
		}
	}

	// Once the destroyed worktree is removed, its kept logs go too.
	delete(active, "feature/kept")
	if _, err := ReconcileOrphans(mgr.cfg, store, active); err != nil {
		t.Fatal(err)
	}
	if exists(filepath.Join(logs, "feature-kept.web.log")) {
		t.Error("kept logs should be swept once their worktree is removed")
	}
}
