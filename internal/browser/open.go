package browser

import (
	"fmt"
	"os/exec"
	"runtime"
)

// BuildURL constructs the project-qualified proxy URL for a service, e.g.
// "http://feature-auth.myapp.localhost:3000".
func BuildURL(scheme, slug, project string, proxyPort int) string {
	host := slug
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
