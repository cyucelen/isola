package proxy

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/cyucelen/isola/internal/registry"
	"github.com/cyucelen/isola/internal/state"
)

// freePort returns a loopback port nothing is listening on.
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	return portOf(t, ln.Addr())
}

func portOf(t *testing.T, addr net.Addr) int {
	t.Helper()
	tcp, ok := addr.(*net.TCPAddr)
	if !ok {
		t.Fatalf("not a TCP address: %v", addr)
	}
	return tcp.Port
}

// addProject writes a minimal repo (config + state) whose `main` worktree's web
// service is answered by a backend that replies with the project name, and
// registers it.
func addProject(t *testing.T, reg *registry.Store, name string, https bool, proxyPort int) {
	t.Helper()
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, name)
	}))
	t.Cleanup(backend.Close)
	backendPort := portOf(t, backend.Listener.Addr())

	repo := t.TempDir()
	toml := fmt.Sprintf(`project = %q

[proxy]
https = %t

[services.web]
command = "true"
port_range = { min = 40000, max = 40099 }
proxy_port = %d
`, name, https, proxyPort)
	if err := os.WriteFile(filepath.Join(repo, ".isola.toml"), []byte(toml), 0o600); err != nil {
		t.Fatal(err)
	}
	stateDir := filepath.Join(repo, ".isola")
	store, err := state.NewFileStore(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	st := &state.State{Services: map[string]map[string]*state.ServiceState{}, PortAssignments: map[string]int{}}
	state.SetPortAssignment(st, "main", "web", backendPort)
	if err := store.Save(st); err != nil {
		t.Fatal(err)
	}
	if err := reg.Register(registry.Project{Name: name, StateDir: stateDir, ProxyPorts: []int{proxyPort}, HTTPS: https}); err != nil {
		t.Fatal(err)
	}
}

// loopbackClient dials 127.0.0.1 for any *.localhost host, as a browser does.
func loopbackClient() *http.Client {
	dialer := &net.Dialer{Timeout: 2 * time.Second}
	return &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				_, port, _ := net.SplitHostPort(addr)
				return dialer.DialContext(ctx, network, net.JoinHostPort("127.0.0.1", port))
			},
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // test CA
		},
	}
}

func get(t *testing.T, url string) string {
	t.Helper()
	resp, err := loopbackClient().Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: %d %s", url, resp.StatusCode, body)
	}
	return string(body)
}

// TestDaemonServesBothSchemesOnSharedPort is the two-project regression: one
// project with `https = false` and one with `https = true` share a proxy port.
// The daemon used to bind the port as TLS only (the "superset"), so the HTTP
// project got "Client sent an HTTP request to an HTTPS server". Each project must
// be reachable on its own scheme, whichever registered first.
func TestDaemonServesBothSchemesOnSharedPort(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	reg, err := registry.Open()
	if err != nil {
		t.Fatal(err)
	}
	port := freePort(t)
	addProject(t, reg, "plain", false, port)
	addProject(t, reg, "secure", true, port)

	d := NewDaemon(reg)
	if err := d.sync(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(d.stopAll)

	p := strconv.Itoa(port)
	if got := get(t, "http://main.plain.localhost:"+p+"/"); got != "plain" {
		t.Errorf("http plain project = %q", got)
	}
	if got := get(t, "https://main.secure.localhost:"+p+"/"); got != "secure" {
		t.Errorf("https secure project = %q", got)
	}
	// The CA must exist once any project wants HTTPS, so `isola up` can trust it.
	if _, err := os.Stat(filepath.Join(reg.Dir(), "certs")); err != nil {
		t.Errorf("certs dir not created: %v", err)
	}
}

// TestDualListenerSilentClientDoesNotBlock: classification waits for a
// connection's first byte, which must not hold up other connections.
func TestDualListenerSilentClientDoesNotBlock(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	})}
	go func() { _ = srv.Serve(newDualListener(ln, &tls.Config{})) }()
	t.Cleanup(func() { _ = srv.Close() })

	silent, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = silent.Close() })

	resp, err := (&http.Client{Timeout: 2 * time.Second}).Get("http://" + ln.Addr().String() + "/")
	if err != nil {
		t.Fatalf("request behind a silent connection: %v", err)
	}
	_ = resp.Body.Close()
}
