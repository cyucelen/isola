package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"text/tabwriter"

	"github.com/cyucelen/isola/internal/config"
	"github.com/cyucelen/isola/internal/git"
	"github.com/cyucelen/isola/internal/logging"
	"github.com/cyucelen/isola/internal/process"
	"github.com/cyucelen/isola/internal/proxy"
	"github.com/cyucelen/isola/internal/registry"
	"github.com/cyucelen/isola/internal/state"
	"github.com/spf13/cobra"
)

type lsEntry struct {
	Worktree  string `json:"worktree"`
	Service   string `json:"service"`
	Port      int    `json:"port"`
	Status    string `json:"status"`
	PID       int    `json:"pid"`
	URL       string `json:"url,omitempty"`
	DirectURL string `json:"direct_url,omitempty"`
}

var lsCmd = &cobra.Command{
	Use:   "ls",
	Short: "List all worktrees and their services",
	Long: `List all git worktrees and the status of each configured service.

Displays a table with worktree branch, service name, allocated port,
running status, and PID for each service.

Use --json to output the result as a JSON array for scripting and automation.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("getting current directory: %w", err)
		}

		trees, err := git.ListWorktrees(cwd)
		if err != nil {
			return fmt.Errorf("listing worktrees: %w", err)
		}

		// Load state for runtime info.
		stateDir := filepath.Join(stateRoot, ".isola")
		store, err := state.NewFileStore(stateDir)
		if err != nil {
			return fmt.Errorf("creating state store: %w", err)
		}

		var st *state.State
		if err := store.WithLock(func() error {
			var e error
			st, e = store.Load()
			return e
		}); err != nil {
			logging.Warn("failed to load state: %v", err)
		}
		if st == nil {
			st = &state.State{
				Services:        map[string]map[string]*state.ServiceState{},
				PortAssignments: map[string]int{},
			}
		}

		// Sort service names for consistent output.
		serviceNames := make([]string, 0, len(cfg.Services))
		for name := range cfg.Services {
			serviceNames = append(serviceNames, name)
		}
		sort.Strings(serviceNames)

		// Proxy status comes from the shared machine-wide daemon, not repo state.
		daemonRunning := false
		if reg, err := registry.Open(); err == nil {
			daemonRunning, _ = proxy.DaemonRunning(reg)
		}
		entries := buildLsEntries(trees, serviceNames, st, cfg, daemonRunning)

		// Detect orphaned branches: in state but not in worktree list.
		activeBranches := make(map[string]bool, len(trees))
		for _, t := range trees {
			if !t.IsBare {
				activeBranches[t.Branch] = true
			}
		}
		orphanBranches := state.OrphanedBranches(st, activeBranches)
		sort.Strings(orphanBranches)
		for _, branch := range orphanBranches {
			for _, svcName := range serviceNames {
				entries = append(entries, lsEntry{
					Worktree: branch + " (orphaned)",
					Service:  svcName,
					Status:   state.StatusStopped,
				})
			}
		}

		jsonFlag, _ := cmd.Flags().GetBool("json")
		if jsonFlag {
			return json.NewEncoder(os.Stdout).Encode(entries)
		}

		return printLsTable(entries)
	},
}

func buildLsEntries(trees []git.Worktree, serviceNames []string, st *state.State, c *config.Config, proxyRunning bool) []lsEntry {
	// The scheme follows this project's proxy config.
	scheme := "http"
	if c != nil && c.Proxy.HTTPS {
		scheme = "https"
	}

	entries := make([]lsEntry, 0)
	for _, tree := range trees {
		if tree.IsBare {
			continue
		}
		branch := tree.Branch
		if branch == "" {
			branch = "(detached)"
		}

		slug := tree.Slug()

		for _, svcName := range serviceNames {
			e := lsEntry{
				Worktree: branch,
				Service:  svcName,
				Status:   state.StatusStopped,
			}

			ss := state.GetServiceState(st, tree.Branch, svcName)
			if ss != nil {
				e.Port = ss.Port
				switch {
				case ss.PID > 0 && process.IsProcessRunning(ss.PID):
					e.Status = state.StatusRunning
					e.PID = ss.PID
				case ss.Status == state.StatusRunning && ss.PID > 0:
					e.Status = state.StatusStopped // stale
				default:
					e.Status = ss.Status
				}
			}

			// Build URLs (project-qualified for the shared proxy).
			if proxyRunning && c != nil {
				if svc, ok := c.Services[svcName]; ok {
					e.URL = fmt.Sprintf("%s://%s.%s.localhost:%d", scheme, slug, c.Project, svc.ProxyPort)
				}
			}
			if e.Port > 0 {
				e.DirectURL = fmt.Sprintf("http://localhost:%d", e.Port)
			}

			entries = append(entries, e)
		}
	}
	return entries
}

func printLsTable(entries []lsEntry) error {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	_, _ = fmt.Fprintln(w, "WORKTREE\tSERVICE\tPORT\tSTATUS\tPID\tURL")

	for _, e := range entries {
		portStr := "—"
		pidStr := "—"
		urlStr := "—"
		if e.Port > 0 {
			portStr = fmt.Sprintf("%d", e.Port)
		}
		if e.PID > 0 {
			pidStr = fmt.Sprintf("%d", e.PID)
		}
		// Prefer the proxy URL (what the user hits); fall back to the direct
		// localhost URL when the proxy isn't running.
		if e.URL != "" {
			urlStr = e.URL
		} else if e.DirectURL != "" {
			urlStr = e.DirectURL
		}
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", e.Worktree, e.Service, portStr, e.Status, pidStr, urlStr)
	}

	return w.Flush()
}

func init() {
	lsCmd.Flags().Bool("json", false, "Output in JSON format")
	rootCmd.AddCommand(lsCmd)
}
