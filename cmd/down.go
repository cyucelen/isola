package cmd

import (
	"fmt"
	"os"
	"strings"

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
	Short: "Stop services for the current worktree",
	Long:  "Stops all running services (or a specific one) for the current worktree, or all worktrees with --all.",
	RunE: func(cmd *cobra.Command, args []string) error {
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("getting current directory: %w", err)
		}

		store, err := openStateStore()
		if err != nil {
			return err
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

// pruneOrphanedState tears down worktrees that no longer exist on disk: stop
// their services, drop the databases they provisioned, and prune their state.
// It shares ReconcileOrphans with `isola up` and the shared proxy, which run the
// same teardown automatically; `--prune` is the explicit, on-demand form.
func pruneOrphanedState(store *state.FileStore, cwd string) error {
	trees, err := git.ListWorktrees(cwd)
	if err != nil {
		return fmt.Errorf("listing worktrees: %w", err)
	}
	res, err := process.ReconcileOrphans(cfg, store, git.ActiveBranches(trees))
	if err != nil {
		return fmt.Errorf("pruning state: %w", err)
	}
	if len(res.PrunedBranches) > 0 {
		logging.Info("Pruned %d orphaned branch(es): %s", len(res.PrunedBranches), strings.Join(res.PrunedBranches, ", "))
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
