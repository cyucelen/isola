package process

import (
	"context"
	"sort"

	"github.com/cyucelen/isola/internal/accessory"
	"github.com/cyucelen/isola/internal/config"
	"github.com/cyucelen/isola/internal/logging"
	"github.com/cyucelen/isola/internal/state"
)

// ReconcileResult reports what ReconcileOrphans tore down.
type ReconcileResult struct {
	StoppedServices    []string // "branch/service"
	DroppedAccessories []string // "branch/accessory"
	PrunedBranches     []string
}

// liveService reports whether ss records a running service whose process is
// still alive, pairing the state-level claim (ServiceState.IsRunning) with a
// liveness probe.
func liveService(ss *state.ServiceState) bool {
	return ss.IsRunning() && IsProcessRunning(ss.PID)
}

// ReconcileOrphans fully tears down worktrees that no longer exist on disk
// (branches recorded in state but absent from activeBranches, the set of
// branches that still have a worktree): it stops their running services, drops
// the accessories (databases) isola provisioned for them, and prunes their
// state. Git has no worktree-removal hook, so this is how a removed worktree is
// cleaned up, automatically, on `isola up` and on the shared proxy's timer.
//
// It only drops resources isola recorded creating (via the state Handle), and
// the drivers refuse to touch a template or server database, so it can never
// destroy anything it did not provision. An accessory whose driver is gone or
// changed kind is left in place (retried on a later pass, or by `down --prune`).
func ReconcileOrphans(cfg *config.Config, store *state.FileStore, activeBranches map[string]bool) (ReconcileResult, error) {
	var res ReconcileResult

	type stopTarget struct {
		id  string
		pid int
	}
	type orphanAccessory struct {
		branch, name string
		rec          *state.AccessoryState
	}
	var toStop []stopTarget
	var orphanBranches []string
	var orphanAccessories []orphanAccessory

	// Phase 1 (locked): find orphaned branches, mark their live services stopped,
	// and snapshot the accessory records to drop outside the lock.
	if err := store.WithLock(func() error {
		st, err := store.Load()
		if err != nil {
			return err
		}
		orphanBranches = state.OrphanedBranches(st, activeBranches)
		sort.Strings(orphanBranches)
		for _, branch := range orphanBranches {
			for name, ss := range st.Services[branch] {
				if liveService(ss) {
					toStop = append(toStop, stopTarget{id: branch + "/" + name, pid: ss.PID})
					state.SetServiceState(st, branch, name, state.StoppedServiceState(ss.Port))
				}
			}
			for name, rec := range state.BranchAccessories(st, branch) {
				orphanAccessories = append(orphanAccessories, orphanAccessory{branch, name, rec})
			}
		}
		if len(toStop) > 0 {
			return store.Save(st)
		}
		return nil
	}); err != nil {
		return res, err
	}

	// Phase 2 (unlocked): stop the processes and drop the accessories.
	for _, t := range toStop {
		_ = StopPID(t.pid)
		res.StoppedServices = append(res.StoppedServices, t.id)
	}
	dropped := map[string]bool{}
	if len(orphanAccessories) > 0 {
		drivers, err := accessory.BuildAll(cfg)
		if err != nil {
			// Without drivers we can't safely drop; leave records for a later pass.
			logging.Warn("cannot build accessories for orphan teardown; leaving %d for a later pass: %v", len(orphanAccessories), err)
		} else {
			for _, oa := range orphanAccessories {
				d, ok := drivers[oa.name]
				if !ok || d.Kind() != oa.rec.Kind {
					continue // config changed under this name; don't dispatch to the wrong driver
				}
				ctx, cancel := context.WithTimeout(context.Background(), accessory.OpTimeout)
				err := d.Drop(ctx, oa.rec.Handle)
				cancel()
				if err != nil {
					logging.Warn("dropping orphaned accessory %s/%s: %v", oa.branch, oa.name, err)
					continue
				}
				dropped[oa.branch+"\x00"+oa.name] = true
				res.DroppedAccessories = append(res.DroppedAccessories, oa.branch+"/"+oa.name)
			}
		}
	}

	// Phase 3 (locked): prune state for orphaned branches, clearing only the
	// accessory records that were successfully dropped so failures are retried.
	if err := store.WithLock(func() error {
		st, err := store.Load()
		if err != nil {
			return err
		}
		for _, branch := range orphanBranches {
			delete(st.Services, branch)
			for name := range st.Accessories[branch] {
				if dropped[branch+"\x00"+name] {
					delete(st.Accessories[branch], name)
				}
			}
			if len(st.Accessories[branch]) == 0 {
				delete(st.Accessories, branch)
			}
		}
		for key := range st.PortAssignments {
			branch, _ := state.ParsePortKey(key)
			if !activeBranches[branch] {
				delete(st.PortAssignments, key)
			}
		}
		return store.Save(st)
	}); err != nil {
		return res, err
	}

	res.PrunedBranches = orphanBranches
	return res, nil
}
