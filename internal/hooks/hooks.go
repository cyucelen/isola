// Package hooks manages isola's git post-checkout hook, which starts a new
// worktree's isolated environment the moment it is created.
//
// git worktree add runs the post-checkout hook in the new worktree with an
// all-zero old ref, whereas a plain branch switch passes a real old ref, so the
// hook can act only on new worktrees (and fresh clones). This is tool-agnostic:
// any worktree manager (Orca, Herd, an editor, plain git) that shells out to
// `git worktree add` triggers it.
package hooks

import (
	"os"
	"path/filepath"
	"strings"
)

// HookName is the git hook isola installs.
const HookName = "post-checkout"

const (
	beginMark = "# >>> isola (managed) >>>"
	endMark   = "# <<< isola (managed) <<<"
	shebang   = "#!/bin/sh"
)

// managedBlock is the isola-owned section, rewritten in place on each install.
// It never exits the script, so it is safe to chain after other post-checkout
// logic. The `*[!0]*` pattern matches any ref containing a non-zero character
// (a real SHA), so an all-zero old ref (new worktree / fresh clone) falls to the
// default branch. This works for both SHA-1 and SHA-256 repositories.
const managedBlock = beginMark + `
# On a new worktree or fresh clone, start this worktree's isolated dev
# environment. A branch switch passes a real old ref and is skipped.
# Skipped when: ISOLA_NO_UP is set; there is no .isola.toml; isola is not on
# PATH; or Orca is configured to run isola itself (orca.yaml wires it), so the
# environment is not started twice.
case "$1" in
  *[!0]*) : ;;
  *)
    if [ "$3" = "1" ] && [ -z "$ISOLA_NO_UP" ] && command -v isola >/dev/null 2>&1 && [ -f .isola.toml ] &&
       ! { [ -f orca.yaml ] && grep -q 'isola up' orca.yaml 2>/dev/null; }; then
      isola up || true
    fi
    ;;
esac
` + endMark

// upsertBlock returns content with isola's managed block inserted or refreshed.
func upsertBlock(content string) string {
	if b, e := strings.Index(content, beginMark), strings.Index(content, endMark); b != -1 && e != -1 && e > b {
		return content[:b] + managedBlock + content[e+len(endMark):]
	}
	if strings.TrimSpace(content) == "" {
		return shebang + "\n\n" + managedBlock + "\n"
	}
	sep := "\n\n"
	if strings.HasSuffix(content, "\n") {
		sep = "\n"
	}
	return content + sep + managedBlock + "\n"
}

// removeBlock strips isola's managed block. It returns "" when only the shebang
// (or nothing) is left, signaling the caller to delete the file.
func removeBlock(content string) string {
	b, e := strings.Index(content, beginMark), strings.Index(content, endMark)
	if b == -1 || e == -1 || e < b {
		return content
	}
	out := strings.TrimRight(content[:b]+content[e+len(endMark):], "\n")
	if trimmed := strings.TrimSpace(out); trimmed == "" || trimmed == shebang {
		return ""
	}
	return out + "\n"
}

// Install ensures the post-checkout hook in hooksDir contains isola's block,
// creating the directory and an executable hook file as needed. It preserves
// any existing hook content, appending (or refreshing) only isola's block.
// Returns whether the file changed.
func Install(hooksDir string) (bool, error) {
	if err := os.MkdirAll(hooksDir, 0755); err != nil {
		return false, err
	}
	path := filepath.Join(hooksDir, HookName)
	old, err := readIfExists(path)
	if err != nil {
		return false, err
	}
	updated := upsertBlock(old)
	if updated == old {
		return false, os.Chmod(path, 0755)
	}
	if err := os.WriteFile(path, []byte(updated), 0755); err != nil {
		return false, err
	}
	return true, os.Chmod(path, 0755)
}

// Uninstall removes isola's block from the hook in hooksDir, deleting the file
// if nothing else remains. Returns whether anything changed.
func Uninstall(hooksDir string) (bool, error) {
	path := filepath.Join(hooksDir, HookName)
	old, err := readIfExists(path)
	if err != nil {
		return false, err
	}
	if !strings.Contains(old, beginMark) {
		return false, nil
	}
	updated := removeBlock(old)
	if updated == "" {
		return true, os.Remove(path)
	}
	return true, os.WriteFile(path, []byte(updated), 0755)
}

// Installed reports whether isola's block is present in hooksDir's hook.
func Installed(hooksDir string) bool {
	old, _ := readIfExists(filepath.Join(hooksDir, HookName))
	return strings.Contains(old, beginMark)
}

func readIfExists(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return string(b), nil
}
