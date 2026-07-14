package git

import (
	"fmt"
	"path/filepath"
	"strings"
)

// gitOutput runs a git command in dir (with a sanitized environment via gitCmd)
// and returns its stdout trimmed of surrounding whitespace.
func gitOutput(dir string, args ...string) (string, error) {
	out, err := gitCmd(dir, args...).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// FindRepoRoot returns the root directory of the git repository
// that contains the given directory (or the current directory).
func FindRepoRoot(dir string) (string, error) {
	out, err := gitOutput(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("not a git repository (or any parent): %w", err)
	}
	return out, nil
}

// CommonDir returns the git common directory (the .git dir of the main worktree).
// For worktrees, this points to the main repo's .git directory.
func CommonDir(dir string) (string, error) {
	result, err := gitOutput(dir, "rev-parse", "--git-common-dir")
	if err != nil {
		return "", fmt.Errorf("failed to get git common dir: %w", err)
	}
	if !filepath.IsAbs(result) {
		result = filepath.Join(dir, result)
	}
	return filepath.Clean(result), nil
}

// MainWorktreeRoot returns the root directory of the main worktree
// by resolving the common git dir.
func MainWorktreeRoot(dir string) (string, error) {
	commonDir, err := CommonDir(dir)
	if err != nil {
		return "", err
	}
	// commonDir is typically /path/to/repo/.git
	// The main worktree root is its parent
	return filepath.Dir(commonDir), nil
}

// ConfigGet returns the value of a git config key for the repo at dir, or the
// empty string if it is unset. It runs with a sanitized environment (see
// SanitizedEnv) so an inherited GIT_DIR — as git sets when invoking hooks —
// cannot hijack the lookup and read another repo's config.
func ConfigGet(dir, key string) string {
	out, _ := gitOutput(dir, "config", "--get", key) // "" on unset or error
	return out
}

// ConfigSet sets a git config key to val for the repo at dir, with the same
// environment sanitization as ConfigGet.
func ConfigSet(dir, key, val string) error {
	if err := gitCmd(dir, "config", key, val).Run(); err != nil {
		return fmt.Errorf("git config %s: %w", key, err)
	}
	return nil
}
