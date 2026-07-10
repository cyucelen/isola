package process

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/cyucelen/isola/internal/browser"
	"github.com/cyucelen/isola/internal/expand"
	"github.com/cyucelen/isola/internal/logging"
)

const stopTimeout = 10 * time.Second

// RunnerConfig contains all parameters needed to start a process.
type RunnerConfig struct {
	ServiceName string
	Branch      string
	BranchSlug  string
	Project     string // project name, for project-qualified sibling URLs
	Command     string
	Dir         string // absolute working directory
	Port        int
	Env         map[string]string // merged environment variables (subject to ${VAR} expansion)
	// AccessoriesByName maps accessory name -> its URL, for ${accessories.<name>.url}.
	AccessoriesByName map[string]string
	LogDir            string // directory for log files
	// AllServicePorts maps service name -> assigned port for cross-service env vars.
	AllServicePorts map[string]int
	// AllServiceProxyPorts maps service name -> proxy port for URL env vars.
	AllServiceProxyPorts map[string]int
	// ProxyScheme is "http" or "https" for ISOLA_*_URL env vars.
	ProxyScheme string
	// CACertPath, when set (HTTPS), is the path to isola's dev CA. It is not
	// injected anywhere automatically; it is exposed for the ${proxy.ca_cert}
	// reference so a service can wire it into whichever CA env var its runtime
	// uses (e.g. NODE_EXTRA_CA_CERTS) to trust sibling HTTPS without `isola trust`.
	CACertPath string
}

// Runner manages a single child process.
type Runner struct {
	config  RunnerConfig
	cmd     *exec.Cmd
	logFile *os.File
	done    chan struct{} // closed when the process exits
}

// NewRunner creates a new Runner.
func NewRunner(cfg RunnerConfig) *Runner {
	return &Runner{config: cfg}
}

// Start launches the process.
// Child processes are intentionally detached and survive CLI exit so that
// development servers keep running after the isola command returns.
// Use `isola down` to stop them.
func (r *Runner) Start() (int, error) {
	if r.cmd != nil && r.cmd.Process != nil {
		if r.IsRunning() {
			return 0, fmt.Errorf("service %s is already running (pid %d)", r.config.ServiceName, r.cmd.Process.Pid)
		}
	}

	// Ensure log directory exists.
	if err := os.MkdirAll(r.config.LogDir, 0700); err != nil {
		return 0, fmt.Errorf("creating log dir: %w", err)
	}

	logPath := filepath.Join(r.config.LogDir, LogFileName(r.config.BranchSlug, r.config.ServiceName))
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return 0, fmt.Errorf("opening log file: %w", err)
	}
	r.logFile = f

	r.cmd = exec.Command("sh", "-c", r.config.Command)
	r.cmd.Dir = r.config.Dir
	r.cmd.Stdout = f
	r.cmd.Stderr = f
	r.cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	r.cmd.Env = r.buildEnv()

	// Initialize the done channel before Start so that Stop can never
	// encounter a nil channel, even if a panic occurs between Start and
	// the goroutine launch.
	r.done = make(chan struct{})

	if err := r.cmd.Start(); err != nil {
		close(r.done)
		_ = f.Close()
		return 0, fmt.Errorf("starting %s: %w", r.config.ServiceName, err)
	}

	// Track process exit via a single Wait call to avoid the race of calling
	// Wait() twice on the same exec.Cmd.
	go func() {
		_ = r.cmd.Wait()
		close(r.done)
	}()

	return r.cmd.Process.Pid, nil
}

// Stop sends SIGTERM then SIGKILL to the process group.
func (r *Runner) Stop() error {
	if r.logFile != nil {
		defer func() { _ = r.logFile.Close() }()
	}

	if r.cmd == nil || r.cmd.Process == nil {
		return nil
	}

	pid := r.cmd.Process.Pid
	pgid, err := syscall.Getpgid(pid)
	if err != nil {
		// Process may already be dead.
		return nil
	}

	// Send SIGTERM to the process group.
	if err := syscall.Kill(-pgid, syscall.SIGTERM); err != nil {
		logging.Warn("failed to send SIGTERM to process group %d: %v", pgid, err)
	}

	// Reuse the done channel from Start instead of calling Wait again.
	select {
	case <-r.done:
		return nil
	case <-time.After(stopTimeout):
		// Force kill the process group.
		if err := syscall.Kill(-pgid, syscall.SIGKILL); err != nil {
			logging.Warn("failed to send SIGKILL to process group %d: %v", pgid, err)
		}
		return nil
	}
}

// Done returns a channel that is closed when the process exits.
// Returns nil if Start has not been called yet.
func (r *Runner) Done() <-chan struct{} {
	return r.done
}

// StopPID stops a process by PID (used for stale processes from state).
func StopPID(pid int) error {
	pgid, err := syscall.Getpgid(pid)
	if err != nil {
		return nil // already dead
	}

	if err := syscall.Kill(-pgid, syscall.SIGTERM); err != nil {
		logging.Warn("failed to send SIGTERM to process group %d: %v", pgid, err)
	}

	// Poll briefly for process exit, then force kill.
	for i := 0; i < 30; i++ {
		time.Sleep(100 * time.Millisecond)
		if !IsProcessRunning(pid) {
			return nil
		}
	}
	if err := syscall.Kill(-pgid, syscall.SIGKILL); err != nil {
		logging.Warn("failed to send SIGKILL to process group %d: %v", pgid, err)
	}
	for i := 0; i < 5; i++ {
		time.Sleep(50 * time.Millisecond)
		if !IsProcessRunning(pid) {
			return nil
		}
	}
	return nil
}

