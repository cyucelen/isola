// Package copyfiles copies gitignored local files (e.g. .env) from the main
// worktree into a linked worktree, since git worktrees do not include
// gitignored files.
package copyfiles

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Sync copies every file matching patterns from srcRoot into dstRoot, preserving
// each match's path relative to srcRoot, and returns the relative paths it
// copied. Patterns are globs relative to srcRoot (e.g. ".env", ".env.*",
// "config/local.yml"). It never overwrites a file that already exists in
// dstRoot, skips directories and matches that escape srcRoot, and is a no-op
// when srcRoot and dstRoot are the same directory (the main worktree).
func Sync(patterns []string, srcRoot, dstRoot string) ([]string, error) {
	srcAbs, err := filepath.Abs(srcRoot)
	if err != nil {
		return nil, err
	}
	dstAbs, err := filepath.Abs(dstRoot)
	if err != nil {
		return nil, err
	}
	if srcAbs == dstAbs {
		return nil, nil
	}

	var copied []string
	for _, pattern := range patterns {
		matches, err := filepath.Glob(filepath.Join(srcAbs, pattern))
		if err != nil {
			return copied, fmt.Errorf("invalid copy_files pattern %q: %w", pattern, err)
		}
		for _, src := range matches {
			rel, err := filepath.Rel(srcAbs, src)
			if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				continue // outside srcRoot
			}
			info, err := os.Stat(src)
			if err != nil || info.IsDir() {
				continue // only regular files
			}
			dst := filepath.Join(dstAbs, rel)
			if _, err := os.Stat(dst); err == nil {
				continue // never overwrite
			}
			if err := copyFile(src, dst, info.Mode()); err != nil {
				if os.IsExist(err) {
					continue // appeared concurrently; leave it
				}
				return copied, fmt.Errorf("copying %s: %w", rel, err)
			}
			copied = append(copied, rel)
		}
	}
	return copied, nil
}

// UpsertEnv sets each key in vars to its value in the dotenv file at path,
// replacing an existing assignment (correcting a value copied from elsewhere) or
// appending the key if absent, while leaving every other line and comment
// untouched. isola uses it so a worktree's env file carries that worktree's
// resolved env (accessory URLs, etc.), for tools that read the file directly
// rather than the process environment. If the file does not exist: when create
// is true it is created (with the given vars), otherwise it is a no-op (returns
// no keys). Returns the keys changed.
func UpsertEnv(path string, vars map[string]string, create bool) ([]string, error) {
	if len(vars) == 0 {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			if !create {
				return nil, nil // only touch an existing file
			}
			data = nil // fall through and create it from the appended keys
		} else {
			return nil, err
		}
	}

	keys := make([]string, 0, len(vars))
	for k := range vars {
		keys = append(keys, k)
	}
	sort.Strings(keys) // deterministic append order

	lines := strings.Split(string(data), "\n")
	seen := map[string]bool{}
	var changed []string
	for i, line := range lines {
		for _, k := range keys {
			if seen[k] {
				continue
			}
			if prefix, ok := assignPrefix(line, k); ok {
				lines[i] = prefix + k + "=" + envValue(vars[k])
				seen[k] = true
				changed = append(changed, k)
				break
			}
		}
	}

	// Append keys that had no existing assignment, before a trailing blank line
	// (a final newline) so the file's terminator is preserved.
	insert := len(lines)
	if insert > 0 && lines[insert-1] == "" {
		insert--
	}
	var appended []string
	for _, k := range keys {
		if !seen[k] {
			appended = append(appended, k+"="+envValue(vars[k]))
			changed = append(changed, k)
		}
	}
	if len(appended) > 0 {
		out := make([]string, 0, len(lines)+len(appended))
		out = append(out, lines[:insert]...)
		out = append(out, appended...)
		out = append(out, lines[insert:]...)
		lines = out
	}

	if len(changed) == 0 {
		return nil, nil
	}
	if create {
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return nil, err
		}
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0600); err != nil {
		return nil, err
	}
	sort.Strings(changed)
	return changed, nil
}

// assignPrefix reports whether line is an assignment of key (optionally
// `export`-prefixed), returning the `export ` prefix to preserve (or "").
// Commented lines (starting with #) do not match.
func assignPrefix(line, key string) (string, bool) {
	s := strings.TrimLeft(line, " \t")
	prefix := ""
	if rest := strings.TrimPrefix(s, "export "); rest != s {
		prefix = "export "
		s = strings.TrimLeft(rest, " \t")
	}
	if !strings.HasPrefix(s, key) {
		return "", false
	}
	s = strings.TrimLeft(s[len(key):], " \t")
	if !strings.HasPrefix(s, "=") {
		return "", false
	}
	return prefix, true
}

// envValue quotes v only if it contains characters that would otherwise be
// misparsed in a dotenv value (whitespace, comment marker, or quotes).
func envValue(v string) string {
	if strings.ContainsAny(v, " \t#\"'") {
		return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(v) + `"`
	}
	return v
}

func copyFile(src, dst string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()

	// O_EXCL so a file that appears between the caller's check and here is not
	// clobbered.
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}
