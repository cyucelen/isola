package process

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/cyucelen/isola/internal/config"
	"github.com/cyucelen/isola/internal/git"
	"github.com/cyucelen/isola/internal/logging"
	"github.com/cyucelen/isola/internal/state"
)

// CleanUpWorktree removes what isola keeps for a worktree under the state
// directory once its services are stopped: its service records and port
// assignments in state (releasing its stable ports), its service lock files, and
// its service logs unless keepLogs is set. It is the file-and-state half of
// `isola destroy`; ReconcileOrphans does the same for removed worktrees.
//
// Accessory records are left alone: one may only be forgotten once its resource
// is dropped, and the caller does that (a record that failed to drop stays so a
// later drop or reconcile retries it). activeBranches is every branch that still
// has a worktree, branch included; it guards logs against Host label collisions.
func CleanUpWorktree(cfg *config.Config, store *state.FileStore, branch string, activeBranches map[string]bool, keepLogs bool) error {
	var services []string
	if err := store.WithLock(func() error {
		st, err := store.Load()
		if err != nil {
			return err
		}
		services = worktreeServiceNames(st, cfg, branch)
		forgetWorktreeServices(st, branch)
		return store.Save(st)
	}); err != nil {
		return fmt.Errorf("pruning state for %s: %w", branch, err)
	}
	return RemoveWorktreeFiles(store, branch, services, activeBranches, keepLogs)
}

// RemoveWorktreeFiles deletes the files isola keeps under the state directory
// for one worktree: the service lock file of each named service and, unless
// keepLogs is set, every service log of the worktree's Host label. Call it only
// after the worktree's services are stopped, since a lock file in use must not
// disappear under its holder. Files already gone are not an error.
//
// Logs are named by Host label, which two branches can share. When a branch in
// activeBranches other than branch maps to the same label, the logs belong to
// that live worktree too and are kept.
func RemoveWorktreeFiles(store *state.FileStore, branch string, services []string, activeBranches map[string]bool, keepLogs bool) error {
	var errs []error
	for _, svc := range services {
		errs = append(errs, removeIfExists(store.ServiceLockPath(branch, svc)))
	}
	if !keepLogs {
		label := git.HostLabel(branch)
		if other := labelSharer(branch, label, activeBranches); other != "" {
			logging.Warn("keeping logs for %s: Host label %q is also used by worktree %s", branch, label, other)
		} else {
			errs = append(errs, removeLabelLogs(filepath.Join(store.Dir(), "logs"), label))
		}
	}
	return errors.Join(errs...)
}

// removeLabelLogs deletes every "<label>.<service>.log" in logDir. Host labels
// contain no dots, so the "<label>." prefix matches only this label's logs, and
// it also catches services since removed from config.
func removeLabelLogs(logDir, label string) error {
	entries, err := os.ReadDir(logDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("reading log directory: %w", err)
	}
	var errs []error
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasPrefix(name, label+".") || !strings.HasSuffix(name, ".log") {
			continue
		}
		errs = append(errs, removeIfExists(filepath.Join(logDir, name)))
	}
	return errors.Join(errs...)
}

// labelSharer returns an active branch other than branch whose Host label is
// label, or "" if there is none.
func labelSharer(branch, label string, activeBranches map[string]bool) string {
	others := make([]string, 0, len(activeBranches))
	for b := range activeBranches {
		if b != branch {
			others = append(others, b)
		}
	}
	sort.Strings(others)
	for _, b := range others {
		if git.HostLabel(b) == label {
			return b
		}
	}
	return ""
}

func removeIfExists(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// worktreeServiceNames returns every service isola may hold files for under
// branch: the configured services plus any recorded in state (service records
// and port assignments), which covers services since removed from config.
func worktreeServiceNames(st *state.State, cfg *config.Config, branch string) []string {
	seen := map[string]bool{}
	for name := range cfg.Services {
		seen[name] = true
	}
	for name := range st.Services[branch] {
		seen[name] = true
	}
	for key := range st.PortAssignments {
		if b, svc := state.ParsePortKey(key); b == branch && svc != "" {
			seen[svc] = true
		}
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// forgetWorktreeServices removes branch's service records and port assignments
// from st, releasing its stable ports. Accessory records are not touched.
func forgetWorktreeServices(st *state.State, branch string) {
	delete(st.Services, branch)
	for key := range st.PortAssignments {
		if b, _ := state.ParsePortKey(key); b == branch {
			delete(st.PortAssignments, key)
		}
	}
}
