package proxy

import (
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/cyucelen/isola/internal/config"
	"github.com/cyucelen/isola/internal/registry"
	"github.com/cyucelen/isola/internal/state"
)

// helloBackend is a server-first backend that greets every connection with
// "hello"; it returns its loopback port.
func helloBackend(t *testing.T) int {
	t.Helper()
	backend, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = backend.Close() })
	go func() {
		for {
			conn, err := backend.Accept()
			if err != nil {
				return
			}
			go func() { defer func() { _ = conn.Close() }(); _, _ = io.WriteString(conn, "hello") }()
		}
	}()
	return portOf(t, backend.Addr())
}

// saveRunningDirect records main/<service> as a started daemon-fronted service
// whose child listens on backendPort.
func saveRunningDirect(t *testing.T, store *state.FileStore, service string, public, backendPort int) {
	t.Helper()
	st, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	ss := state.RunningServiceState(public, os.Getpid())
	ss.Direct = true
	ss.BackendPort = backendPort
	state.SetServiceState(st, "main", service, ss)
	if err := store.Save(st); err != nil {
		t.Fatal(err)
	}
}

// expectGreeting dials addr and requires the backend's greeting through the relay.
func expectGreeting(t *testing.T, addr string) {
	t.Helper()
	client, err := net.DialTimeout("tcp", addr, time.Second)
	if err != nil {
		t.Fatalf("dial %s: %v", addr, err)
	}
	defer func() { _ = client.Close() }()
	_ = client.SetDeadline(time.Now().Add(time.Second))
	greeting := make([]byte, 5)
	if _, err := io.ReadFull(client, greeting); err != nil || string(greeting) != "hello" {
		t.Fatalf("greeting via %s: %q %v", addr, greeting, err)
	}
}

func ipOf(t *testing.T, addr net.Addr) net.IP {
	t.Helper()
	tcp, ok := addr.(*net.TCPAddr)
	if !ok {
		t.Fatalf("not a TCP address: %v", addr)
	}
	return tcp.IP
}

// lanIPv4 returns a non-loopback IPv4 address of this host, standing in for
// the address a phone on the same network dials.
func lanIPv4(t *testing.T) net.IP {
	t.Helper()
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range addrs {
		if ipnet, ok := a.(*net.IPNet); ok && !ipnet.IP.IsLoopback() && !ipnet.IP.IsLinkLocalUnicast() {
			if ip4 := ipnet.IP.To4(); ip4 != nil {
				return ip4
			}
		}
	}
	t.Skip("no non-loopback IPv4 address on this host")
	return nil
}

func TestDirectRelayServerFirstAfterExplicitStart(t *testing.T) {
	store, err := state.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// Port 0 lets the test listener bind an available public port itself.
	saveRunningDirect(t, store, "mail", 0, helloBackend(t))
	relay := &directListener{resolver: NewResolver(&config.Config{}, store), branch: "main", service: "mail"}
	if err := relay.listen(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(relay.close)
	expectGreeting(t, relay.listeners[0].Addr().String())
}

func TestDirectListenerBindsLoopbackByDefault(t *testing.T) {
	lan := lanIPv4(t)
	store, err := state.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	public := freePort(t)
	saveRunningDirect(t, store, "api", public, helloBackend(t))
	relay := &directListener{port: public, resolver: NewResolver(&config.Config{}, store), branch: "main", service: "api"}
	if err := relay.listen(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(relay.close)
	for _, ln := range relay.listeners {
		if ip := ipOf(t, ln.Addr()); !ip.IsLoopback() {
			t.Errorf("default listener bound %v, want loopback", ip)
		}
	}
	expectGreeting(t, net.JoinHostPort("127.0.0.1", strconv.Itoa(public)))
	if conn, err := net.DialTimeout("tcp", net.JoinHostPort(lan.String(), strconv.Itoa(public)), time.Second); err == nil {
		_ = conn.Close()
		t.Fatalf("default listener accepted a connection on %s", lan)
	}
}

func TestDirectListenerBindAllAcceptsNetworkClients(t *testing.T) {
	lan := lanIPv4(t)
	store, err := state.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	public := freePort(t)
	saveRunningDirect(t, store, "api", public, helloBackend(t))
	relay := &directListener{port: public, all: true, resolver: NewResolver(&config.Config{}, store), branch: "main", service: "api"}
	if err := relay.listen(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(relay.close)
	for _, ln := range relay.listeners {
		if ip := ipOf(t, ln.Addr()); !ip.IsUnspecified() {
			t.Errorf("bind = all listener bound %v, want the unspecified address", ip)
		}
	}
	// Loopback keeps working for simulators and sibling direct_url refs.
	expectGreeting(t, net.JoinHostPort("127.0.0.1", strconv.Itoa(public)))
	expectGreeting(t, net.JoinHostPort(lan.String(), strconv.Itoa(public)))
}

// Editing bind in .isola.toml rebinds a live direct port on the next daemon
// sync, without `isola up` or a daemon restart, in both directions.
func TestDaemonRebindsDirectPortWhenBindChanges(t *testing.T) {
	lan := lanIPv4(t)
	d, reg := newTestDaemon(t)
	public := freePort(t)
	repo := t.TempDir()
	cfgPath := filepath.Join(repo, ".isola.toml")
	writeCfg := func(bind string, mtime time.Time) {
		toml := fmt.Sprintf(`project = "app"

[services.api]
command = "true"
on_demand = true
port_range = { min = 40000, max = 40099 }
bind = %q
`, bind)
		if err := os.WriteFile(cfgPath, []byte(toml), 0o600); err != nil {
			t.Fatal(err)
		}
		// The daemon reloads config on a changed stamp; make sure it moves.
		if err := os.Chtimes(cfgPath, mtime, mtime); err != nil {
			t.Fatal(err)
		}
	}
	writeCfg(config.BindLoopback, time.Now())
	stateDir := filepath.Join(repo, ".isola")
	store, err := state.NewFileStore(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	saveRunningDirect(t, store, "api", public, helloBackend(t))
	if err := reg.Register(registry.Project{Name: "app", StateDir: stateDir}); err != nil {
		t.Fatal(err)
	}
	lanAddr := net.JoinHostPort(lan.String(), strconv.Itoa(public))
	reachable := func() bool {
		conn, err := net.DialTimeout("tcp", lanAddr, time.Second)
		if err != nil {
			return false
		}
		_ = conn.Close()
		return true
	}

	syncOrFail(t, d)
	if reachable() {
		t.Fatal("bind = loopback: reachable from the network")
	}

	writeCfg(config.BindAll, time.Now().Add(time.Minute))
	syncOrFail(t, d)
	expectGreeting(t, lanAddr)

	writeCfg(config.BindLoopback, time.Now().Add(2*time.Minute))
	syncOrFail(t, d)
	if reachable() {
		t.Fatal("back to bind = loopback: still reachable from the network")
	}
	expectGreeting(t, net.JoinHostPort("127.0.0.1", strconv.Itoa(public)))
}
