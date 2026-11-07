package process

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/cyucelen/isola/internal/state"
)

func stopVersion(st *state.State, branch, service string) uint64 {
	if ss := state.GetServiceState(st, branch, service); ss != nil {
		return ss.StopVersion
	}
	return 0
}

func (m *Manager) checkStart(branch, service string, version uint64) error {
	st, err := m.store.Snapshot()
	if err != nil {
		return err
	}
	if stopVersion(st, branch, service) != version {
		return fmt.Errorf("startup canceled by stop for %s/%s", branch, service)
	}
	return nil
}

// Poll persisted intent so a CLI stop can cancel setup owned by another process.
func (m *Manager) startContext(branch, service string, version uint64) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if m.checkStart(branch, service, version) != nil {
					cancel()
					return
				}
			}
		}
	}()
	return ctx, cancel
}

func (m *Manager) requestStop(branch, service string) error {
	return m.store.WithLock(func() error {
		st, err := m.store.Load()
		if err != nil {
			return err
		}
		ss := state.GetServiceState(st, branch, service)
		if ss == nil {
			ss = state.StoppedServiceState(state.GetPortAssignment(st, branch, service))
			state.SetServiceState(st, branch, service, ss)
		}
		ss.StopVersion++
		if ss.Status == state.StatusOnDemand {
			ss.Status = state.StatusStopped
			ss.Direct = false
			ss.ListenerPID = 0
		}
		return m.store.Save(st)
	})
}

// prepareStart records the actual child port before setup. Public port refs
// remain stable while daemon-fronted services bind a separate loopback backend.
func (m *Manager) prepareStart(branch, service string, public int, direct bool, version uint64) (int, error) {
	backend := public
	err := m.store.WithLock(func() error {
		st, err := m.store.Load()
		if err != nil {
			return err
		}
		if stopVersion(st, branch, service) != version {
			return fmt.Errorf("startup canceled by stop")
		}
		if direct {
			backend, err = m.freeBackendPort(st)
			if err != nil {
				return err
			}
		}
		ss := &state.ServiceState{Port: public, Status: state.StatusStarting, Direct: direct}
		if direct {
			ss.BackendPort = backend
		}
		state.SetServiceState(st, branch, service, ss)
		return m.store.Save(st)
	})
	return backend, err
}

func (m *Manager) freeBackendPort(st *state.State) (int, error) {
	used := map[int]bool{}
	for _, p := range st.PortAssignments {
		used[p] = true
	}
	for _, services := range st.Services {
		for _, ss := range services {
			if ss != nil {
				used[ss.BackendPort] = true
			}
		}
	}
	for _, svc := range m.cfg.Services {
		used[svc.ProxyPort] = true
	}
	for i := 0; i < 128; i++ {
		ln, err := net.Listen("tcp4", "127.0.0.1:0")
		if err != nil {
			return 0, err
		}
		addr, ok := ln.Addr().(*net.TCPAddr)
		_ = ln.Close()
		if !ok {
			return 0, fmt.Errorf("unexpected backend listener address")
		}
		if !used[addr.Port] {
			return addr.Port, nil
		}
	}
	return 0, fmt.Errorf("no private backend port available")
}