// IsRunning checks if the process is still alive.
func (r *Runner) IsRunning() bool {
	if r.cmd == nil || r.cmd.Process == nil {
		return false
	}
	return IsProcessRunning(r.cmd.Process.Pid)
}

// PID returns the process ID, or 0 if not started.
func (r *Runner) PID() int {
	if r.cmd != nil && r.cmd.Process != nil {
		return r.cmd.Process.Pid
	}
	return 0
}

// IsProcessRunning checks if a process with the given PID is alive.
func IsProcessRunning(pid int) bool {
	if pid <= 0 {
		return false
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = process.Signal(syscall.Signal(0))
	return err == nil
}

// IsPortAvailable reports whether a TCP port is free to use. It treats a port as
// taken if any server is accepting connections on it via loopback (IPv4 or
// IPv6), detected by dialing rather than binding: a bind-based check is defeated
// by SO_REUSEADDR and address family, so a backend already held on 0.0.0.0 or
// [::] (e.g. another isola project's service) would be missed.
func IsPortAvailable(port int) bool {
	for _, host := range []string{"127.0.0.1", "::1"} {
		conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, strconv.Itoa(port)), 200*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return false
		}
	}
	return true
}

// scheme returns the proxy scheme, defaulting to http.
func (r *Runner) scheme() string {
	if r.config.ProxyScheme == "" {
		return "http"
	}
	return r.config.ProxyScheme
}

// builtins returns the isola auto-injected variables for this service.
func (r *Runner) builtins() map[string]string {
	m := map[string]string{
		"PORT":              fmt.Sprintf("%d", r.config.Port),
		"ISOLA_BRANCH":      r.config.Branch,
		"ISOLA_BRANCH_SLUG": r.config.BranchSlug,
		"ISOLA_SERVICE":     r.config.ServiceName,
	}
	for svcName, svcPort := range r.config.AllServicePorts {
		m["ISOLA_"+strings.ToUpper(svcName)+"_PORT"] = fmt.Sprintf("%d", svcPort)
	}
	for svcName, proxyPort := range r.config.AllServiceProxyPorts {
		m["ISOLA_"+strings.ToUpper(svcName)+"_URL"] = browser.BuildURL(r.scheme(), r.config.BranchSlug, r.config.Project, proxyPort)
	}
	return m
}

// resolver returns the ${...} expansion function: isola built-ins, the
// accessories.<name>.url / services.<name>.url / services.<name>.port reference
// namespace, then the process environment. Only the explicit ${...} form is
// interpolated; a bare "$" is left literal so values like "p$ssw0rd" survive.
func (r *Runner) resolver(builtins map[string]string) func(string) string {
	return func(name string) string {
		if v, ok := builtins[name]; ok {
			return v
		}
		if rest, ok := strings.CutPrefix(name, "accessories."); ok {
			if key, ok := strings.CutSuffix(rest, ".url"); ok {
				return r.config.AccessoriesByName[key]
			}
		}
		if rest, ok := strings.CutPrefix(name, "services."); ok {
			if svc, ok := strings.CutSuffix(rest, ".url"); ok {
				if p, ok := r.config.AllServiceProxyPorts[svc]; ok {
					return browser.BuildURL(r.scheme(), r.config.BranchSlug, r.config.Project, p)
				}
				return ""
			}
			if svc, ok := strings.CutSuffix(rest, ".port"); ok {
				if p, ok := r.config.AllServicePorts[svc]; ok {
					return fmt.Sprintf("%d", p)
				}
				return ""
			}
		}
		// proxy.ca_cert is the path to isola's dev CA (empty unless HTTPS). A
		// service opts in by mapping it to its runtime's CA var in its own env,
		// e.g. NODE_EXTRA_CA_CERTS = "${proxy.ca_cert}"; isola never sets a
		// runtime-specific var itself.
		if name == "proxy.ca_cert" {
			return r.config.CACertPath
		}
		return os.Getenv(name)
	}
}

// resolvedConfigEnv returns the service's config env (global [env] + service +
// per-worktree overrides) with ${...} references expanded. Keys/values with a
// null byte are dropped with a warning.
func (r *Runner) resolvedConfigEnv() map[string]string {
	expandVar := r.resolver(r.builtins())
	out := make(map[string]string, len(r.config.Env))
	for k, v := range r.config.Env {
		if strings.ContainsRune(k, 0) || strings.ContainsRune(v, 0) {
			logging.Warn("skipping env var %q: contains null byte", k)
			continue
		}
		out[k] = expand.Braces(v, expandVar)
	}
	return out
}

// FileEnv returns the variables isola writes into a service's env file: its
// resolved config env (with ${...} references expanded, so accessory URLs and
// sibling URLs are already substituted). Ephemeral built-ins (PORT, ISOLA_*) are
// intentionally excluded — they change per run and would go stale in a file.
func (r *Runner) FileEnv() map[string]string {
	return r.resolvedConfigEnv()
}

// buildEnv constructs the full environment for the child process by merging
// layers in increasing precedence: parent environment, then the config env
// (expanded), then the isola built-ins. Keys are deduplicated so precedence is
// deterministic: with duplicate keys in a process environment, libc typically
// returns the first, so relying on append order would be unreliable (a later
// layer would not actually override an earlier one).
func (r *Runner) buildEnv() []string {
	merged := map[string]string{}
	for _, e := range os.Environ() {
		if parts := strings.SplitN(e, "=", 2); len(parts) == 2 {
			merged[parts[0]] = parts[1]
		}
	}
	for k, v := range r.resolvedConfigEnv() {
		merged[k] = v
	}
	for k, v := range r.builtins() {
		merged[k] = v
	}

	env := make([]string, 0, len(merged))
	for k, v := range merged {
		env = append(env, k+"="+v)
	}
	return env
}
