package proxy

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/cyucelen/isola/internal/registry"
)

// startTimeout bounds how long EnsureDaemon waits for a freshly spawned daemon
// to record itself running.
const startTimeout = 5 * time.Second

// DaemonRunning reports whether the machine-wide proxy daemon is alive.
func DaemonRunning(reg *registry.Store) (bool, error) {
	d, err := reg.GetDaemon()
	if err != nil {
		return false, err
	}
	return d.Running && d.PID > 0 && isAlive(d.PID), nil
}

// EnsureDaemon starts the machine-wide proxy daemon as a detached process if one
// is not already running, and returns true if it started a new one. The daemon
// is `isola proxy start`; its output goes to proxy.log under the registry's
// global dir, and it survives this process exiting.
func EnsureDaemon(reg *registry.Store) (bool, error) {
	if running, err := DaemonRunning(reg); err != nil || running {
		return false, err
	}

	exe, err := os.Executable()
	if err != nil {
		return false, fmt.Errorf("locating isola binary: %w", err)
	}
	logDir := filepath.Join(reg.Dir(), "logs")
	if err := os.MkdirAll(logDir, 0700); err != nil {
		return false, fmt.Errorf("creating log dir: %w", err)
	}
	logFile, err := os.OpenFile(filepath.Join(logDir, "proxy.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return false, fmt.Errorf("opening proxy log: %w", err)
	}
	defer func() { _ = logFile.Close() }()

	cmd := exec.Command(exe, "proxy", "start")
	cmd.Dir = reg.Dir()
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return false, fmt.Errorf("starting proxy daemon: %w", err)
	}

	deadline := time.Now().Add(startTimeout)
	for time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
		if running, _ := DaemonRunning(reg); running {
			return true, nil
		}
	}
	return false, fmt.Errorf("proxy daemon did not start within %s; see %s", startTimeout, filepath.Join(logDir, "proxy.log"))
}

// StopDaemon signals the running daemon to shut down and clears its recorded
// state. It returns true if a running daemon was found.
func StopDaemon(reg *registry.Store) (bool, error) {
	d, err := reg.GetDaemon()
	if err != nil {
		return false, err
	}
	stopped := false
	if d.PID > 0 && d.Running && isAlive(d.PID) {
		if p, err := os.FindProcess(d.PID); err == nil {
			_ = p.Signal(syscall.SIGTERM)
		}
		stopped = true
	}
	return stopped, reg.SetDaemon(registry.Daemon{})
}

func isAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return p.Signal(syscall.Signal(0)) == nil
}
