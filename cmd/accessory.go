package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/cyucelen/isola/internal/accessory"
	"github.com/cyucelen/isola/internal/git"
	"github.com/cyucelen/isola/internal/logging"
	"github.com/cyucelen/isola/internal/state"
	"github.com/spf13/cobra"
)

var accessoryCmd = &cobra.Command{
	Use:   "accessory",
	Short: "Manage per-worktree accessories (databases, …)",
	Long: `Bring up, reset, and drop the isolated stateful dependencies declared
under [accessories] in .isola.toml.

Accessories are normally brought up automatically on 'isola up' and torn down
on 'isola down --prune'. These verbs let you operate them out of band. Pass a
name to act on a single accessory; omit it to act on all.`,
}

var accessoryLsCmd = &cobra.Command{
	Use:   "ls",
	Short: "List accessories and their state",
	RunE: func(cmd *cobra.Command, args []string) error {
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		trees, err := git.ListWorktrees(cwd)
		if err != nil {
			return fmt.Errorf("listing worktrees: %w", err)
		}
		store, err := openStateStore()
		if err != nil {
			return err
		}
		st, err := store.LoadLocked()
		if err != nil {
			return fmt.Errorf("loading state: %w", err)
		}

		names := sortedAccessoryNames()
		if len(names) == 0 {
			logging.Info("No accessories configured in %s.", ".isola.toml")
			return nil
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
		_, _ = fmt.Fprintln(w, "WORKTREE\tACCESSORY\tKIND\tRESOURCE\tPROVISIONED")
		for _, tree := range trees {
			if tree.IsBare {
				continue
			}
			for _, name := range names {
				kind, _ := cfg.AccessoryKind(cfg.Accessories[name])
				resource, provisioned := "—", "no"
				if rec := state.GetAccessoryState(st, tree.Branch, name); rec != nil {
					resource, provisioned, kind = formatHandle(rec.Handle), "yes", rec.Kind
				}
				_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", tree.Branch, name, kind, resource, provisioned)
			}
		}
		return w.Flush()
	},
}

var accessoryUpCmd = &cobra.Command{
	Use:   "up [name]",
	Short: "Bring up the current worktree's accessories (reuse if present)",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return accessoryForEach(args, func(ctx context.Context, store *state.FileStore, wt accessory.WorktreeInfo, name string, a accessory.Accessory) error {
			prov, err := a.Provision(ctx, wt)
			if err != nil {
				return err
			}
			recordAccessory(store, wt.Branch, name, a.Kind(), prov.Handle)
			logging.Info("Brought up %s (%s) for %s", name, a.Kind(), wt.Branch)
			logging.Info("  reference it as ${accessories.%s.url} (%s)", name, prov.URL)
			return nil
		})
	},
}

var accessoryResetCmd = &cobra.Command{
	Use:   "reset [name]",
	Short: "Reset accessories for the current worktree to their template baseline",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return accessoryForEach(args, func(ctx context.Context, store *state.FileStore, wt accessory.WorktreeInfo, name string, a accessory.Accessory) error {
			r, ok := a.(accessory.Resettable)
			if !ok {
				return fmt.Errorf("kind %q does not support reset", a.Kind())
			}
			prov, err := r.Reset(ctx, wt)
			if err != nil {
				return err
			}
			recordAccessory(store, wt.Branch, name, a.Kind(), prov.Handle)
			logging.Info("Reset %s (%s) for %s", name, a.Kind(), wt.Branch)
			return nil
		})
	},
}

var accessoryDropCmd = &cobra.Command{
	Use:   "drop [name]",
	Short: "Drop the current worktree's accessories",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return accessoryForEach(args, dropAccessory)
	},
}

