//go:build e2e

// Package e2e exercises the assembled isola binary end to end: real git repos,
// real spawned services, the reverse proxy (HTTP and HTTPS), cross-service URL
// injection, the worktree post-checkout hook, orphan reconcile, and — against a
// throwaway Postgres container — the full accessory provision/teardown cycle.
// These are the flows unit tests can't reach (they need isola on PATH, a running
// proxy, git actually invoking the hook, and a real database). Run with:
//
//	make e2e     # or: go test -tags e2e ./e2e/... -count=1
//
// Tests that need a database start a container via testcontainers and skip
// themselves if Docker is unavailable. Each test uses a throwaway HOME so it
// never touches the caller's ~/.isola.
package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

// bin is the isola binary; testServer is a tiny HTTP server. Both are built once
// for the whole suite.
var (
	bin        string
	testServer string
)

// serverSrc is a minimal HTTP service used as a "real" backend. It answers "/"
// with $MARKER (default "ok"), and "/call" by fetching $TARGET_URL and echoing
// "via:<body>" — proving a service can reach a sibling through the proxy. Its
// HTTP client forces *.localhost to 127.0.0.1 so sibling calls don't depend on
// wildcard-localhost DNS (which many Linux hosts lack).
const serverSrc = `package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		os.Exit(1)
	}
	marker := os.Getenv("MARKER")
	if marker == "" {
		marker = "ok"
	}
	client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			if h, p, err := net.SplitHostPort(addr); err == nil && strings.HasSuffix(h, ".localhost") {
				addr = net.JoinHostPort("127.0.0.1", p)
			}
			return (&net.Dialer{}).DialContext(ctx, network, addr)
		},
	}}
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(marker))
	})
	http.HandleFunc("/call", func(w http.ResponseWriter, r *http.Request) {
		resp, err := client.Get(os.Getenv("TARGET_URL"))
		if err != nil {
			http.Error(w, "call failed: "+err.Error(), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		_, _ = w.Write([]byte("via:" + string(b)))
	})
	_ = http.ListenAndServe(":"+port, nil)
}
`

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "isola-e2e")
	if err != nil {
		panic(err)
	}
	bin = filepath.Join(dir, "isola")
	if out, err := exec.Command("go", "build", "-o", bin, "github.com/cyucelen/isola").CombinedOutput(); err != nil {
		panic("building isola: " + err.Error() + "\n" + string(out))
	}

	// Build the throwaway HTTP test server in its own tiny module.
	srvDir := filepath.Join(dir, "testserver")
	if err := os.MkdirAll(srvDir, 0o755); err != nil {
		panic(err)
	}
	_ = os.WriteFile(filepath.Join(srvDir, "go.mod"), []byte("module testserver\n\ngo 1.23\n"), 0o644)
	_ = os.WriteFile(filepath.Join(srvDir, "main.go"), []byte(serverSrc), 0o644)
	testServer = filepath.Join(dir, "testserver-bin")
	build := exec.Command("go", "build", "-o", testServer, ".")
	build.Dir = srvDir
	if out, err := build.CombinedOutput(); err != nil {
		panic("building testserver: " + err.Error() + "\n" + string(out))
	}

	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

type env struct {
	t    *testing.T
	home string
}

func newEnv(t *testing.T) *env { return &env{t: t, home: t.TempDir()} }

