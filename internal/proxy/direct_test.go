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

// expectRefused requires that nothing accepts a connection on addr.
func expectRefused(t *testing.T, addr, why string) {
	t.Helper()
	if conn, err := net.DialTimeout("tcp", addr, time.Second); err == nil {
		_ = conn.Close()
		t.Fatalf("%s: %s accepted a connection", why, addr)
	}
}

// lanIPv4 returns the IPv4 address this host would send from on its default
// route, standing in for the address a phone on the same network dials.
// Connecting a UDP socket sends nothing; it only selects the route, which
// skips VPN and container bridge addresses that only reach themselves.
func lanIPv4(t *testing.T) net.IP {
	t.Helper()
	conn, err := net.Dial("udp4", "192.0.2.1:9") // TEST-NET-1, never routed to a real host
	if err != nil {
		t.Skipf("no IPv4 default route: %v", err)
	}
	defer func() { _ = conn.Close() }()
	ip := tcpOrUDPAddrIP(t, conn.LocalAddr())
	if ip.IsLoopback() || ip.IsUnspecified() {
		t.Skipf("default route source %v is not a network address", ip)
	}
	return ip
}

func tcpOrUDPAddrIP(t *testing.T, addr net.Addr) net.IP {
	t.Helper()
	switch a := addr.(type) {
	case *net.TCPAddr:
		return a.IP
	case *net.UDPAddr:
		return a.IP
	}
	t.Fatalf("not an IP address: %v", addr)
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

func TestDirectListenerInterfaces(t *testing.T) {
	lan := lanIPv4(t)
	tests := []struct {
		name    string
		bindAll bool
		// wantBound checks each listener's address.
		wantBound func(net.IP) bool
		lanOpen   bool
	}{
		{"loopback by default", false, net.IP.IsLoopback, false},
		{"bind all", true, net.IP.IsUnspecified, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store, err := state.NewFileStore(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			public := freePort(t)
			saveRunningDirect(t, store, "api", public, helloBackend(t))
			relay := &directListener{port: public, bindAll: tt.bindAll, resolver: NewResolver(&config.Config{}, store), branch: "main", service: "api"}
			if err := relay.listen(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(relay.close)
			for _, ln := range relay.listeners {
				if ip := tcpOrUDPAddrIP(t, ln.Addr()); !tt.wantBound(ip) {
					t.Errorf("listener bound %v", ip)
				}
			}
			// Loopback always works, for simulators and sibling direct_url refs.
			port := strconv.Itoa(public)
			expectGreeting(t, net.JoinHostPort("127.0.0.1", port))
			if len(relay.listeners) == 2 { // IPv6 is available on this host
				expectGreeting(t, net.JoinHostPort("::1", port))
			}
			lanAddr := net.JoinHostPort(lan.String(), port)
			if tt.lanOpen {
				expectGreeting(t, lanAddr)
			} else {
				expectRefused(t, lanAddr, "loopback listener")
			}
		})
	}
}

// addDirectProject registers project "app" whose on-demand api service is
// running behind a daemon direct port with the given bind. It returns the
// public port, the state store, and a function that rewrites bind in the
// project's config.
func addDirectProject(t *testing.T, reg *registry.Store, bind string) (int, *state.FileStore, func(bind string)) {
	t.Helper()
	repo := t.TempDir()
	cfgPath := filepath.Join(repo, ".isola.toml")
	mtime := time.Now()
	writeCfg := func(bind string) {
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
		mtime = mtime.Add(time.Minute)
		if err := os.Chtimes(cfgPath, mtime, mtime); err != nil {
			t.Fatal(err)
		}
	}
	writeCfg(bind)
	stateDir := filepath.Join(repo, ".isola")
	store, err := state.NewFileStore(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	public := freePort(t)
	saveRunningDirect(t, store, "api", public, helloBackend(t))
	if err := reg.Register(registry.Project{Name: "app", StateDir: stateDir}); err != nil {
		t.Fatal(err)
	}
	return public, store, writeCfg
}

// Editing bind in .isola.toml rebinds a live direct port on the next daemon
// sync, without `isola up` or a daemon restart, in both directions.
func TestDaemonRebindsDirectPortWhenBindChanges(t *testing.T) {
	lan := lanIPv4(t)
	d, reg := newTestDaemon(t)
	public, _, writeCfg := addDirectProject(t, reg, config.BindLoopback)
	lanAddr := net.JoinHostPort(lan.String(), strconv.Itoa(public))

	syncOrFail(t, d)
	expectRefused(t, lanAddr, "bind = loopback")

	writeCfg(config.BindAll)
	syncOrFail(t, d)
	expectGreeting(t, lanAddr)

	writeCfg(config.BindLoopback)
	syncOrFail(t, d)
	expectRefused(t, lanAddr, "back to bind = loopback")
	expectGreeting(t, net.JoinHostPort("127.0.0.1", strconv.Itoa(public)))
}

// A direct port the daemon cannot bind withdraws its readiness record, so
// `isola up` reports the failure instead of trusting a listener that is gone.
func TestDaemonWithdrawsReadinessWhenBindFails(t *testing.T) {
	d, reg := newTestDaemon(t)
	public, store, _ := addDirectProject(t, reg, config.BindAll)
	// Another process holds the exact address the daemon needs.
	blocker, err := net.Listen("tcp4", net.JoinHostPort("0.0.0.0", strconv.Itoa(public)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = blocker.Close() })
	// A record left by this daemon from before the bind failed.
	if err := store.WithLock(func() error {
		st, err := store.Load()
		if err != nil {
			return err
		}
		state.GetServiceState(st, "main", "api").ListenerPID = os.Getpid()
		return store.Save(st)
	}); err != nil {
		t.Fatal(err)
	}

	syncOrFail(t, d)

	st, err := store.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if pid := state.GetServiceState(st, "main", "api").ListenerPID; pid != 0 {
		t.Errorf("ListenerPID = %d after a failed bind, want 0", pid)
	}
}
