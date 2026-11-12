package proxy

import (
	"bufio"
	"crypto/tls"
	"errors"
	"net"
	"sync"
	"time"

	"github.com/cyucelen/isola/internal/logging"
)

// tlsRecordHandshake is the first byte of every TLS connection: the record type
// of the ClientHello. A plain HTTP request starts with an ASCII method instead.
const tlsRecordHandshake = 0x16

// sniffTimeout bounds how long a fresh connection may stay silent before its
// first byte decides the protocol. It matches the server's ReadHeaderTimeout,
// which restarts once the connection is handed over, so a client that sends one
// byte and stalls is held at most twice this long.
const sniffTimeout = 10 * time.Second

// maxAcceptBackoff caps the retry delay after a temporary Accept error, as
// net/http does for a plain listener.
const maxAcceptBackoff = time.Second

// dualListener serves plain HTTP and TLS on one port. Proxy ports are shared by
// every registered project, and each project picks its own scheme, so a port
// cannot be bound as one or the other: a project with `https = false` sharing
// :3000 with an HTTPS project would get "client sent an HTTP request to an HTTPS
// server" (or the reverse, depending on which registered first). Each accepted
// connection is classified by its first byte instead.
//
// Classification runs off the Accept path so one silent client cannot stall
// every other connection on the port.
type dualListener struct {
	net.Listener
	tlsConfig *tls.Config

	conns     chan net.Conn
	done      chan struct{}
	closeOnce sync.Once

	mu      sync.Mutex
	err     error                 // permanent Accept error, returned once done
	pending map[net.Conn]struct{} // connections still being classified
}

func newDualListener(ln net.Listener, tlsConfig *tls.Config) *dualListener {
	d := &dualListener{
		Listener:  ln,
		tlsConfig: tlsConfig,
		conns:     make(chan net.Conn),
		done:      make(chan struct{}),
		pending:   map[net.Conn]struct{}{},
	}
	go d.acceptLoop()
	return d
}

func (d *dualListener) acceptLoop() {
	var backoff time.Duration
	for {
		c, err := d.Listener.Accept()
		if err == nil {
			backoff = 0
			if d.track(c) {
				go d.classify(c)
			} else {
				_ = c.Close()
			}
			continue
		}
		if d.closed() || errors.Is(err, net.ErrClosed) {
			_ = d.Close()
			return
		}
		// A temporary error (EMFILE/ENFILE when out of file descriptors) must
		// not take the port down: back off and retry, as http.Server does.
		if isTemporary(err) {
			if backoff == 0 {
				backoff = 5 * time.Millisecond
			} else {
				backoff = min(2*backoff, maxAcceptBackoff)
			}
			logging.Warn("proxy: accept error on %s: %v; retrying in %v", d.Addr(), err, backoff)
			select {
			case <-time.After(backoff):
				continue
			case <-d.done:
				return
			}
		}
		d.mu.Lock()
		d.err = err
		d.mu.Unlock()
		_ = d.Close()
		return
	}
}

// isTemporary reports whether err is worth retrying. Temporary is deprecated on
// net.Error in general, but it is still how syscall errors such as EMFILE say
// they may clear, and it is what net/http's own accept loop checks.
func isTemporary(err error) bool {
	var te interface{ Temporary() bool }
	if errors.As(err, &te) && te.Temporary() {
		return true
	}
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

func (d *dualListener) closed() bool {
	select {
	case <-d.done:
		return true
	default:
		return false
	}
}

// track records c as being classified, so Close can close it. It refuses once
// the listener is closed; Close takes mu after closing done, so a connection is
// either tracked before Close sweeps or refused here.
func (d *dualListener) track(c net.Conn) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed() {
		return false
	}
	d.pending[c] = struct{}{}
	return true
}

func (d *dualListener) untrack(c net.Conn) {
	d.mu.Lock()
	delete(d.pending, c)
	d.mu.Unlock()
}

func (d *dualListener) classify(c net.Conn) {
	defer d.untrack(c)
	_ = c.SetReadDeadline(time.Now().Add(sniffTimeout))
	br := bufio.NewReader(c)
	first, err := br.Peek(1)
	if err != nil {
		_ = c.Close()
		return
	}
	_ = c.SetReadDeadline(time.Time{})

	var out net.Conn = &peekedConn{Conn: c, r: br}
	if first[0] == tlsRecordHandshake {
		// A real *tls.Conn, so net/http sets Request.TLS as it would behind a
		// plain tls.Listener.
		out = tls.Server(out, d.tlsConfig)
	}
	select {
	case d.conns <- out:
	case <-d.done:
		_ = out.Close()
	}
}

// Accept returns the next classified connection.
func (d *dualListener) Accept() (net.Conn, error) {
	select {
	case c := <-d.conns:
		if !d.closed() {
			return c, nil
		}
		_ = c.Close() // raced with Close; don't hand out a connection after it
	case <-d.done:
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.err != nil {
		return nil, d.err
	}
	return nil, net.ErrClosed
}

// Close stops accepting and closes connections still being classified.
func (d *dualListener) Close() error {
	var err error
	d.closeOnce.Do(func() {
		close(d.done)
		d.mu.Lock()
		for c := range d.pending {
			_ = c.Close()
		}
		d.mu.Unlock()
		err = d.Listener.Close()
	})
	return err
}

// peekedConn replays the bytes buffered while sniffing before reading on.
type peekedConn struct {
	net.Conn
	r *bufio.Reader
}

func (p *peekedConn) Read(b []byte) (int, error) { return p.r.Read(b) }

// CloseWrite keeps the TCP half-close net/http uses to end a plain connection
// cleanly; embedding net.Conn alone would hide it.
func (p *peekedConn) CloseWrite() error {
	if cw, ok := p.Conn.(interface{ CloseWrite() error }); ok {
		return cw.CloseWrite()
	}
	return nil
}
