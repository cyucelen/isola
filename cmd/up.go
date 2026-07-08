package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cyucelen/isola/internal/copyfiles"
	"github.com/cyucelen/isola/internal/git"
	"github.com/cyucelen/isola/internal/logging"
	"github.com/cyucelen/isola/internal/port"
	"github.com/cyucelen/isola/internal/process"
	"github.com/cyucelen/isola/internal/proxy"
	"github.com/cyucelen/isola/internal/state"
	"github.com/spf13/cobra"
)

var (
	upAll     bool
	upService string
)

var upCmd = &cobra.Command{
	Use:   "up",
	Short: "Start dev servers for the current worktree",
	Long:  "Starts all configured services (or a specific one) for the current worktree, or all worktrees with --all.",
	RunE: func(cmd *cobra.Command, args []string) error {
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("getting current directory: %w", err)
		}

		// Validate service filter.
		if upService != "" {
			if _, ok := cfg.Services[upService]; !ok {
				return fmt.Errorf("unknown service %q", upService)
			}
		}

		stateDir := filepath.Join(stateRoot, ".isola")
		store, err := state.NewFileStore(stateDir)
		if err != nil {
			return fmt.Errorf("creating state store: %w", err)
		}

		registry := port.NewRegistry(store, cfg)
		mgr := process.NewManager(cfg, store, registry)

		var trees []git.Worktree
		if upAll {
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

		// Warn about branch slug collisions.
		if collisions := git.DetectSlugCollisions(trees); len(collisions) > 0 {
			for slug, branches := range collisions {
				logging.Warn("branches %v all map to slug %q; proxy routing may be ambiguous", branches, slug)
			}
		}

		totalStarted := 0
		for _, tree := range trees {
			if tree.IsBare {
				continue
			}
			logging.Verbose("starting services for worktree %s (%s)", tree.Branch, tree.Path)

			// Copy gitignored local files (e.g. .env) from the main worktree,
			// since git worktrees don't include them. Never overwrites; a
			// failure is a warning, not a blocker.
			if copied, err := copyfiles.Sync(cfg.FilesToCopy(), stateRoot, tree.Path); err != nil {
				logging.Warn("copying files into %s: %v", tree.Branch, err)
			} else if len(copied) > 0 {
				logging.Info("Copied %s into %s", strings.Join(copied, ", "), tree.Branch)
			}

			results := mgr.StartServices(&tree, upService)
			for _, r := range results {
				if r.Err != nil {
					logging.Error("starting %s/%s: %v", r.Branch, r.Service, r.Err)
				} else {
					logging.Info("Starting %s (port %d) for %s ...", r.Service, r.Port, r.Branch)
					totalStarted++
				}
			}
		}

		if totalStarted > 0 {
			noun := "services"
			if totalStarted == 1 {
				noun = "service"
			}
			if upAll {
				logging.Info("✓ %d %s started", totalStarted, noun)
			} else {
				logging.Info("✓ %d %s started for %s", totalStarted, noun, trees[0].Branch)
			}
		}

		// Auto-start the reverse proxy in the background (unless disabled) so
		// services are reachable at *.localhost without a separate command.
		if cfg.AutoProxyEnabled() {
			logDir := filepath.Join(store.Dir(), "logs")
			started, err := proxy.EnsureRunning(store, cwd, logDir, cfg.Proxy.HTTPS)
			scheme := "http"
			if cfg.Proxy.HTTPS {
				scheme = "https"
			}
			switch {
			case err != nil:
				logging.Warn("proxy auto-start failed: %v", err)
			case started:
				logging.Info("✓ proxy started; reach services at %s://<branch-slug>.localhost:<proxy_port>", scheme)
			default:
				logging.Verbose("proxy already running")
			}
		}

		return nil
	},
}

func init() {
	upCmd.Flags().BoolVar(&upAll, "all", false, "Start services for all worktrees")
	upCmd.Flags().StringVar(&upService, "service", "", "Start only a specific service")
	rootCmd.AddCommand(upCmd)
}
