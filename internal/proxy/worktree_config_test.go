package proxy

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/cyucelen/isola/internal/config"
	"github.com/cyucelen/isola/internal/registry"
	"github.com/cyucelen/isola/internal/state"
)

// gitRun runs git in dir with a fixed identity, failing the test on error.
func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=isola", "-c", "user.email=isola@example.com", "-c", "commit.gpgsign=false"}, args...)...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// newRepoWithWorktree creates a repo whose main checkout is on "main" and a
// linked worktree on "feature". It returns both paths.
func newRepoWithWorktree(t *testing.T) (mainDir, treeDir string) {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	mainDir, treeDir = filepath.Join(base, "repo"), filepath.Join(base, "feature")
	if err := os.Mkdir(mainDir, 0o755); err != nil {
		t.Fatal(err)
	}
	gitRun(t, mainDir, "init", "-q", "-b", "main")
	gitRun(t, mainDir, "commit", "-q", "--allow-empty", "-m", "init")
	gitRun(t, mainDir, "worktree", "add", "-q", "-b", "feature", treeDir)
	return mainDir, treeDir
}

// directConfig is a config whose on-demand api service has the given bind and
// extra service TOML appended.
func directConfig(bind, extra string) string {
	return fmt.Sprintf(`project = "app"

[services.api]
command = "true"
on_demand = true
port_range = { min = 40000, max = 40099 }
bind = %q
%s`, bind, extra)
}

// configWriter returns a function that writes a config into dir, moving its
// mtime forward each time so the daemon sees a new stamp.
func configWriter(t *testing.T, dir string) func(toml string) {
	t.Helper()
	path := filepath.Join(dir, config.FileName)
	mtime := time.Now()
	return func(toml string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(toml), 0o600); err != nil {
			t.Fatal(err)
		}
		mtime = mtime.Add(time.Minute)
		if err := os.Chtimes(path, mtime, mtime); err != nil {
			t.Fatal(err)
		}
	}
}

// addWorktreeProject registers project "app" from a real repo. The main
// checkout's config sets mainBind on api, the "feature" worktree's sets
// treeBind, and api is running behind a daemon direct port on "feature". It
// returns the public port and a writer for the worktree's config.
func addWorktreeProject(t *testing.T, reg *registry.Store, mainBind, treeBind string) (int, func(toml string)) {
	t.Helper()
	mainDir, treeDir := newRepoWithWorktree(t)
	configWriter(t, mainDir)(directConfig(mainBind, ""))
	writeTree := configWriter(t, treeDir)
	writeTree(directConfig(treeBind, ""))
	stateDir := filepath.Join(mainDir, ".isola")
	store, err := state.NewFileStore(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	public := freePort(t)
	saveRunningDirectOn(t, store, "feature", "api", public, helloBackend(t))
	if err := reg.Register(registry.Project{Name: "app", StateDir: stateDir}); err != nil {
		t.Fatal(err)
	}
	return public, writeTree
}

// A branch that adds bind = "all" exposes its own direct port before it
// merges: the daemon reads bind from the worktree that owns the service, not
// from the main checkout.
func TestDaemonBindsFromOwningWorktreeConfig(t *testing.T) {
	lan := lanIPv4(t)
	d, reg := newTestDaemon(t)
	public, _ := addWorktreeProject(t, reg, config.BindLoopback, config.BindAll)

	syncOrFail(t, d)

	expectGreeting(t, net.JoinHostPort(lan.String(), strconv.Itoa(public)))
	expectGreeting(t, net.JoinHostPort("127.0.0.1", strconv.Itoa(public)))
}

// Editing bind in a worktree's own config rebinds its direct port on the next
// sync, with the main checkout's config unchanged and no daemon restart. A
// broken edit keeps the listener as it was.
func TestDaemonRebindsWhenWorktreeConfigChanges(t *testing.T) {
	lan := lanIPv4(t)
	d, reg := newTestDaemon(t)
	public, writeTree := addWorktreeProject(t, reg, config.BindLoopback, config.BindLoopback)
	lanAddr := net.JoinHostPort(lan.String(), strconv.Itoa(public))

	syncOrFail(t, d)
	expectRefused(t, lanAddr, "worktree bind = loopback")

	writeTree(directConfig(config.BindAll, ""))
	syncOrFail(t, d)
	expectGreeting(t, lanAddr)

	writeTree("this is [not toml")
	syncOrFail(t, d)
	expectGreeting(t, lanAddr) // the last good config still applies

	writeTree(directConfig(config.BindLoopback, ""))
	syncOrFail(t, d)
	expectRefused(t, lanAddr, "worktree back to bind = loopback")
	expectGreeting(t, net.JoinHostPort("127.0.0.1", strconv.Itoa(public)))
}

// When the owning worktree goes away (until reconcile stops its services), its
// direct port keeps the bind it had rather than flapping to the main
// checkout's setting.
func TestDaemonKeepsBindWhenWorktreeRemoved(t *testing.T) {
	lan := lanIPv4(t)
	d, reg := newTestDaemon(t)
	public, _ := addWorktreeProject(t, reg, config.BindLoopback, config.BindAll)
	lanAddr := net.JoinHostPort(lan.String(), strconv.Itoa(public))
	syncOrFail(t, d)
	expectGreeting(t, lanAddr)

	p, _, err := reg.Lookup("app")
	if err != nil {
		t.Fatal(err)
	}
	mainDir := filepath.Dir(p.StateDir)
	gitRun(t, mainDir, "worktree", "remove", "--force", filepath.Join(filepath.Dir(mainDir), "feature"))
	syncOrFail(t, d) // within the listing TTL: the last good worktree config
	expectGreeting(t, lanAddr)

	// Past the TTL the branch has no worktree and no config at all.
	res, err := d.resolverFor("app")
	if err != nil {
		t.Fatal(err)
	}
	res.worktrees.mu.Lock()
	res.worktrees.listedAt = time.Time{}
	res.worktrees.mu.Unlock()
	syncOrFail(t, d)
	if _, owned := res.bindsAll("feature", "api"); owned {
		t.Fatal("bind still read from the removed worktree")
	}
	expectGreeting(t, lanAddr)
}

// A proxy_port that only a branch's config declares routes to that branch's
// service: the proxy maps the port to a service with the worktree's config.
func TestResolverRoutesProxyPortFromWorktreeConfig(t *testing.T) {
	mainDir, treeDir := newRepoWithWorktree(t)
	configWriter(t, mainDir)(directConfig(config.BindLoopback, ""))
	configWriter(t, treeDir)(directConfig(config.BindLoopback, `
[services.admin]
command = "true"
port_range = { min = 41000, max = 41099 }
proxy_port = 3999
`))
	mainCfg, err := config.Load(mainDir)
	if err != nil {
		t.Fatal(err)
	}
	store, err := state.NewFileStore(filepath.Join(mainDir, ".isola"))
	if err != nil {
		t.Fatal(err)
	}
	st := &state.State{Services: map[string]map[string]*state.ServiceState{}, PortAssignments: map[string]int{}}
	state.SetPortAssignment(st, "feature", "admin", 41007)
	if err := store.Save(st); err != nil {
		t.Fatal(err)
	}

	got, err := NewResolver(mainCfg, store).Resolve("feature", 3999)
	if err != nil || got != 41007 {
		t.Fatalf("Resolve(feature, 3999) = %d, %v; want 41007", got, err)
	}
}
