package proxy

import (
	"io"
	"net"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/cyucelen/isola/internal/config"
	"github.com/cyucelen/isola/internal/state"
)

func TestDirectRelayServerFirstAfterExplicitStart(t *testing.T) {
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
	_, backendText, err := net.SplitHostPort(backend.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	backendPort, err := strconv.Atoi(backendText)
	if err != nil {
		t.Fatal(err)
	}
	store, err := state.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	// Port 0 lets the test listener bind an available public port itself.
	ss := state.RunningServiceState(0, os.Getpid())
	ss.Direct = true
	ss.BackendPort = backendPort
	state.SetServiceState(st, "main", "mail", ss)
	if err := store.Save(st); err != nil {
		t.Fatal(err)
	}
	relay := &directListener{resolver: NewResolver(&config.Config{}, store), branch: "main", service: "mail"}
	if err := relay.listen(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(relay.close)
	client, err := net.Dial("tcp", relay.listeners[0].Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()
	_ = client.SetDeadline(time.Now().Add(time.Second))
	greeting := make([]byte, 5)
	if _, err := io.ReadFull(client, greeting); err != nil || string(greeting) != "hello" {
		t.Fatalf("server-first greeting: %q %v", greeting, err)
	}
}