// cmd builds a command with an isolated HOME and the isola binary on PATH (so
// the git hook's `command -v isola` resolves).
func (e *env) cmd(dir, name string, args ...string) *exec.Cmd {
	c := exec.Command(name, args...)
	c.Dir = dir
	c.Env = append(os.Environ(),
		"HOME="+e.home,
		"PATH="+filepath.Dir(bin)+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	return c
}

func (e *env) isola(dir string, args ...string) string {
	out, err := e.cmd(dir, bin, args...).CombinedOutput()
	if err != nil {
		e.t.Fatalf("isola %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

func (e *env) git(dir string, args ...string) {
	if out, err := e.cmd(dir, "git", args...).CombinedOutput(); err != nil {
		e.t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

type lsEntry struct {
	Worktree string `json:"worktree"`
	Service  string `json:"service"`
	Port     int    `json:"port"`
	Status   string `json:"status"`
	PID      int    `json:"pid"`
	URL      string `json:"url"`
}

func (e *env) ls(dir string) []lsEntry {
	out, err := e.cmd(dir, bin, "ls", "--json").Output() // stdout only => clean JSON
	if err != nil {
		e.t.Fatalf("ls --json: %v", err)
	}
	var entries []lsEntry
	if err := json.Unmarshal(out, &entries); err != nil {
		e.t.Fatalf("parse ls --json: %v\n%s", err, out)
	}
	return entries
}

func (e *env) running(dir, branch string) bool {
	for _, x := range e.ls(dir) {
		if strings.HasPrefix(x.Worktree, branch) && x.Status == "running" {
			return true
		}
	}
	return false
}

func (e *env) anyRunning(dir string) bool {
	for _, x := range e.ls(dir) {
		if x.Status == "running" {
			return true
		}
	}
	return false
}

// newRepo initializes a git repo whose .isola.toml is body, commits it, and
// returns its path. Cleanup stops any services and proxy the repo started.
func (e *env) newRepo(body string) string {
	dir := filepath.Join(e.t.TempDir(), "repo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		e.t.Fatal(err)
	}
	e.git(dir, "init", "-q", "-b", "main")
	e.git(dir, "config", "user.email", "e2e@example.com")
	e.git(dir, "config", "user.name", "e2e")
	if err := os.WriteFile(filepath.Join(dir, ".isola.toml"), []byte(body), 0o644); err != nil {
		e.t.Fatal(err)
	}
	e.git(dir, "add", "-A")
	e.git(dir, "commit", "-q", "-m", "init")
	e.t.Cleanup(func() {
		_, _ = e.cmd(dir, bin, "down", "--all").CombinedOutput()
		_, _ = e.cmd(dir, bin, "proxy", "stop").CombinedOutput()
	})
	return dir
}

// repoWith creates a repo with one service. proxyPort 0 disables the proxy;
// otherwise the service's backend range sits just above it.
func (e *env) repoWith(project, command string, proxyPort int) string {
	proxyEnabled := proxyPort > 0
	pport := proxyPort
	if pport == 0 {
		pport = 4800
	}
	body := fmt.Sprintf("project = %q\n\n[proxy]\nenabled = %t\n\n[services.web]\ncommand = %q\nport_range = { min = %d, max = %d }\nproxy_port = %d\n",
		project, proxyEnabled, command, pport+1, pport+9, pport)
	return e.newRepo(body)
}

// repo is a proxy-disabled repo with a long-running (sleep) service.
func (e *env) repo(project string) string { return e.repoWith(project, "sleep 120", 0) }

func (e *env) waitRunning(dir, branch string) {
	e.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if e.running(dir, branch) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	e.t.Fatalf("service for %q did not start in time", branch)
}

// req issues an HTTP(S) GET to the proxy for host:port+path, forcing the
// connection to 127.0.0.1 via --resolve (so *.localhost need not resolve) and
// trusting the dev cert with -k on HTTPS. Returns the status (0 if none) + body.
func (e *env) req(dir, scheme, host string, port int, path string) (int, string) {
	url := fmt.Sprintf("%s://%s:%d%s", scheme, host, port, path)
	base := []string{"-s", "-m", "5", "--resolve", fmt.Sprintf("%s:%d:127.0.0.1", host, port)}
	if scheme == "https" {
		base = append(base, "-k")
	}
	code, _ := e.cmd(dir, "curl", append(append([]string{}, base...), "-o", "/dev/null", "-w", "%{http_code}", url)...).Output()
	status, _ := strconv.Atoi(strings.TrimSpace(string(code)))
	body, _ := e.cmd(dir, "curl", append(append([]string{}, base...), url)...).Output()
	return status, string(body)
}

// waitReq polls the proxy until it returns wantStatus (and, if wantBody is
// non-empty, a body containing it), or times out — returning the last seen.
func (e *env) waitReq(dir, scheme, host string, port int, path string, wantStatus int, wantBody string) (int, string) {
	e.t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	var st int
	var body string
	for time.Now().Before(deadline) {
		st, body = e.req(dir, scheme, host, port, path)
		if st == wantStatus && (wantBody == "" || strings.Contains(body, wantBody)) {
			return st, body
		}
		time.Sleep(200 * time.Millisecond)
	}
	return st, body
}

// startPostgres boots a throwaway Postgres, seeds the template database isola
// clones per worktree (myapp_dev), and returns the maintenance-db URL. It skips
// the test if Docker is unavailable.
func startPostgres(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	ctr, err := tcpostgres.Run(ctx, "postgres:16",
		tcpostgres.WithDatabase("isola"),
		tcpostgres.WithUsername("isola"),
		tcpostgres.WithPassword("isola"),
		tcpostgres.BasicWaitStrategies(),
	)
	if err != nil {
		t.Skipf("Docker/Postgres unavailable, skipping accessory e2e: %v", err)
	}
	t.Cleanup(func() { _ = testcontainers.TerminateContainer(ctr) })

	serverURL, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}
	conn, err := pgx.Connect(ctx, serverURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()
	if _, err := conn.Exec(ctx, "CREATE DATABASE myapp_dev"); err != nil {
		t.Fatalf("seeding template db: %v", err)
	}
	return serverURL
}

// pgDatabaseExists reports whether a database with the given name exists.
func pgDatabaseExists(t *testing.T, serverURL, name string) bool {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, serverURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()
	var n int
	if err := conn.QueryRow(ctx, "select count(*) from pg_database where datname = $1", name).Scan(&n); err != nil {
		t.Fatalf("query pg_database: %v", err)
	}
	return n > 0
}

func TestUpLsDown(t *testing.T) {
	e := newEnv(t)
	repo := e.repo("e2eupdown")

	e.isola(repo, "up")
	if !e.anyRunning(repo) {
		t.Fatal("a service should be running after up")
	}
	e.isola(repo, "down")
	if e.anyRunning(repo) {
		t.Error("no service should be running after down")
	}
}

func TestUpFailsOnCrashingService(t *testing.T) {
	e := newEnv(t)
	repo := e.repoWith("e2ecrash", "exit 1", 0)

	out, err := e.cmd(repo, bin, "up").CombinedOutput()
	if err == nil {
		t.Errorf("up should exit non-zero when a service crashes on startup; output:\n%s", out)
	}
	if !strings.Contains(string(out), "exited immediately") {
		t.Errorf("up should report the crash; output:\n%s", out)
	}
	if e.anyRunning(repo) {
		t.Error("a crashed service must not be recorded running")
	}
}

func TestServiceSetupRunsBeforeCommand(t *testing.T) {
	e := newEnv(t)
	repo := e.newRepo(`project = "e2esetup"

[proxy]
enabled = false

[services.web]
setup = "echo ok > setup-ran.txt"
command = "sleep 120"
port_range = { min = 4811, max = 4819 }
proxy_port = 4810
`)
	e.isola(repo, "up")
	if !e.anyRunning(repo) {
		t.Fatal("service should be running after up")
	}
	if _, err := os.Stat(filepath.Join(repo, "setup-ran.txt")); err != nil {
		t.Errorf("setup should have run before the service (setup-ran.txt missing): %v", err)
	}
}

func TestServiceSetupFailureBlocksService(t *testing.T) {
	e := newEnv(t)
	repo := e.newRepo(`project = "e2esetupfail"

[proxy]
enabled = false

[services.web]
setup = "exit 3"
command = "sleep 120"
port_range = { min = 4821, max = 4829 }
proxy_port = 4820
`)
	out, _ := e.cmd(repo, bin, "up").CombinedOutput()
	if !strings.Contains(string(out), "setup failed") {
		t.Errorf("up should report the setup failure; output:\n%s", out)
	}
	if e.anyRunning(repo) {
		t.Error("a service whose setup failed must not be started")
	}
}

func TestRootSetupRunsAtRootBeforeServices(t *testing.T) {
	e := newEnv(t)
	// The service runs in a subdir, so a marker at the repo root can only come
	// from the top-level setup, which runs at the worktree root.
	repo := e.newRepo(`project = "e2erootsetup"

setup = "echo ok > root-setup-ran.txt"

[proxy]
enabled = false

[services.web]
dir = "web"
command = "sleep 120"
port_range = { min = 4831, max = 4839 }
proxy_port = 4830
`)
	if err := os.MkdirAll(filepath.Join(repo, "web"), 0o755); err != nil {
		t.Fatal(err)
	}

	e.isola(repo, "up")
	if !e.anyRunning(repo) {
		t.Fatal("service should be running after up")
	}
	if _, err := os.Stat(filepath.Join(repo, "root-setup-ran.txt")); err != nil {
		t.Errorf("root setup should have run at the worktree root (root-setup-ran.txt missing): %v", err)
	}
}

func TestRootSetupFailureAbortsUp(t *testing.T) {
	e := newEnv(t)
	repo := e.newRepo(`project = "e2erootsetupfail"

setup = "exit 3"

[proxy]
enabled = false

[services.web]
command = "sleep 120"
port_range = { min = 4841, max = 4849 }
proxy_port = 4840
`)
	out, err := e.cmd(repo, bin, "up").CombinedOutput()
	if err == nil {
		t.Errorf("up should exit non-zero when root setup fails; output:\n%s", out)
	}
	if !strings.Contains(string(out), "root setup failed") {
		t.Errorf("up should report the root setup failure; output:\n%s", out)
	}
	if e.anyRunning(repo) {
		t.Error("no service must start when root setup fails")
	}
}

func TestBackgroundProcessRunsWithoutPortOrURL(t *testing.T) {
	e := newEnv(t)
	// web is proxied; worker has neither port_range nor proxy_port, so it is a
	// first-class background process: it runs and is managed, but gets no port,
	// no proxy route, and no URL.
	repo := e.newRepo(`project = "e2eportless"

[proxy]
enabled = true

[services.web]
command = "sleep 120"
port_range = { min = 4971, max = 4979 }
proxy_port = 4970

[services.worker]
command = "sleep 120"
`)
	e.isola(repo, "up")
	e.waitRunning(repo, "main")

	byService := map[string]lsEntry{}
	for _, entry := range e.ls(repo) {
		byService[entry.Service] = entry
	}
	web, okWeb := byService["web"]
	worker, okWorker := byService["worker"]
	if !okWeb || !okWorker {
		t.Fatalf("expected both web and worker in ls, got %v", byService)
	}
	if web.Status != "running" || worker.Status != "running" {
		t.Fatalf("both services should run: web=%s worker=%s", web.Status, worker.Status)
	}
	if worker.Port != 0 {
		t.Errorf("background process got port %d, want 0", worker.Port)
	}
	if worker.URL != "" {
		t.Errorf("background process got URL %q, want none", worker.URL)
	}
	if web.Port == 0 {
		t.Error("web should have an allocated port")
	}
	if web.URL == "" {
		t.Error("web should have a proxy URL")
	}
}

func TestWorktreeHookLifecycle(t *testing.T) {
	e := newEnv(t)
	repo := e.repo("e2ehook")

	e.isola(repo, "hooks", "install")

	// Creating a worktree fires post-checkout, which runs `isola up` in it.
	wt := filepath.Join(filepath.Dir(repo), "feat")
	e.git(repo, "worktree", "add", wt, "-b", "feat")
	e.waitRunning(repo, "feat")

	// Removing the worktree leaves an orphan; the next `up` reconciles it (git
	// fires no removal hook).
	e.git(repo, "worktree", "remove", "--force", wt)
	e.isola(repo, "up")
	if e.running(repo, "feat") {
		t.Error("the removed worktree's orphaned service should be reconciled away")
	}
}

func TestProxyRoutesToService(t *testing.T) {
	e := newEnv(t)
	repo := e.repoWith("e2eproxy", testServer, 4970)

	e.isola(repo, "up")
	status, body := e.waitReq(repo, "http", "main.e2eproxy.localhost", 4970, "/", 200, "ok")
	if status != 200 || strings.TrimSpace(body) != "ok" {
		t.Errorf("proxy should route to the service: status=%d body=%q", status, body)
	}
}

func TestProxyServesHTTPS(t *testing.T) {
	e := newEnv(t)
	// auto_trust=false so `up` never touches the system trust store; -k trusts
	// the freshly-minted dev cert for this request.
	body := fmt.Sprintf("project = \"e2ehttps\"\n\n[proxy]\nenabled = true\nhttps = true\nauto_trust = false\n\n[services.web]\ncommand = %q\nport_range = { min = 4961, max = 4969 }\nproxy_port = 4960\n", testServer)
	repo := e.newRepo(body)

	e.isola(repo, "up")
	status, got := e.waitReq(repo, "https", "main.e2ehttps.localhost", 4960, "/", 200, "ok")
	if status != 200 || strings.TrimSpace(got) != "ok" {
		t.Errorf("HTTPS proxy should route to the service: status=%d body=%q", status, got)
	}
}

func TestSiblingServiceReachableThroughProxy(t *testing.T) {
	e := newEnv(t)
	// web reaches api via the injected ${services.api.url}, through the proxy.
	body := fmt.Sprintf(`project = "e2ex"

[proxy]
enabled = true

[services.web]
command = %q
port_range = { min = 4942, max = 4944 }
proxy_port = 4940

[services.web.env]
TARGET_URL = "${services.api.url}"

[services.api]
command = %q
port_range = { min = 4945, max = 4947 }
proxy_port = 4941

[services.api.env]
MARKER = "api-marker"
`, testServer, testServer)
	repo := e.newRepo(body)

	e.isola(repo, "up")
	status, got := e.waitReq(repo, "http", "main.e2ex.localhost", 4940, "/call", 200, "via:api-marker")
	if status != 200 || strings.TrimSpace(got) != "via:api-marker" {
		t.Errorf("web should reach api through the proxy via the injected URL: status=%d body=%q", status, got)
	}
}

func TestSiblingReachableViaDirectURL(t *testing.T) {
	e := newEnv(t)
	// Proxy fully disabled: web reaches api via the injected
	// ${services.api.direct_url} (http://127.0.0.1:<port>), proving the DNS-free,
	// proxy-free path works. We reach web the same way, by its own direct URL.
	body := fmt.Sprintf(`project = "e2edirect"

[proxy]
enabled = false

[services.web]
command = %q
port_range = { min = 4951, max = 4953 }
proxy_port = 4950

[services.web.env]
TARGET_URL = "${services.api.direct_url}"

[services.api]
command = %q
port_range = { min = 4954, max = 4956 }
proxy_port = 4951

[services.api.env]
MARKER = "api-marker"
`, testServer, testServer)
	repo := e.newRepo(body)

	e.isola(repo, "up")
	// Find web's backend port from `isola ls` and hit it directly (no proxy).
	var webPort int
	for _, x := range e.ls(repo) {
		if x.Service == "web" && x.Port > 0 {
			webPort = x.Port
		}
	}
	if webPort == 0 {
		t.Fatal("could not determine web's backend port from ls")
	}
	status, got := e.waitReq(repo, "http", "127.0.0.1", webPort, "/call", 200, "via:api-marker")
	if status != 200 || strings.TrimSpace(got) != "via:api-marker" {
		t.Errorf("web should reach api via the direct loopback URL with no proxy: status=%d body=%q", status, got)
	}
}

func TestAccessoryProvisionReconcileAndDestroy(t *testing.T) {
	e := newEnv(t)
	serverURL := startPostgres(t)
	body := fmt.Sprintf(`project = "e2epg"

[proxy]
enabled = false

[services.web]
command = "sleep 120"
port_range = { min = 4801, max = 4809 }
proxy_port = 4800

[accessories.db]
kind = "postgres"
server_url = %q
clone_from = "myapp_dev"
name = "myapp_${ISOLA_BRANCH_SLUG}"
`, serverURL)
	repo := e.newRepo(body)

	// up provisions the main worktree's database from the template.
	e.isola(repo, "up")
	if !pgDatabaseExists(t, serverURL, "myapp_main") {
		t.Fatal("up should have provisioned myapp_main")
	}

	// A new worktree provisions its own database via the post-checkout hook.
	e.isola(repo, "hooks", "install")
	wt := filepath.Join(filepath.Dir(repo), "feature")
	e.git(repo, "worktree", "add", wt, "-b", "feature")
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) && !pgDatabaseExists(t, serverURL, "myapp_feature") {
		time.Sleep(300 * time.Millisecond)
	}
	if !pgDatabaseExists(t, serverURL, "myapp_feature") {
		t.Fatal("creating the feature worktree should have provisioned myapp_feature")
	}

	// Removing the worktree + reconcile (on the next up) drops its database but
	// keeps the still-live main worktree's.
	e.git(repo, "worktree", "remove", "--force", wt)
	e.isola(repo, "up")
	if pgDatabaseExists(t, serverURL, "myapp_feature") {
		t.Error("reconcile should have dropped the removed worktree's database")
	}
	if !pgDatabaseExists(t, serverURL, "myapp_main") {
		t.Error("the live worktree's database must be kept")
	}

	// destroy tears down the current worktree, dropping its database too.
	e.isola(repo, "destroy")
	if pgDatabaseExists(t, serverURL, "myapp_main") {
		t.Error("destroy should have dropped the current worktree's database")
	}
}

func TestOrcaConfigGenerated(t *testing.T) {
	e := newEnv(t)
	repo := e.repo("e2eorca")

	e.isola(repo, "orca")
	body, err := os.ReadFile(filepath.Join(repo, "orca.yaml"))
	if err != nil {
		t.Fatalf("orca.yaml not created: %v", err)
	}
	if !strings.Contains(string(body), "isola up") {
		t.Errorf("orca.yaml should run `isola up` on create:\n%s", body)
	}
}

func TestDestroyStopsCurrentWorktree(t *testing.T) {
	e := newEnv(t)
	repo := e.repo("e2edestroy")

	e.isola(repo, "up")
	if !e.anyRunning(repo) {
		t.Fatal("a service should be running after up")
	}
	e.isola(repo, "destroy")
	if e.anyRunning(repo) {
		t.Error("destroy should stop the current worktree's services")
	}
}

func TestProxyBrandedErrorForDeadBackend(t *testing.T) {
	e := newEnv(t)
	// sleep stays "running" but never binds $PORT, so the backend is dead.
	repo := e.repoWith("e2e502", "sleep 120", 4980)

	e.isola(repo, "up")
	status, body := e.waitReq(repo, "http", "main.e2e502.localhost", 4980, "/", 502, "")
	if status != 502 {
		t.Errorf("a dead backend should return 502, got %d (body %q)", status, body)
	}
	// The branded page is isola's own, not Go's blank 502: it is prefixed
	// "isola:" and explains that nothing is answering on the backend.
	if !strings.Contains(body, "isola:") || !strings.Contains(strings.ToLower(body), "answering") {
		t.Errorf("502 should be the branded isola page, got body:\n%s", body)
	}
}
