package proxy

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/cyucelen/isola/internal/state"
)

// startTimeout bounds how long EnsureRunning waits for a freshly spawned proxy
// to bind its ports and record itself as running.
const startTimeout = 3 * time.Second

// IsRunning reports whether a proxy recorded in state is alive.
func IsRunning(store *state.FileStore) (bool, error) {
	running := false
	err := store.WithLock(func() error {
		st, e := store.Load()
		if e != nil {
			return e
		}
		running = st.Proxy.Status == state.StatusRunning && st.Proxy.PID > 0 && isAlive(st.Proxy.PID)
		return nil
	})
	return running, err
}

// EnsureRunning starts the reverse proxy as a detached background process if one
// is not already running, and returns true if it started a new one. The child
// is `isola proxy start` launched from workDir (so it detects the same repo),
// its output going to proxy.log under logDir; it survives this process exiting,
// exactly like a service. https selects the scheme.
func EnsureRunning(store *state.FileStore, workDir, logDir string, https bool) (bool, error) {
	if running, err := IsRunning(store); err != nil || running {
		return false, err
	}

	exe, err := os.Executable()
	if err != nil {
		return false, fmt.Errorf("locating isola binary: %w", err)
	}
	if err := os.MkdirAll(logDir, 0700); err != nil {
		return false, fmt.Errorf("creating log dir: %w", err)
	}
	logFile, err := os.OpenFile(filepath.Join(logDir, "proxy.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return false, fmt.Errorf("opening proxy log: %w", err)
	}
	defer func() { _ = logFile.Close() }()

	args := []string{"proxy", "start"}
	if https {
		args = append(args, "--https")
	}
	cmd := exec.Command(exe, args...)
	cmd.Dir = workDir
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	// New process group so the proxy survives this CLI exiting and is not hit by
	// a terminal signal sent to the parent's group.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return false, fmt.Errorf("starting proxy: %w", err)
	}

	// Wait for the child to bind its ports and record itself running.
	deadline := time.Now().Add(startTimeout)
	for time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
		if running, _ := IsRunning(store); running {
			return true, nil
		}
	}
	return false, fmt.Errorf("proxy did not start within %s; see %s", startTimeout, filepath.Join(logDir, "proxy.log"))
}

// Stop signals a running proxy to shut down and marks it stopped in state. It
// returns true if a running proxy was found.
func Stop(store *state.FileStore) (bool, error) {
	stopped := false
	err := store.WithLock(func() error {
		st, e := store.Load()
		if e != nil {
			return e
		}
		if st.Proxy.PID > 0 && st.Proxy.Status == state.StatusRunning && isAlive(st.Proxy.PID) {
			if p, err := os.FindProcess(st.Proxy.PID); err == nil {
				_ = p.Signal(syscall.SIGTERM)
			}
			stopped = true
		}
		st.Proxy = state.ProxyState{Status: state.StatusStopped}
		return store.Save(st)
	})
	return stopped, err
}

func isAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return p.Signal(syscall.Signal(0)) == nil
}
