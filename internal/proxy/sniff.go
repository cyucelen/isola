package proxy

import (
	"bufio"
	"crypto/tls"
	"errors"
	"net"
	"sync"
	"time"
)

// tlsRecordHandshake is the first byte of every TLS connection: the record type
// of the ClientHello. A plain HTTP request starts with an ASCII method instead.
const tlsRecordHandshake = 0x16

// sniffTimeout bounds how long a fresh connection may stay silent before its
// first byte decides the protocol. It matches the server's ReadHeaderTimeout.
const sniffTimeout = 10 * time.Second

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

	errMu sync.Mutex
	err   error
}

func newDualListener(ln net.Listener, tlsConfig *tls.Config) *dualListener {
	d := &dualListener{
		Listener:  ln,
		tlsConfig: tlsConfig,
		conns:     make(chan net.Conn),
		done:      make(chan struct{}),
	}
	go d.acceptLoop()
	return d
}

func (d *dualListener) acceptLoop() {
	for {
		c, err := d.Listener.Accept()
		if err != nil {
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				continue
			}
			d.errMu.Lock()
			d.err = err
			d.errMu.Unlock()
			_ = d.Close()
			return
		}
		go d.classify(c)
	}
}

func (d *dualListener) classify(c net.Conn) {
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
		return c, nil
	case <-d.done:
		d.errMu.Lock()
		defer d.errMu.Unlock()
		if d.err != nil {
			return nil, d.err
		}
		return nil, net.ErrClosed
	}
}

// Close stops accepting; connections still being classified are closed.
func (d *dualListener) Close() error {
	var err error
	d.closeOnce.Do(func() {
		close(d.done)
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
