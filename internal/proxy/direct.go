package proxy

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/cyucelen/isola/internal/logging"
	"github.com/cyucelen/isola/internal/registry"
	"github.com/cyucelen/isola/internal/state"
)

type directListener struct {
	port            int
	resolver        *Resolver
	branch, service string
	listeners       []net.Listener
	ctx             context.Context
	cancel          context.CancelFunc
}

func directActive(ss *state.ServiceState) bool {
	return ss != nil && ss.Direct && ss.Port > 0 && (ss.Status == state.StatusOnDemand || ss.Status == state.StatusStarting || ss.Status == state.StatusRunning)
}

// syncDirect discovers public direct ports from live project state. Removing a
// worktree, stopping a service, or deregistering a project releases its listeners.
func (d *Daemon) syncDirect() error {
	projects, err := d.reg.List()
	if err != nil {
		return err
	}
	desired := map[string]*directListener{}
	for _, project := range projects {
		res, err := d.resolverFor(project.Name)
		if err != nil {
			continue
		}
		st, err := res.store.Snapshot()
		if err != nil {
			return err
		}
		for branch, services := range st.Services {
			for service, ss := range services {
				if directActive(ss) {
					key := project.StateDir + "\x00" + state.PortKey(branch, service)
					desired[key] = &directListener{port: ss.Port, resolver: res, branch: branch, service: service}
				}
			}
		}
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	for key, active := range d.direct {
		want := desired[key]
		if want == nil || want.port != active.port {
			active.close()
			delete(d.direct, key)
		}
	}
	for key, want := range desired {
		active := d.direct[key]
		if active == nil {
			if err := want.listen(); err != nil {
				logging.Warn("proxy: direct port %d for %s/%s: %v", want.port, want.branch, want.service, err)
				continue
			}
			active = want
			d.direct[key] = active
		}
		// Publish readiness without dialing (dialing would be ambiguous with traffic).
		if err := active.resolver.store.WithLock(func() error {
			st, err := active.resolver.store.Load()
			if err != nil {
				return err
			}
			ss := state.GetServiceState(st, active.branch, active.service)
			if directActive(ss) && ss.Port == active.port && ss.ListenerPID != os.Getpid() {
				ss.ListenerPID = os.Getpid()
				return active.resolver.store.Save(st)
			}
			return nil
		}); err != nil {
			return err
		}
	}
	return nil
}

func (l *directListener) listen() error {
	v4, err := net.Listen("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(l.port)))
	if err != nil {
		return err
	}
	l.listeners = append(l.listeners, v4)
	v6, err := net.Listen("tcp6", net.JoinHostPort("::1", strconv.Itoa(l.port)))
	switch {
	case err == nil:
		l.listeners = append(l.listeners, v6)
	case errors.Is(err, syscall.EAFNOSUPPORT), errors.Is(err, syscall.EADDRNOTAVAIL):
		// IPv6 is disabled on this host; the IPv4 direct URL still works.
	default:
		_ = v4.Close()
		return err
	}
	l.ctx, l.cancel = context.WithCancel(context.Background())
	for _, ln := range l.listeners {
		go l.accept(ln)
	}
	return nil
}

func (l *directListener) close() {
	l.cancel()
	for _, ln := range l.listeners {
		_ = ln.Close()
	}
}

func (l *directListener) accept(ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		go l.forward(conn)
	}
}

func (l *directListener) forward(client net.Conn) {
	defer func() { _ = client.Close() }()
	stopClose := context.AfterFunc(l.ctx, func() { _ = client.Close() })
	defer stopClose()
	reader := bufio.NewReader(client)
	st, err := l.resolver.store.Snapshot()
	if err != nil {
		return
	}
	ss := state.GetServiceState(st, l.branch, l.service)
	if ss == nil || ss.Status == state.StatusStopped {
		return
	}
	if ss.Status == state.StatusOnDemand {
		// Availability probes connect then close. Only actual traffic wakes an
		// idle service. An explicitly started server can send its greeting first.
		_ = client.SetReadDeadline(time.Now().Add(onDemandWait))
		if _, err := reader.Peek(1); err != nil {
			return
		}
		_ = client.SetReadDeadline(time.Time{})
	}
	ctx, cancel := context.WithTimeout(l.ctx, onDemandWait)
	defer cancel()
	if err := l.resolver.wake(ctx, l.branch, l.service, l.port); err != nil {
		return
	}
	backend, err := l.resolver.readyBackend(ctx, l.branch, l.service)
	if err != nil {
		return
	}
	upstream, err := (&net.Dialer{}).DialContext(ctx, "tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(backend)))
	if err != nil {
		return
	}
	defer func() { _ = upstream.Close() }()
	stopUpstream := context.AfterFunc(l.ctx, func() { _ = upstream.Close() })
	defer stopUpstream()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _ = io.Copy(upstream, reader)
		if tcp, ok := upstream.(*net.TCPConn); ok {
			_ = tcp.CloseWrite()
		}
	}()
	_, _ = io.Copy(client, upstream)
	// Closing both directions also releases a client that never finishes writing.
	_ = upstream.Close()
	_ = client.Close()
	wg.Wait()
}

// WaitDirectListeners lets `up` return only after the daemon owns the advertised
// ports. A stale readiness record from a previous daemon cannot satisfy it.
func WaitDirectListeners(reg *registry.Store, store *state.FileStore, branches []string) error {
	deadline := time.Now().Add(startTimeout)
	for {
		daemon, err := reg.GetDaemon()
		if err != nil {
			return err
		}
		st, err := store.Snapshot()
		if err != nil {
			return err
		}
		ready := true
		for _, branch := range branches {
			for _, ss := range st.Services[branch] {
				if directActive(ss) && (!daemon.Running || ss.ListenerPID != daemon.PID) {
					ready = false
				}
			}
		}
		if ready {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("direct-port listeners not ready; check ~/.isola/logs/proxy.log and restart an older proxy")
		}
		time.Sleep(50 * time.Millisecond)
	}
}