// dropAccessory tears down one accessory's recorded resource for a worktree and
// forgets its state. Shared by `isola accessory drop` and `isola destroy`.
func dropAccessory(ctx context.Context, store *state.FileStore, wt accessory.WorktreeInfo, name string, a accessory.Accessory) error {
	var rec *state.AccessoryState
	if err := store.WithLock(func() error {
		st, e := store.Load()
		if e != nil {
			return e
		}
		rec = state.GetAccessoryState(st, wt.Branch, name)
		return nil
	}); err != nil {
		return err
	}
	if rec == nil {
		logging.Info("No %s resource recorded for %s; nothing to drop", name, wt.Branch)
		return nil
	}
	if err := a.Drop(ctx, rec.Handle); err != nil {
		return err
	}
	forgetAccessory(store, wt.Branch, name)
	logging.Info("Dropped %s (%s) for %s", name, rec.Kind, wt.Branch)
	return nil
}

// accessoryForEach runs fn for each configured accessory (optionally filtered by
// a positional name) against the current worktree, under a per-operation timeout.
func accessoryForEach(args []string, fn func(context.Context, *state.FileStore, accessory.WorktreeInfo, string, accessory.Accessory) error) error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	tree, err := git.CurrentWorktree(cwd)
	if err != nil {
		return fmt.Errorf("detecting worktree: %w", err)
	}
	accs, err := accessory.BuildAll(cfg)
	if err != nil {
		return err
	}
	filter := ""
	if len(args) > 0 {
		filter = args[0]
	}
	names, err := selectedAccessories(accs, filter)
	if err != nil {
		return err
	}
	if len(names) == 0 {
		logging.Info("No accessories configured in %s.", ".isola.toml")
		return nil
	}
	store, err := openStateStore()
	if err != nil {
		return err
	}
	wt := accessory.FromWorktree(tree, cfg.Project)

	var firstErr error
	for _, name := range names {
		ctx, cancel := context.WithTimeout(context.Background(), accessory.OpTimeout)
		err := fn(ctx, store, wt, name, accs[name])
		cancel()
		if err != nil {
			logging.Error("%s: %v", name, err)
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

// openStateStore opens the repo's state store at <stateRoot>/.isola. It is the
// single constructor the commands share instead of each inlining the path.
func openStateStore() (*state.FileStore, error) {
	store, err := state.NewFileStore(filepath.Join(stateRoot, ".isola"))
	if err != nil {
		return nil, fmt.Errorf("creating state store: %w", err)
	}
	return store, nil
}

// selectedAccessories returns the accessory names to act on, honoring an
// optional single-name filter.
func selectedAccessories(accs map[string]accessory.Accessory, filter string) ([]string, error) {
	if filter != "" {
		if _, ok := accs[filter]; !ok {
			return nil, fmt.Errorf("unknown accessory %q", filter)
		}
		return []string{filter}, nil
	}
	names := make([]string, 0, len(accs))
	for name := range accs {
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

func sortedAccessoryNames() []string {
	names := make([]string, 0, len(cfg.Accessories))
	for name := range cfg.Accessories {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// formatHandle renders a driver Handle compactly for display (e.g. "database=myapp_x").
func formatHandle(handle map[string]string) string {
	if len(handle) == 0 {
		return "—"
	}
	keys := make([]string, 0, len(handle))
	for k := range handle {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+handle[k])
	}
	return strings.Join(parts, ",")
}

func recordAccessory(store *state.FileStore, branch, name, kind string, handle map[string]string) {
	if err := store.RecordAccessory(branch, name, kind, handle); err != nil {
		logging.Warn("failed to record accessory state %s/%s: %v", branch, name, err)
	}
}

func forgetAccessory(store *state.FileStore, branch, name string) {
	if err := store.ForgetAccessory(branch, name); err != nil {
		logging.Warn("failed to clear accessory state %s/%s: %v", branch, name, err)
	}
}

func init() {
	accessoryCmd.AddCommand(accessoryLsCmd, accessoryUpCmd, accessoryResetCmd, accessoryDropCmd)
	rootCmd.AddCommand(accessoryCmd)
}
