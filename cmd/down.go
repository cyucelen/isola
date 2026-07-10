package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/cyucelen/isola/internal/accessory"
	"github.com/cyucelen/isola/internal/git"
	"github.com/cyucelen/isola/internal/logging"
	"github.com/cyucelen/isola/internal/port"
	"github.com/cyucelen/isola/internal/process"
	"github.com/cyucelen/isola/internal/registry"
	"github.com/cyucelen/isola/internal/state"
	"github.com/spf13/cobra"
)

var (
	downAll     bool
	downService string
	downPrune   bool
)

var downCmd = &cobra.Command{
	Use:   "down",
	Short: "Stop dev servers for the current worktree",
	Long:  "Stops all running services (or a specific one) for the current worktree, or all worktrees with --all.",
	RunE: func(cmd *cobra.Command, args []string) error {
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("getting current directory: %w", err)
		}

		stateDir := filepath.Join(stateRoot, ".isola")
		store, err := state.NewFileStore(stateDir)
		if err != nil {
			return fmt.Errorf("creating state store: %w", err)
		}

		// Handle --prune: remove orphaned state entries.
		if downPrune {
			return pruneOrphanedState(store, cwd)
		}

		if downService != "" {
			if _, ok := cfg.Services[downService]; !ok {
				return fmt.Errorf("unknown service %q", downService)
			}
		}

		portReg := port.NewRegistry(store, cfg)
		mgr := process.NewManager(cfg, store, portReg)

		var trees []git.Worktree
		if downAll {
			trees, err = git.ListWorktrees(cwd)
			if err != nil {
				return fmt.Errorf("listing worktrees: %w", err)
			}
		} else {
			tree, err := git.CurrentWorktree(cwd)
			if err != nil {
				return fmt.Errorf("detecting worktree: %w", err)
			}
			trees = []git.Worktree{*tree}
		}

		totalStopped := 0
		for _, tree := range trees {
			if tree.IsBare {
				continue
			}
			results := mgr.StopServices(&tree, downService)
			for _, r := range results {
				if r.Err != nil {
					logging.Error("stopping %s/%s: %v", r.Branch, r.Service, r.Err)
				} else {
					logging.Info("Stopping %s for %s ...", r.Service, r.Branch)
					totalStopped++
				}
			}
		}

		if totalStopped > 0 {
			noun := "services"
			if totalStopped == 1 {
				noun = "service"
			}
			if downAll {
				logging.Info("✓ %d %s stopped", totalStopped, noun)
			} else {
				logging.Info("✓ %d %s stopped for %s", totalStopped, noun, trees[0].Branch)
			}
		}

		// Taking the whole project down removes its shared-proxy registration so
		// the daemon no longer advertises routes for it. The machine-wide daemon
		// keeps running for other projects; stop it with `isola proxy stop`.
		if downAll && downService == "" {
			if reg, err := registry.Open(); err == nil {
				if err := reg.Deregister(store.Dir()); err != nil {
					logging.Warn("deregistering project from proxy: %v", err)
				}
			}
		}

		return nil
	},
}

// pruneOrphanedState removes state entries for branches whose worktrees no
// longer exist, tearing down any accessories those branches provisioned.
func pruneOrphanedState(store *state.FileStore, cwd string) error {
	trees, err := git.ListWorktrees(cwd)
	if err != nil {
		return fmt.Errorf("listing worktrees: %w", err)
	}

	activeBranches := make(map[string]bool, len(trees))
	for _, t := range trees {
		if !t.IsBare {
			activeBranches[t.Branch] = true
		}
	}

	// Phase 1 (locked, read-only): find orphaned branches and snapshot the
	// accessory resources they own, so we can drop them without holding the
	// lock during network I/O.
	type orphanAccessory struct {
		branch, name string
		rec          *state.AccessoryState
	}
	var orphanBranches []string
	var orphanAccessories []orphanAccessory
	if err := store.WithLock(func() error {
		st, e := store.Load()
		if e != nil {
			return e
		}
		seen := map[string]bool{}
		addOrphan := func(branch string) {
			if !activeBranches[branch] && !seen[branch] {
				seen[branch] = true
				orphanBranches = append(orphanBranches, branch)
			}
		}
		for branch := range st.Services {
			addOrphan(branch)
		}
		for branch := range st.Accessories {
			addOrphan(branch)
		}
		sort.Strings(orphanBranches)
		for _, branch := range orphanBranches {
			for name, rec := range state.BranchAccessories(st, branch) {
				orphanAccessories = append(orphanAccessories, orphanAccessory{branch, name, rec})
			}
		}
		return nil
	}); err != nil {
		return fmt.Errorf("reading state: %w", err)
	}

	// Phase 2 (unlocked): drop the orphaned resources via their drivers.
	dropped := map[string]bool{}
	dropFailures := 0
	if len(orphanAccessories) > 0 {
		drivers, err := accessory.BuildAll(cfg)
		if err != nil {
			// Without drivers we cannot safely drop anything; leave every record
			// in place (they are retried on the next prune) rather than falsely
			// reporting the resources as gone.
			logging.Error("cannot build accessories for teardown; leaving %d accessory resource(s) for a later prune: %v",
				len(orphanAccessories), err)
			dropFailures = len(orphanAccessories)
		} else {
			for _, oa := range orphanAccessories {
				d, ok := drivers[oa.name]
				if !ok {
					logging.Warn("accessory %q for %s is no longer in config; leaving its %s resource untouched",
						oa.name, oa.branch, oa.rec.Kind)
					dropFailures++
					continue
				}
				// The recorded kind must match the current driver; a kind change
				// under a reused name would dispatch the drop to the wrong driver.
				if d.Kind() != oa.rec.Kind {
					logging.Warn("accessory %q for %s changed kind (%s -> %s); leaving its resource untouched",
						oa.name, oa.branch, oa.rec.Kind, d.Kind())
					dropFailures++
					continue
				}
				ctx, cancel := context.WithTimeout(context.Background(), accessory.OpTimeout)
				err := d.Drop(ctx, oa.rec.Handle)
				cancel()
				if err != nil {
					logging.Error("dropping accessory %s/%s: %v", oa.branch, oa.name, err)
					dropFailures++
					continue
				}
				logging.Info("Dropped %s (%s) for %s", oa.name, oa.rec.Kind, oa.branch)
				dropped[oa.branch+"\x00"+oa.name] = true
			}
		}
	}

	// Phase 3 (locked): delete state for orphaned branches, clearing only the
	// accessory records that were successfully dropped so failures are retried.
	if err := store.WithLock(func() error {
		st, e := store.Load()
		if e != nil {
			return e
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
		return fmt.Errorf("pruning state: %w", err)
	}

	if len(orphanBranches) > 0 {
		logging.Info("Pruned %d orphaned branch(es): %s", len(orphanBranches), strings.Join(orphanBranches, ", "))
		if dropFailures > 0 {
			logging.Warn("%d accessory resource(s) could not be dropped and were retained for a later prune", dropFailures)
		}
	} else {
		logging.Info("No orphaned state entries found.")
	}

	return nil
}

func init() {
	downCmd.Flags().BoolVar(&downAll, "all", false, "Stop services for all worktrees")
	downCmd.Flags().StringVar(&downService, "service", "", "Stop only a specific service")
	downCmd.Flags().BoolVar(&downPrune, "prune", false, "Remove state entries for deleted worktrees")
	rootCmd.AddCommand(downCmd)
}
