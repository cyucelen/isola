package proxy

import (
	"context"
	"fmt"
	"net"
	"path/filepath"
	"strconv"
	"time"

	"github.com/cyucelen/isola/internal/config"
	"github.com/cyucelen/isola/internal/git"
	"github.com/cyucelen/isola/internal/logging"
	"github.com/cyucelen/isola/internal/port"
	"github.com/cyucelen/isola/internal/process"
	"github.com/cyucelen/isola/internal/state"
)

const onDemandWait = 2 * time.Minute

type pendingStart struct {
	done chan struct{}
	err  error
}

// wake waits for a single shared startup per worktree/service. A disconnected
// browser does not cancel setup for other requests. No HTTP request is sent to
// the backend until it listens, so even POST bodies are forwarded just once.
func (r *Resolver) wake(ctx context.Context, branch, service string, backend int) error {
	key := state.PortKey(branch, service)
	r.mu.Lock()
	pending := r.starts[key]
	if pending == nil {
		st, err := r.store.LoadLocked()
		if err != nil {
			r.mu.Unlock()
			return err
		}
		ss := state.GetServiceState(st, branch, service)
		if ss == nil || ss.Status != state.StatusOnDemand {
			r.mu.Unlock()
			return nil
		}
		pending = &pendingStart{done: make(chan struct{})}
		r.starts[key] = pending
		go func() {
			pending.err = r.startOnDemand(branch, service, backend)
			if pending.err != nil {
				logging.Warn("proxy: starting %s: %v", key, pending.err)
			}
			r.mu.Lock()
			delete(r.starts, key)
			close(pending.done)
			r.mu.Unlock()
		}()
	}
	r.mu.Unlock()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-pending.done:
		return pending.err
	}
}

func (r *Resolver) startOnDemand(branch, service string, backend int) error {
	root := filepath.Dir(r.store.Dir())
	trees, err := git.ListWorktrees(root)
	if err != nil {
		return err
	}
	var tree *git.Worktree
	for i := range trees {
		if !trees[i].IsBare && trees[i].Branch == branch {
			tree = &trees[i]
			break
		}
	}
	if tree == nil {
		return fmt.Errorf("worktree %q no longer exists", branch)
	}
	// Read the target worktree's config, just as an explicit `up --service`
	// there would. The shared daemon may have been launched from another repo.
	cfg, err := config.Load(tree.Path)
	if err != nil {
		return err
	}
	cfg.Project = cfg.ProjectName(root)
	svc, ok := cfg.Services[service]
	if !ok || !svc.OnDemand {
		return fmt.Errorf("service %q is no longer on demand; run isola up", service)
	}
	mgr := process.NewManager(cfg, r.store, port.NewRegistry(r.store, cfg))
	// Root setup and file copying already ran when `up` armed this service.
	results := mgr.WakeService(tree, service)
	if len(results) != 1 {
		return fmt.Errorf("service %q could not be started", service)
	}
	if results[0].Err != nil {
		return results[0].Err
	}
	if results[0].Port != backend {
		return fmt.Errorf("service port changed; retry the request")
	}
	ctx, cancel := context.WithTimeout(context.Background(), onDemandWait)
	defer cancel()
	return waitForBackend(ctx, backend, results[0].PID)
}

func waitForBackend(ctx context.Context, backend, pid int) error {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		if !process.IsProcessRunning(pid) {
			return fmt.Errorf("service exited before listening; check isola logs")
		}
		conn, err := (&net.Dialer{Timeout: 200 * time.Millisecond}).DialContext(ctx, "tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(backend)))
		if err == nil {
			_ = conn.Close()
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("waiting for service to listen: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}
