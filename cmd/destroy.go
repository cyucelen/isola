package cmd

import (
	"fmt"
	"os"

	"github.com/cyucelen/isola/internal/git"
	"github.com/cyucelen/isola/internal/logging"
	"github.com/cyucelen/isola/internal/port"
	"github.com/cyucelen/isola/internal/process"
	"github.com/spf13/cobra"
)

var destroyCmd = &cobra.Command{
	Use:   "destroy",
	Short: "Stop the current worktree's services and drop its data",
	Long: `Tear the current worktree down completely: stop its services (like
'isola down') and drop its per-worktree accessories/databases (like
'isola accessory drop'). The destructive counterpart to 'isola up'.

It affects only the current worktree and never touches other worktrees' data.
Handy before deleting a worktree by hand; a worktree that is simply removed is
torn down automatically by isola's reconcile. To stop services but keep the
data, use 'isola down' instead.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("getting current directory: %w", err)
		}
		tree, err := git.CurrentWorktree(cwd)
		if err != nil {
			return fmt.Errorf("detecting worktree: %w", err)
		}
		store, err := openStateStore()
		if err != nil {
			return err
		}

		// Stop this worktree's services.
		mgr := process.NewManager(cfg, store, port.NewRegistry(store, cfg))
		for _, r := range mgr.StopServices(tree, "") {
			if r.Err != nil {
				logging.Error("stopping %s/%s: %v", r.Branch, r.Service, r.Err)
			} else {
				logging.Info("Stopping %s for %s ...", r.Service, r.Branch)
			}
		}

		// Drop this worktree's accessories (databases).
		if err := accessoryForEach(nil, dropAccessory); err != nil {
			return fmt.Errorf("dropping accessories: %w", err)
		}

		logging.Info("✓ destroyed %s", tree.Branch)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(destroyCmd)
}
