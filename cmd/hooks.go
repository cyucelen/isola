package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/cyucelen/isola/internal/git"
	"github.com/cyucelen/isola/internal/hooks"
	"github.com/cyucelen/isola/internal/logging"
	"github.com/spf13/cobra"
)

var hooksShared bool

var hooksCmd = &cobra.Command{
	Use:   "hooks",
	Short: "Manage the git hook that starts a worktree on creation",
	Long: `Install a git post-checkout hook so creating a new worktree runs
'isola up' in it automatically.

git worktree add fires this hook (as do the tools built on it: Orca, Herd, your
editor), so it works everywhere. It runs only on a new worktree or fresh clone,
never on a branch switch, and is a no-op when the repo has no .isola.toml or
isola is not on PATH.`,
	Annotations: map[string]string{"skipRepoDetection": "true"},
}

var hooksInstallCmd = &cobra.Command{
	Use:         "install",
	Short:       "Install the post-checkout hook (--shared commits it for the team)",
	Annotations: map[string]string{"skipRepoDetection": "true"},
	RunE: func(cmd *cobra.Command, args []string) error {
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("getting current directory: %w", err)
		}

		if hooksShared {
			root, err := git.MainWorktreeRoot(cwd)
			if err != nil {
				return fmt.Errorf("locating main worktree: %w", err)
			}
			dir := filepath.Join(root, ".githooks")
			if _, err := hooks.Install(dir); err != nil {
				return fmt.Errorf("installing hook: %w", err)
			}
			if err := git.ConfigSet(cwd, "core.hooksPath", ".githooks"); err != nil {
				return fmt.Errorf("setting core.hooksPath: %w", err)
			}
			logging.Info("✓ installed the %s hook in .githooks and set core.hooksPath", hooks.HookName)
			logging.Info("Commit .githooks; each clone runs `isola hooks install --shared` (or `git config core.hooksPath .githooks`) once.")
			return nil
		}

		common, err := git.CommonDir(cwd)
		if err != nil {
			return fmt.Errorf("locating git common dir: %w", err)
		}
		dir := filepath.Join(common, "hooks")
		if _, err := hooks.Install(dir); err != nil {
			return fmt.Errorf("installing hook: %w", err)
		}
		logging.Info("✓ installed the %s hook in %s", hooks.HookName, dir)
		if hp := git.ConfigGet(cwd, "core.hooksPath"); hp != "" && hp != ".githooks" {
			logging.Warn("core.hooksPath is set to %q, so this hook may not run; add the isola block there or use --shared.", hp)
		}
		return nil
	},
}

var hooksUninstallCmd = &cobra.Command{
	Use:         "uninstall",
	Short:       "Remove the post-checkout hook isola installed",
	Annotations: map[string]string{"skipRepoDetection": "true"},
	RunE: func(cmd *cobra.Command, args []string) error {
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("getting current directory: %w", err)
		}
		removed := false
		for _, dir := range hookDirs(cwd) {
			changed, err := hooks.Uninstall(dir)
			if err != nil {
				return fmt.Errorf("removing hook from %s: %w", dir, err)
			}
			if changed {
				logging.Info("Removed the isola hook from %s", dir)
				removed = true
			}
		}
		if !removed {
			logging.Info("No isola hook was installed")
		}
		return nil
	},
}

var hooksStatusCmd = &cobra.Command{
	Use:         "status",
	Short:       "Show whether the post-checkout hook is installed",
	Annotations: map[string]string{"skipRepoDetection": "true"},
	RunE: func(cmd *cobra.Command, args []string) error {
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("getting current directory: %w", err)
		}
		hp := git.ConfigGet(cwd, "core.hooksPath")
		for _, dir := range hookDirs(cwd) {
			mark := "not installed"
			if hooks.Installed(dir) {
				mark = "installed"
			}
			logging.Info("  %-12s %s", mark, filepath.Join(dir, hooks.HookName))
		}
		if hp == "" {
			logging.Info("core.hooksPath is unset (git uses the repo's hooks dir)")
		} else {
			logging.Info("core.hooksPath = %s", hp)
		}
		return nil
	},
}

// hookDirs returns the local and shared hook directories for the repo at cwd.
func hookDirs(cwd string) []string {
	var dirs []string
	if common, err := git.CommonDir(cwd); err == nil {
		dirs = append(dirs, filepath.Join(common, "hooks"))
	}
	if root, err := git.MainWorktreeRoot(cwd); err == nil {
		dirs = append(dirs, filepath.Join(root, ".githooks"))
	}
	return dirs
}

func init() {
	hooksInstallCmd.Flags().BoolVar(&hooksShared, "shared", false, "Commit the hook to .githooks and set core.hooksPath for the team")
	hooksCmd.AddCommand(hooksInstallCmd, hooksUninstallCmd, hooksStatusCmd)
	rootCmd.AddCommand(hooksCmd)
}
