package browser

import (
	"fmt"
	"os/exec"
	"runtime"

	"github.com/cyucelen/isola/internal/slug"
)

// BuildURL constructs the project-qualified proxy URL for a service, e.g.
// "http://feature-auth.myapp.localhost:3000".
//
// The worktree label is fitted to the 63-byte DNS label limit here as well as at
// the point it is derived (git.HostLabel), so every URL isola prints, injects, or
// opens is one a resolver and a browser will accept even if a caller passes an
// unfitted slug. Fitting is idempotent, so a label that already fits is used
// verbatim and this agrees byte-for-byte with the Host the proxy matches.
func BuildURL(scheme, hostLabel, project string, proxyPort int) string {
	host := slug.Fit(hostLabel, slug.DNSLabelMax)
	if project != "" {
		host += "." + project
	}
	return fmt.Sprintf("%s://%s.localhost:%d", scheme, host, proxyPort)
}

// DirectURL constructs the loopback URL that reaches a service's backend
// directly, bypassing the proxy and DNS entirely, e.g. "http://127.0.0.1:3000".
// It uses 127.0.0.1 rather than "localhost" to match how the proxy dials
// backends and to avoid resolving to ::1 when a service listens only on IPv4.
func DirectURL(port int) string {
	return fmt.Sprintf("http://127.0.0.1:%d", port)
}

// Open opens the given URL in the default browser.
func Open(url string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", url).Start()
	case "windows":
		// On Windows, "start" requires an empty title argument before the URL.
		return exec.Command("cmd", "/c", "start", "", url).Start()
	default:
		return exec.Command("xdg-open", url).Start()
	}
}
