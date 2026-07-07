package process

import "path/filepath"

// LogFileName returns the log file name for a service in a worktree,
// e.g. "feature-auth.frontend.log".
func LogFileName(slug, service string) string {
	return slug + "." + service + ".log"
}

// LogPath returns the absolute path to a service's log file, given the isola
// state directory (the ".isola" dir). Log files live under "<stateDir>/logs".
func LogPath(stateDir, slug, service string) string {
	return filepath.Join(stateDir, "logs", LogFileName(slug, service))
}
