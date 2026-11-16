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
// the accessories (databases) isola provisioned for them, prunes their state,
// and deletes their service lock files and every service log under the state
// directory that no live worktree owns. Git has no worktree-removal hook, so
// this is how a removed worktree is cleaned up, automatically, on `isola up`
// and on the shared proxy's timer.
//
// It only drops resources isola recorded creating (via the state Handle), and
// the drivers refuse to touch a template or server database, so it can never
// destroy anything it did not provision. An accessory whose driver is gone or
// changed kind is left in place (retried on a later pass, or by `down --prune`).
func ReconcileOrphans(cfg *config.Config, store *state.FileStore, activeBranches map[string]bool) (ReconcileResult, error) {
	var res ReconcileResult

	type stopTarget struct {
		branch, name string
		rec          state.ServiceState // the record as it was, restored if the stop fails
	}
	type orphanAccessory struct {
		branch, name string
		rec          *state.AccessoryState
	}
	var toStop []stopTarget
	var orphanBranches []string
	var orphanAccessories []orphanAccessory
	orphanServices := map[string][]string{}

	// Phase 1 (locked): find orphaned branches, mark their live services stopped,
	// and snapshot the service names and the accessory records to act on outside
	// the lock.
	if err := store.WithLock(func() error {
		st, err := store.Load()
		if err != nil {
			return err
		}
		orphanBranches = state.OrphanedBranches(st, activeBranches)
		sort.Strings(orphanBranches)
		for _, branch := range orphanBranches {
			orphanServices[branch] = worktreeServiceNames(st, cfg, branch)
			for name, ss := range st.Services[branch] {
				if liveService(ss) {
					toStop = append(toStop, stopTarget{branch, name, *ss})
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

	// Phase 2 (unlocked): stop the processes and drop the accessories. A service
	// that survives its stop keeps its record, port, lock and log, so a later
	// pass retries it instead of losing track of a running process.
	kept := map[string]map[string]bool{} // branch -> service -> keep in state
	keep := func(branch, name string) {
		if kept[branch] == nil {
			kept[branch] = map[string]bool{}
		}
		kept[branch][name] = true
	}
	restore := map[string]state.ServiceState{}
	sort.Slice(toStop, func(i, j int) bool {
		return toStop[i].branch+"/"+toStop[i].name < toStop[j].branch+"/"+toStop[j].name
	})
	for _, t := range toStop {
		_ = stopPID(t.rec.PID)
		if IsProcessRunning(t.rec.PID) {
			logging.Warn("orphaned service %s/%s (pid %d) is still running; keeping it for a later pass", t.branch, t.name, t.rec.PID)
			keep(t.branch, t.name)
			restore[t.branch+"\x00"+t.name] = t.rec
			continue
		}
		res.StoppedServices = append(res.StoppedServices, t.branch+"/"+t.name)
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

	// Phase 3 (unlocked): delete the stopped services' lock files, before state
	// is pruned so a service whose lock file cannot go keeps the record that
	// lets a later pass find it.
	for _, branch := range orphanBranches {
		failed, err := removeServiceLocks(store, branch, orphanServices[branch], kept[branch])
		if err != nil {
			logging.Warn("removing lock files of orphaned worktree %s: %v", branch, err)
		}
		for svc := range failed {
			keep(branch, svc)
		}
	}

	// Phase 4 (locked): prune state for orphaned branches, keeping the services
	// above and the accessory records that failed to drop, so both are retried.
	if err := store.WithLock(func() error {
		st, err := store.Load()
		if err != nil {
			return err
		}
		for _, branch := range orphanBranches {
			for name := range st.Services[branch] {
				if !kept[branch][name] {
					delete(st.Services[branch], name)
				} else if rec, ok := restore[branch+"\x00"+name]; ok {
					st.Services[branch][name] = &rec
				}
			}
			if len(st.Services[branch]) == 0 {
				delete(st.Services, branch)
			}
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
			branch, svc := state.ParsePortKey(key)
			if !activeBranches[branch] && !kept[branch][svc] {
				delete(st.PortAssignments, key)
			}
		}
		return store.Save(st)
	}); err != nil {
		return res, err
	}

	// Phase 5: delete service logs no live worktree or remaining record owns:
	// the orphans' logs (unless a live worktree shares their Host label), and
	// any older strays.
	if err := sweepStrayLogs(store, activeBranches); err != nil {
		logging.Warn("removing stray service logs: %v", err)
	}

	res.PrunedBranches = orphanBranches
	return res, nil
}
