package process

import (
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
