package cmd

import (
	"errors"
	"fmt"
	"os"

	"github.com/cyucelen/isola/internal/git"
	"github.com/cyucelen/isola/internal/logging"
	"github.com/cyucelen/isola/internal/port"
	"github.com/cyucelen/isola/internal/process"
	"github.com/spf13/cobra"
)

var destroyKeepLogs bool

var destroyCmd = &cobra.Command{
	Use:   "destroy",
	Short: "Stop the current worktree's services and drop its data",
	Long: `Tear the current worktree down completely: stop its services (like
'isola down'), drop its per-worktree accessories/databases (like
'isola accessory drop'), and delete what isola keeps for it under .isola/:
its service logs, its service lock files, and its entries in state.json
(releasing its stable ports). The destructive counterpart to 'isola up'.
Pass --keep-logs to keep the service logs.

It affects only the current worktree and never touches other worktrees' data,
files inside the worktree, or ~/.isola. Logs are also kept when another live
worktree shares this worktree's host label. Handy before deleting a worktree
by hand; a worktree that is simply removed is torn down automatically by
isola's reconcile. To stop services but keep the data, use 'isola down'
instead.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("getting current directory: %w", err)
		}
		tree, err := git.CurrentWorktree(cwd)
		if err != nil {
			return fmt.Errorf("detecting worktree: %w", err)
		}
		trees, err := git.ListWorktrees(cwd)
		if err != nil {
			return fmt.Errorf("listing worktrees: %w", err)
		}
		store, err := openStateStore()
		if err != nil {
			return err
		}

		// Stop this worktree's services.
		mgr := process.NewManager(cfg, store, port.NewRegistry(store, cfg))
		stopFailed := 0
		for _, r := range mgr.StopServices(tree, "") {
			if r.Err != nil {
				stopFailed++
				logging.Error("stopping %s/%s: %v", r.Branch, r.Service, r.Err)
			} else {
				logging.Info("Stopping %s for %s ...", r.Service, r.Branch)
			}
		}

		// Drop this worktree's accessories (databases). A failed drop keeps its
		// record so a later drop or reconcile retries it; the rest still goes.
		var errs []error
		if err := accessoryForEach(nil, dropAccessory); err != nil {
			errs = append(errs, fmt.Errorf("dropping accessories: %w", err))
		}

		// Delete the worktree's logs, lock files, and state entries. A service
		// that failed to stop may still be running, so its record and lock file
		// must stay; leave everything for a rerun rather than orphan its process.
		if stopFailed > 0 {
			errs = append(errs, fmt.Errorf("%d service(s) failed to stop; kept the worktree's logs and state", stopFailed))
		} else {
			active := git.ActiveBranches(trees)
			active[tree.Branch] = true
			if err := process.CleanUpWorktree(cfg, store, tree.Branch, active, destroyKeepLogs); err != nil {
				errs = append(errs, fmt.Errorf("cleaning up files: %w", err))
			}
		}

		if err := errors.Join(errs...); err != nil {
			return err
		}
		logging.Info("✓ destroyed %s", tree.Branch)
		return nil
	},
}

func init() {
	destroyCmd.Flags().BoolVar(&destroyKeepLogs, "keep-logs", false, "Keep the worktree's service logs")
	rootCmd.AddCommand(destroyCmd)
}
