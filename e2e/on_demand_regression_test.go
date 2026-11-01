//go:build e2e

package e2e

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/creack/pty"
)

func TestDownCancelsQueuedSelectedService(t *testing.T) {
	e := newEnv(t)
	repo := e.newRepo(`[proxy]
enabled = false
[services.a]
on_demand = true
setup = "touch a-starting; while [ ! -f release-a ]; do sleep 0.05; done"
command = "sleep 60"
[services.b]
on_demand = true
command = "touch b-started; sleep 60"
`)
	start := e.cmd(repo, bin, "up", "--service", "a,b")
	var output bytes.Buffer
	start.Stdout, start.Stderr = &output, &output
	if err := start.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	finished := make(chan struct{})
	go func() {
		done <- start.Wait()
		close(finished)
	}()
	t.Cleanup(func() {
		_ = os.WriteFile(filepath.Join(repo, "release-a"), nil, 0600)
		select {
		case <-finished:
		case <-time.After(5 * time.Second):
			_ = start.Process.Kill()
			<-finished
		}
		_, _ = e.cmd(repo, bin, "down").CombinedOutput()
	})
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(repo, "a-starting")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("first service never entered setup")
		}
		time.Sleep(10 * time.Millisecond)
	}
	e.isola(repo, "down", "--service", "b")
	if err := os.WriteFile(filepath.Join(repo, "release-a"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err == nil || !strings.Contains(output.String(), "startup canceled by stop for main/b") {
			t.Fatalf("queued start did not report cancellation: %v\n%s", err, &output)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("startup did not finish")
	}
	if _, err := os.Stat(filepath.Join(repo, "b-started")); !os.IsNotExist(err) {
		t.Fatalf("stopped service was launched: %v", err)
	}
	for _, entry := range e.ls(repo) {
		if entry.Service == "a" && entry.Status != "running" {
			t.Fatalf("unaffected service did not start: %+v", entry)
		}
		if entry.Service == "b" && entry.Status != "stopped" {
			t.Fatalf("queued service did not stay stopped: %+v", entry)
		}
	}
	// A later explicit start is still allowed.
	e.isola(repo, "up", "--service", "b")
	if _, err := os.Stat(filepath.Join(repo, "b-started")); err != nil {
		t.Fatalf("explicit retry did not launch service: %v", err)
	}
}

func TestOnDemandDashboardDirectPorts(t *testing.T) {
	for _, tc := range []struct {
		name, key, message string
		deregister         bool
	}{
		{"start with no daemon", "s", "Started web for main", false},
		{"restart after deregistration", "r", "Restarted web for main", true},
		{"arm with no daemon", "a", "Started 0 services; 1 on demand", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			ln, err := net.Listen("tcp4", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			addr, ok := ln.Addr().(*net.TCPAddr)
			if !ok {
				t.Fatal("unexpected listener address")
			}
			publicPort := addr.Port
			_ = ln.Close()
			repo := e.newRepo(fmt.Sprintf(`project = "dashboard"
[services.web]
on_demand = true
command = %q
port_range = { min = %d, max = %d }
`, testServer, publicPort, publicPort))
			if tc.deregister {
				e.isola(repo, "up")
				e.isola(repo, "down", "--all")
				// Wait for the daemon to remove its old listener, so a request
				// cannot succeed through it before the next registry sync.
				deadline := time.Now().Add(5 * time.Second)
				for {
					conn, err := net.DialTimeout("tcp4", fmt.Sprintf("127.0.0.1:%d", publicPort), 100*time.Millisecond)
					if err != nil {
						break
					}
					_ = conn.Close()
					if time.Now().After(deadline) {
						t.Fatal("deregistered project's listener was not released")
					}
					time.Sleep(20 * time.Millisecond)
				}
			}
			cmd := e.cmd(repo, bin, "dash")
			cmd.Env = append(cmd.Env, "TERM=xterm-256color")
			terminal, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 30, Cols: 120})
			if err != nil {
				t.Fatal(err)
			}
			var mu sync.Mutex
			var transcript bytes.Buffer
			readDone := make(chan struct{})
			go func() {
				defer close(readDone)
				buf := make([]byte, 4096)
				for {
					n, err := terminal.Read(buf)
					mu.Lock()
					_, _ = transcript.Write(buf[:n])
					mu.Unlock()
					if err != nil {
						return
					}
				}
			}()
			done := make(chan error, 1)
			go func() { done <- cmd.Wait() }()
			t.Cleanup(func() {
				_, _ = io.WriteString(terminal, "q")
				select {
				case <-done:
				case <-time.After(3 * time.Second):
					_ = cmd.Process.Kill()
					<-done
				}
				_ = terminal.Close()
				<-readDone
			})
			waitText := func(want string) {
				t.Helper()
				deadline := time.Now().Add(10 * time.Second)
				for {
					mu.Lock()
					text := transcript.String()
					mu.Unlock()
					if strings.Contains(text, want) {
						return
					}
					if time.Now().After(deadline) {
						t.Fatalf("dashboard never displayed %q:\n%s", want, text)
					}
					time.Sleep(20 * time.Millisecond)
				}
			}
			waitText("web")
			if _, err := io.WriteString(terminal, tc.key); err != nil {
				t.Fatal(err)
			}
			waitText(tc.message)
			if tc.key == "a" {
				entries := e.ls(repo)
				if len(entries) != 1 || entries[0].Status != "on-demand" {
					t.Fatalf("start all should leave service idle until accessed: %+v", entries)
				}
			}
			status, body := e.req(repo, "http", "127.0.0.1", publicPort, "/")
			if status != 200 || body != "ok" {
				t.Fatalf("dashboard reported success but public port is unreachable: %d %q", status, body)
			}
		})
	}
}
