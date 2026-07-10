package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cyucelen/isola/internal/config"
	"github.com/cyucelen/isola/internal/copyfiles"
	"github.com/cyucelen/isola/internal/git"
	"github.com/cyucelen/isola/internal/logging"
	"github.com/cyucelen/isola/internal/port"
	"github.com/cyucelen/isola/internal/process"
	"github.com/cyucelen/isola/internal/proxy"
	"github.com/cyucelen/isola/internal/registry"
	"github.com/cyucelen/isola/internal/state"
	"github.com/cyucelen/isola/internal/trust"
	"github.com/spf13/cobra"
)

var (
	upAll     bool
	upService string
)

var upCmd = &cobra.Command{
	Use:   "up",
	Short: "Start services for the current worktree",
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

		portReg := port.NewRegistry(store, cfg)
		mgr := process.NewManager(cfg, store, portReg)

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
		totalRunning := 0
		totalFailed := 0
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
				switch {
				case r.Err != nil:
					logging.Error("starting %s/%s: %v", r.Branch, r.Service, r.Err)
					totalFailed++
				case r.AlreadyRunning:
					logging.Info("%s already running (port %d) for %s", r.Service, r.Port, r.Branch)
					totalRunning++
				default:
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
		} else if totalRunning > 0 && totalFailed == 0 {
			logging.Info("✓ already up to date; all services running")
		}

		// Register this project with the shared proxy and ensure the machine-wide
		// daemon is running (unless disabled), so services are reachable at
		// <branch>.<project>.localhost without a separate command.
		if cfg.AutoProxyEnabled() {
			reg, err := registry.Open()
			if err != nil {
				logging.Warn("proxy registry unavailable: %v", err)
			} else {
				// Capture the project's previous registration before overwriting
				// it, so we can tell whether the proxy scheme or ports changed.
				prev, hadPrev, _ := reg.Lookup(cfg.Project)
				if regErr := reg.Register(registry.Project{
					Name:       cfg.Project,
					StateDir:   store.Dir(),
					ProxyPorts: cfg.ProxyPorts(),
					HTTPS:      cfg.Proxy.HTTPS,
				}); regErr != nil {
					logging.Warn("registering project with proxy: %v", regErr)
				} else {
					started, derr := proxy.EnsureDaemon(reg)
					switch {
					case derr != nil:
						logging.Warn("proxy daemon start failed: %v", derr)
					case started:
						logging.Info("✓ proxy started")
					default:
						logging.Verbose("proxy already running")
						// A running shared daemon binds each port with the scheme
						// it read at startup; it won't pick up a scheme/port change
						// for this project until it is restarted.
						if hadPrev && proxyConfigChanged(prev, cfg) {
							logging.Warn("proxy config changed but the shared proxy is already running with the old settings; restart it with `isola proxy stop` then `isola up` to apply.")
						}
					}

					// Only advertise reachability when something is actually up;
					// printing it after a failed start reads as false success.
					if totalStarted > 0 || totalRunning > 0 {
						scheme := "http"
						if cfg.Proxy.HTTPS {
							scheme = "https"
						}
						logging.Info("Reach services at %s://<branch-slug>.%s.localhost:<proxy_port>", scheme, cfg.Project)
					}

					// With HTTPS, trust the shared CA so browsers don't warn. Never
					// blocks `up`; only runs on an interactive terminal.
					if derr == nil && cfg.Proxy.HTTPS && cfg.AutoTrustEnabled() {
						ensureHTTPSTrust(filepath.Join(reg.Dir(), "certs", "ca.crt"))
					}
				}
			}
		}

		if totalFailed > 0 {
			return fmt.Errorf("%d service(s) failed to start; see the errors above", totalFailed)
		}
		return nil
	},
}

// ensureHTTPSTrust trusts the auto-generated HTTPS CA so browsers accept it.
// It never fails `up`: if trust is not already established it installs it on an
// interactive terminal (one password prompt), and if that is declined,
// unsupported, or the session is non-interactive, it warns and returns.
func ensureHTTPSTrust(caPath string) {
	// The proxy writes the CA as it starts; give it a moment to appear.
	for i := 0; i < 20; i++ {
		if _, err := os.Stat(caPath); err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if _, err := os.Stat(caPath); err != nil {
		return // no CA to trust yet
	}
	if trust.IsTrusted(caPath) {
		return
	}
	if !trust.Supported() || !trust.Interactive() {
		logging.Warn("HTTPS CA not trusted; browsers will warn. Run `isola trust` once in a terminal, or click through the warning.")
		return
	}
	logging.Info("HTTPS CA not yet trusted; installing it now (you may be prompted for your password).")
	if err := trust.Install(caPath); err != nil {
		logging.Warn("could not install the HTTPS CA (%v); continuing. Browsers will warn until you run `isola trust`.", err)
		return
	}
	logging.Info("✓ HTTPS CA trusted")
}

// proxyConfigChanged reports whether the proxy-relevant config (HTTPS scheme or
// the set of proxy ports) differs from a project's previous registration, i.e.
// whether an already-running shared daemon would be serving stale settings.
func proxyConfigChanged(prev registry.Project, c *config.Config) bool {
	if prev.HTTPS != c.Proxy.HTTPS {
		return true
	}
	return !sameInts(prev.ProxyPorts, c.ProxyPorts())
}

func sameInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	seen := make(map[int]int, len(a))
	for _, v := range a {
		seen[v]++
	}
	for _, v := range b {
		seen[v]--
	}
	for _, n := range seen {
		if n != 0 {
			return false
		}
	}
	return true
}

func init() {
	upCmd.Flags().BoolVar(&upAll, "all", false, "Start services for all worktrees")
	upCmd.Flags().StringVar(&upService, "service", "", "Start only a specific service")
	rootCmd.AddCommand(upCmd)
}
