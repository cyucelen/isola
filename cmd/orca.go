package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/cyucelen/isola/internal/git"
	"github.com/cyucelen/isola/internal/logging"
	"github.com/cyucelen/isola/internal/orca"
	"github.com/spf13/cobra"
)

var orcaCmd = &cobra.Command{
	Use:   "orca",
	Short: "Wire `isola up` into Orca's worktree setup hook (orca.yaml)",
	Long: `Add 'isola up' to orca.yaml's scripts.setup so Orca (onorca.dev) brings
up each new worktree's isolated environment when it creates the worktree.
Existing setup content and the rest of orca.yaml are preserved.

Teardown needs no hook: isola reconciles removed worktrees automatically (stops
their services and drops the databases they provisioned). For worktree managers
without their own hooks, use 'isola hooks install' instead (a tool-agnostic git
post-checkout hook).`,
	Annotations: map[string]string{"skipRepoDetection": "true"},
	RunE: func(cmd *cobra.Command, args []string) error {
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("getting current directory: %w", err)
		}
		root, err := git.MainWorktreeRoot(cwd)
		if err != nil {
			return fmt.Errorf("locating main worktree: %w", err)
		}
		path := filepath.Join(root, orca.FileName)
		changed, err := orca.Upsert(path)
		if err != nil {
			return fmt.Errorf("updating %s: %w", orca.FileName, err)
		}
		if changed {
			logging.Info("✓ added `isola up` to %s (scripts.setup)", orca.FileName)
		} else {
			logging.Info("%s already runs `isola up`; nothing to do", orca.FileName)
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(orcaCmd)
}
