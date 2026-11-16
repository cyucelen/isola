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

// stopPID stops a recorded process. Tests replace it to simulate a process
// that survives being stopped.
var stopPID = StopPID

// CleanUpWorktree removes what isola keeps for a worktree under the state
// directory: its service lock files, its service logs unless keepLogs is set,
// and its service records and port assignments in state (releasing its stable
// ports). It is the file-and-state half of `isola destroy`; ReconcileOrphans
// does the same for removed worktrees.
//
// Any recorded service still alive is stopped first, including one no longer
// in config, which the caller's stop could not see. A service that cannot be
// stopped, or whose lock file cannot be removed, keeps its record, port, lock
// and log so a rerun can finish the job; so does every service in stopFailed
// (those the caller already failed to stop). Each removed service record is
// replaced by a stopped tombstone carrying a bumped StopVersion, so a start
// that began before the cleanup (an `isola up` racing the destroy) sees the
// stop and cancels instead of starting in a destroyed worktree.
//
// Accessory records are left alone: one may only be forgotten once its resource
// is dropped, and the caller does that. activeBranches is every branch that
// still has a worktree, branch included; it guards logs against Host label
// collisions.
func CleanUpWorktree(cfg *config.Config, store *state.FileStore, branch string, activeBranches map[string]bool, keepLogs bool, stopFailed []string) error {
	kept := map[string]bool{}
	for _, svc := range stopFailed {
		kept[svc] = true
	}

	// Snapshot the service names and the recorded services still alive.
	var services []string
	live := map[string]int{}
	if err := store.WithLock(func() error {
		st, err := store.Load()
		if err != nil {
			return err
		}
		services = worktreeServiceNames(st, cfg, branch)
		for name, ss := range st.Services[branch] {
			if !kept[name] && liveService(ss) {
				live[name] = ss.PID
			}
		}
		return nil
	}); err != nil {
		return fmt.Errorf("reading state for %s: %w", branch, err)
	}

	var errs []error
	for _, name := range sortedKeys(live) {
		pid := live[name]
		_ = stopPID(pid)
		if IsProcessRunning(pid) {
			kept[name] = true
			errs = append(errs, fmt.Errorf("%s/%s (pid %d) is still running", branch, name, pid))
		}
	}

	// Files first, state last: a record is only pruned once its files are gone,
	// so a rerun still finds every service to clean up.
	if failed, err := removeServiceLocks(store, branch, services, kept); err != nil {
		errs = append(errs, err)
		for svc := range failed {
			kept[svc] = true
		}
	}
	if !keepLogs {
		label := git.HostLabel(branch)
		if other := labelSharer(branch, label, activeBranches); other != "" {
			logging.Warn("keeping logs for %s: Host label %q is also used by worktree %s", branch, label, other)
		} else {
			errs = append(errs, removeLabelLogs(filepath.Join(store.Dir(), "logs"), label, kept))
		}
	}

	if err := store.WithLock(func() error {
		st, err := store.Load()
		if err != nil {
			return err
		}
		for _, name := range services {
			if kept[name] {
				continue
			}
			if ss := state.GetServiceState(st, branch, name); ss != nil {
				if liveService(ss) {
					// Started again since the snapshot: not ours to prune.
					errs = append(errs, fmt.Errorf("%s/%s was started during the cleanup", branch, name))
					continue
				}
				st.Services[branch][name] = &state.ServiceState{Status: state.StatusStopped, StopVersion: ss.StopVersion + 1}
			}
			delete(st.PortAssignments, state.PortKey(branch, name))
		}
		return store.Save(st)
	}); err != nil {
		errs = append(errs, fmt.Errorf("pruning state for %s: %w", branch, err))
	}
	if len(kept) > 0 {
		errs = append(errs, fmt.Errorf("kept the record, lock and log of %s so a rerun can finish: %s", branch, strings.Join(sortedKeys(kept), ", ")))
	}
	return errors.Join(errs...)
}

// removeServiceLocks deletes the lock file of each of branch's services not in
// skip, holding each lock while it does. It returns the services whose lock
// file could not be removed.
func removeServiceLocks(store *state.FileStore, branch string, services []string, skip map[string]bool) (map[string]bool, error) {
	failed := map[string]bool{}
	var errs []error
	for _, svc := range services {
		if skip[svc] {
			continue
		}
		if err := store.RemoveServiceLock(branch, svc); err != nil {
			failed[svc] = true
			errs = append(errs, fmt.Errorf("removing lock file of %s/%s: %w", branch, svc, err))
		}
	}
	return failed, errors.Join(errs...)
}

// parseLogName splits a service log file name "<label>.<service>.log" into its
// Host label and service. Host labels contain no dots, so the first dot ends
// the label. A name without a service part, such as "proxy.log", is not a
// service log.
func parseLogName(name string) (label, service string, ok bool) {
	label, rest, found := strings.Cut(name, ".")
	if !found || label == "" || !strings.HasSuffix(rest, ".log") {
		return "", "", false
	}
	service = strings.TrimSuffix(rest, ".log")
	if service == "" {
		return "", "", false
	}
	return label, service, true
}

// removeLabelLogs deletes every service log of label in logDir except those of
// the services in keep. Matching on the label rather than on the configured
// services also catches services since removed from config.
func removeLabelLogs(logDir, label string, keep map[string]bool) error {
	entries, err := os.ReadDir(logDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("reading log directory: %w", err)
	}
	var errs []error
	for _, e := range entries {
		l, svc, ok := parseLogName(e.Name())
		if e.IsDir() || !ok || l != label || keep[svc] {
			continue
		}
		errs = append(errs, removeIfExists(filepath.Join(logDir, e.Name())))
	}
	return errors.Join(errs...)
}

// sweepStrayLogs deletes every service log in logDir whose Host label belongs
// to no branch that still has a worktree or a service record. It collects logs
// no per-branch cleanup reaches: logs kept by `destroy --keep-logs` or because
// a live worktree shared the label, once that worktree is gone too, and logs
// left from before isola deleted them.
//
// The directory is listed before state is read: a starting service records
// itself in state before it creates its log, so a log listed here belongs to a
// branch the state read below already knows about.
func sweepStrayLogs(store *state.FileStore, activeBranches map[string]bool) error {
	logDir := filepath.Join(store.Dir(), "logs")
	entries, err := os.ReadDir(logDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("reading log directory: %w", err)
	}
	st, err := store.Snapshot()
	if err != nil {
		return err
	}
	keep := map[string]bool{}
	for b := range activeBranches {
		keep[git.HostLabel(b)] = true
	}
	for b := range st.Services {
		keep[git.HostLabel(b)] = true
	}
	var errs []error
	for _, e := range entries {
		label, _, ok := parseLogName(e.Name())
		if e.IsDir() || !ok || keep[label] {
			continue
		}
		errs = append(errs, removeIfExists(filepath.Join(logDir, e.Name())))
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
	return sortedKeys(seen)
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
