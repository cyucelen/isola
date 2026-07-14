package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/cyucelen/isola/internal/config"
	"github.com/cyucelen/isola/internal/git"
	"github.com/cyucelen/isola/internal/process"
	"github.com/cyucelen/isola/internal/proxy"
	"github.com/cyucelen/isola/internal/registry"
	"github.com/cyucelen/isola/internal/state"
	"github.com/spf13/cobra"
)

type checkResult struct {
	name   string
	ok     bool
	detail string
}

var doctorCmd = &cobra.Command{
	Use:         "doctor",
	Short:       "Check environment and diagnose common issues",
	Long:        "Runs a series of checks to verify that isola's dependencies and configuration are healthy.",
	Annotations: map[string]string{"skipRepoDetection": "true"},
	RunE: func(cmd *cobra.Command, args []string) error {
		var results []checkResult

		results = append(results, checkGit())

		cwd, err := os.Getwd()
		if err != nil {
			results = append(results, checkResult{
				name: "inside git repository", ok: false, detail: err.Error(),
			})
			printResults(results)
			return nil
		}

		results = append(results, checkRepo(cwd))

		// Config checks use the current worktree root; state checks use the
		// main worktree root, where the shared .isola state lives.
		root, rootErr := git.FindRepoRoot(cwd)
		if rootErr == nil {
			results = append(results, checkConfig(root))

			cfgObj, cfgErr := config.Load(root)
			if cfgErr == nil {
				results = append(results, checkPortConflicts(cfgObj)...)

				stateRoot, stateErr := git.MainWorktreeRoot(cwd)
				if stateErr != nil {
					stateRoot = root
				}
				results = append(results, checkStaleState(stateRoot))
				results = append(results, checkStaleWorktrees(stateRoot, cwd))
			}
		}

		printResults(results)
		return nil
	},
}

func printResults(results []checkResult) {
	allOK := true
	for _, r := range results {
		mark := "✓"
		if !r.ok {
			mark = "✗"
			allOK = false
		}
		fmt.Printf("  %s  %s\n", mark, r.name)
		if r.detail != "" {
			fmt.Printf("     %s\n", r.detail)
		}
	}

	if allOK {
		fmt.Println("\nAll checks passed.")
	} else {
		fmt.Println("\nSome checks failed. See details above.")
	}
}

func checkGit() checkResult {
	path, err := exec.LookPath("git")
	if err != nil {
		return checkResult{name: "git installed", ok: false, detail: "git not found in PATH"}
	}
	out, err := exec.Command("git", "--version").Output()
	if err != nil {
		return checkResult{name: "git installed", ok: false, detail: "git found but failed to run"}
	}
	return checkResult{
		name:   "git installed",
		ok:     true,
		detail: fmt.Sprintf("%s (%s)", strings.TrimSuffix(string(out), "\n"), path),
	}
}

func checkRepo(cwd string) checkResult {
	root, err := git.FindRepoRoot(cwd)
	if err != nil {
		return checkResult{name: "inside git repository", ok: false, detail: "not inside a git repository"}
	}
	return checkResult{name: "inside git repository", ok: true, detail: root}
}

func checkConfig(root string) checkResult {
	cfgPath := filepath.Join(root, config.FileName)
	if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
		return checkResult{
			name:   "config file",
			ok:     false,
			detail: fmt.Sprintf("%s not found (run 'isola init' to create)", config.FileName),
		}
	}

	cfg, err := config.Load(root)
	if err != nil {
		return checkResult{name: "config file", ok: false, detail: err.Error()}
	}

	return checkResult{
		name:   "config file",
		ok:     true,
		detail: fmt.Sprintf("%d service(s) defined", len(cfg.Services)),
	}
}

func checkPortConflicts(cfg *config.Config) []checkResult {
	// Sort for deterministic output order.
	names := make([]string, 0, len(cfg.Services))
	for name := range cfg.Services {
		names = append(names, name)
	}
	sort.Strings(names)

	// Ports isola's own running proxy is expected to hold, so we don't flag them
	// as conflicts (the whole point is that the proxy binds these).
	ownProxyPorts := map[int]bool{}
	if reg, err := registry.Open(); err == nil {
		if running, _ := proxy.DaemonRunning(reg); running {
			if ports, err := reg.Ports(); err == nil {
				for _, p := range ports {
					ownProxyPorts[p] = true
				}
			}
		}
	}

	var results []checkResult
	for _, name := range names {
		port := cfg.Services[name].ProxyPort
		label := fmt.Sprintf("proxy port %d (%s) available", port, name)
		switch {
		case process.IsPortAvailable(port):
			results = append(results, checkResult{name: label, ok: true})
		case ownProxyPorts[port]:
			results = append(results, checkResult{name: label, ok: true, detail: "served by isola's proxy"})
		default:
			results = append(results, checkResult{
				name: label, ok: false,
				detail: fmt.Sprintf("port %d already in use by another process", port),
			})
		}
	}
	return results
}

func checkStaleState(root string) checkResult {
	stateDir := filepath.Join(root, ".isola")
	store, err := state.NewFileStore(stateDir)
	if err != nil {
		return checkResult{name: "state file healthy", ok: false, detail: err.Error()}
	}

	st, err := store.LoadLocked()
	if err != nil {
		return checkResult{name: "state file healthy", ok: false, detail: err.Error()}
	}

	var staleDetails []string
	for branch, services := range st.Services {
		for svcName, ss := range services {
			if ss.IsRunning() && !process.IsProcessRunning(ss.PID) {
				staleDetails = append(staleDetails, fmt.Sprintf("%s/%s (PID %d)", branch, svcName, ss.PID))
			}
		}
	}

	if len(staleDetails) > 0 {
		return checkResult{
			name:   "state file healthy",
			ok:     false,
			detail: fmt.Sprintf("%d stale: %v", len(staleDetails), staleDetails),
		}
	}

	return checkResult{name: "state file healthy", ok: true}
}

func checkStaleWorktrees(root, cwd string) checkResult {
	stateDir := filepath.Join(root, ".isola")
	store, err := state.NewFileStore(stateDir)
	if err != nil {
		return checkResult{name: "worktree state consistent", ok: false, detail: err.Error()}
	}

	st, err := store.LoadLocked()
	if err != nil {
		return checkResult{name: "worktree state consistent", ok: false, detail: err.Error()}
	}

	trees, err := git.ListWorktrees(cwd)
	if err != nil {
		return checkResult{name: "worktree state consistent", ok: false, detail: err.Error()}
	}

	// Find branches in state that have no worktree on disk.
	orphaned := state.OrphanedBranches(st, git.ActiveBranches(trees))
	sort.Strings(orphaned)

	if len(orphaned) > 0 {
		return checkResult{
			name:   "worktree state consistent",
			ok:     false,
			detail: fmt.Sprintf("%d orphaned: %v (run 'isola down --prune' to clean)", len(orphaned), orphaned),
		}
	}

	return checkResult{name: "worktree state consistent", ok: true}
}

func init() {
	rootCmd.AddCommand(doctorCmd)
}
