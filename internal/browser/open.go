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
