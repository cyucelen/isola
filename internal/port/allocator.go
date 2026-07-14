package port

import (
	"fmt"
	"hash/fnv"
	"net"
	"strconv"
	"time"

	"github.com/cyucelen/isola/internal/config"
)

// Allocate returns a port for the given branch and service using FNV32 hash.
// If fixedPort > 0, it is returned directly (after checking it is not already
// assigned to another service in `used`). Otherwise, hash-based allocation with
// linear probing is used.
func Allocate(branch, service string, svc config.ServiceConfig, fixedPort int, used map[int]bool) (int, error) {
	pr := svc.PortRange
	rangeSize := pr.Max - pr.Min + 1

	if fixedPort > 0 {
		if used[fixedPort] {
			return 0, fmt.Errorf("fixed port %d for %s/%s is already in use", fixedPort, branch, service)
		}
		return fixedPort, nil
	}

	base := hashPort(branch, service, pr.Min, pr.Max)

	for i := 0; i < rangeSize; i++ {
		candidate := pr.Min + (base-pr.Min+i)%rangeSize
		if !used[candidate] && Available(candidate) {
			return candidate, nil
		}
	}

	return 0, fmt.Errorf("no available port in range [%d, %d] for %s/%s", pr.Min, pr.Max, branch, service)
}

// hashPort returns a port within [minPort, maxPort] based on FNV32 of branch+service.
func hashPort(branch, service string, minPort, maxPort int) int {
	h := fnv.New32a()
	h.Write([]byte(branch + ":" + service))
	rangeSize := maxPort - minPort + 1
	return minPort + int(h.Sum32())%rangeSize
}

// Available reports whether a TCP port is free to use: nothing is currently
// listening on it via loopback (IPv4 or IPv6). It detects a listener by dialing
// rather than binding, because a bind-based check is defeated by SO_REUSEADDR (a
// wildcard bind can coexist with a loopback-specific one) and by address family,
// so it would hand the same port to two projects. Dialing is also exactly what
// the proxy does to reach a backend, so "reachable" is the property that matters.
//
// There is an inherent TOCTOU race between this check and the moment the child
// process binds the port, mitigated by (1) the file-level lock in
// state.FileStore serializing allocation across concurrent isola invocations,
// and (2) a clear error when a service fails to bind its assigned port.
func Available(port int) bool {
	for _, host := range []string{"127.0.0.1", "::1"} {
		conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, strconv.Itoa(port)), 200*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return false
		}
	}
	return true
}
