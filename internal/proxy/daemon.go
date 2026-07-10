package proxy

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/cyucelen/isola/internal/cert"
	"github.com/cyucelen/isola/internal/config"
	"github.com/cyucelen/isola/internal/logging"
	"github.com/cyucelen/isola/internal/registry"
	"github.com/cyucelen/isola/internal/state"
)

// syncInterval is how often the daemon re-reads the registry to pick up ports
// newly registered by projects that started after it did.
const syncInterval = 1 * time.Second

// Daemon is the single machine-wide reverse proxy. It binds the union of every
// registered project's proxy ports and routes <slug>.<project>.localhost to that
// project's backends, resolving live from each project's own state.
type Daemon struct {
	reg     *registry.Store
	certDir string

	mu        sync.Mutex
	servers   map[int]*http.Server
	getCert   func(*tls.ClientHelloInfo) (*tls.Certificate, error)
	resolvers map[string]*cachedResolver // by project name
}

type cachedResolver struct {
	stateDir string
	resolver *Resolver
}

// NewDaemon creates a Daemon backed by the given registry. Certificates for
// HTTPS ports live under the registry's global dir, so one CA covers every
// project and `isola trust` runs once.
func NewDaemon(reg *registry.Store) *Daemon {
	return &Daemon{
		reg:       reg,
		certDir:   filepath.Join(reg.Dir(), "certs"),
		servers:   map[int]*http.Server{},
		resolvers: map[string]*cachedResolver{},
	}
}

// Serve binds current ports and keeps binding newly registered ones until ctx
// is canceled, then shuts every listener down.
func (d *Daemon) Serve(ctx context.Context) error {
	if err := d.sync(); err != nil {
		return err
	}
	ticker := time.NewTicker(syncInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			d.stopAll()
			return nil
		case <-ticker.C:
			if err := d.sync(); err != nil {
				logging.Warn("proxy: registry sync failed: %v", err)
			}
		}
	}
}

// sync opens a listener for every registered proxy port not already served.
func (d *Daemon) sync() error {
	httpsByPort, err := d.reg.HTTPSByPort()
	if err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	for port, https := range httpsByPort {
		if _, ok := d.servers[port]; ok {
			continue
		}
		if err := d.listenLocked(port, https); err != nil {
			logging.Warn("proxy: cannot serve port %d: %v", port, err)
		}
	}
	return nil
}

func (d *Daemon) listenLocked(port int, https bool) error {
	srv := &http.Server{
		Addr:              net.JoinHostPort("127.0.0.1", strconv.Itoa(port)),
		Handler:           recoveryMiddleware(d.handler(port)),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	ln, err := net.Listen("tcp", srv.Addr)
	if err != nil {
		return err
	}
	if https {
		getCert, err := d.certGetter()
		if err != nil {
			_ = ln.Close()
			return err
		}
		ln = tls.NewListener(ln, &tls.Config{GetCertificate: getCert})
	}
	d.servers[port] = srv
	scheme := "http"
	if https {
		scheme = "https"
	}
	logging.Info("proxy: serving %s on :%d", scheme, port)
	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			logging.Error("proxy: server on :%d stopped: %v", port, err)
		}
	}()
	return nil
}

// certGetter lazily initializes the shared per-SNI certificate minter.
func (d *Daemon) certGetter() (func(*tls.ClientHelloInfo) (*tls.Certificate, error), error) {
	if d.getCert != nil {
		return d.getCert, nil
	}
	paths, err := cert.EnsureCerts(d.certDir)
	if err != nil {
		return nil, fmt.Errorf("generating certificates: %w", err)
	}
	getCert, err := cert.NewSNIGetCertificate(paths)
	if err != nil {
		return nil, fmt.Errorf("initializing certificate minting: %w", err)
	}
	d.getCert = getCert
	return getCert, nil
}

func (d *Daemon) handler(port int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		slug, project := ParseHost(r.Host)
		if project == "" {
			writeProxyError(w, r, http.StatusNotFound, "Project-qualified URL needed",
				fmt.Sprintf("Use `http://<branch>.<project>.localhost:%d`\nThe bare `<branch>.localhost` form is not routed.", port))
			return
		}
		res, err := d.resolverFor(project)
		if err != nil {
			writeProxyError(w, r, http.StatusNotFound, "Unknown project",
				fmt.Sprintf("No project %q is registered on this machine.\nRun `isola up` in that repo.", project))
			return
		}
		backendPort, err := res.Resolve(slug, port)
		if err != nil {
			writeProxyError(w, r, http.StatusNotFound, "Worktree not reachable",
				fmt.Sprintf("No worktree %q in project %q is serving port %d.\nIs it `isola up`?", slug, project, port))
			return
		}
		target, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", backendPort))
		rp := &httputil.ReverseProxy{
			Rewrite: func(pr *httputil.ProxyRequest) {
				pr.SetURL(target)
				pr.Out.Host = r.Host
				pr.Out.Header.Set("X-Forwarded-Host", r.Host)
			},
		}
		rp.ServeHTTP(w, r)
	})
}

// resolverFor returns a Resolver for the named project, building and caching it
// from the project's own config and state. The cache is invalidated if the
// project's registered state dir changes.
func (d *Daemon) resolverFor(project string) (*Resolver, error) {
	p, ok, err := d.reg.Lookup(project)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("project %q not registered", project)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if c, ok := d.resolvers[project]; ok && c.stateDir == p.StateDir {
		return c.resolver, nil
	}
	repoRoot := filepath.Dir(p.StateDir)
	cfg, err := config.Load(repoRoot)
	if err != nil {
		return nil, err
	}
	store, err := state.NewFileStore(p.StateDir)
	if err != nil {
		return nil, err
	}
	res := NewResolver(cfg, store)
	d.resolvers[project] = &cachedResolver{stateDir: p.StateDir, resolver: res}
	return res, nil
}

func (d *Daemon) stopAll() {
	d.mu.Lock()
	defer d.mu.Unlock()
	for port, srv := range d.servers {
		ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		_ = srv.Shutdown(ctx)
		cancel()
		delete(d.servers, port)
	}
}
