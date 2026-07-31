package git

import (
	"bufio"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/cyucelen/isola/internal/slug"
)

// Worktree represents a single git worktree.
type Worktree struct {
	Path   string // absolute path
	Branch string // branch name (e.g., "main", "feature/auth")
	Head   string // HEAD commit hash
	IsBare bool
}

// HostLabel returns the worktree's routing identity: the DNS label that carries
// it in "<label>.<project>.localhost", in its log file names, and in
// ISOLA_BRANCH_SLUG. See [HostLabel].
func (w *Worktree) HostLabel() string {
	return HostLabel(w.Branch)
}

// BranchSlug converts a branch name to a URL-safe slug, with no length bound.
// It is the *input* to derived names, not a name itself: use it where the
// consumer applies its own budget (an accessory's resource name, via
// accessory.WorktreeInfo.ExpandWithin). For anything that appears in a hostname,
// use [HostLabel].
func BranchSlug(branch string) string {
	return slug.Make(branch)
}

// HostLabel converts a branch name to the DNS label isola routes it on, fitted
// to the 63-byte limit a label may not exceed. Long branch names (the automated
// "dependabot/npm_and_yarn/services/<svc>/<pkg>-<version>" shape passes 63 on its
// own) are shortened with a hash of the full slug, so distinct branches stay
// distinct. Labels that already fit are returned unchanged.
//
// Everything that must agree on the host derives it here: the URL isola prints
// and injects, the Host the proxy matches, and the SNI name the dev cert is
// minted for. An over-long label breaks all three at once — a resolver will not
// look it up, and a browser will not match a certificate SAN containing it — so
// the services run but no browser can reach them.
func HostLabel(branch string) string {
	return slug.Fit(BranchSlug(branch), slug.DNSLabelMax)
}

// DetectHostLabelCollisions returns a map of host label -> branch names for any
// label shared by more than one branch. An empty map means no collisions. Two
// branches on one label make proxy routing ambiguous, since the label is all the
// Host header carries.
func DetectHostLabelCollisions(trees []Worktree) map[string][]string {
	byLabel := map[string][]string{}
	for _, t := range trees {
		if t.IsBare {
			continue
		}
		l := t.HostLabel()
		byLabel[l] = append(byLabel[l], t.Branch)
	}
	collisions := map[string][]string{}
	for l, branches := range byLabel {
		if len(branches) > 1 {
			collisions[l] = branches
		}
	}
	return collisions
}

// ActiveBranches returns the set of branches that currently have a (non-bare)
// worktree on disk. It is the input to reconciling orphaned services: any
// branch still recorded in state but absent here has lost its worktree.
func ActiveBranches(trees []Worktree) map[string]bool {
	active := make(map[string]bool, len(trees))
	for _, t := range trees {
		if !t.IsBare {
			active[t.Branch] = true
		}
	}
	return active
}

// ListWorktrees returns all worktrees for the repo containing dir.
func ListWorktrees(dir string) ([]Worktree, error) {
	cmd := gitCmd(dir, "worktree", "list", "--porcelain")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git worktree list: %w", err)
	}
	return parsePorcelain(string(out))
}

// CurrentWorktree returns the worktree for the given directory.
func CurrentWorktree(dir string) (*Worktree, error) {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	// Resolve symlinks for proper path comparison (e.g., /tmp -> /private/tmp on macOS)
	absDir, err = filepath.EvalSymlinks(absDir)
	if err != nil {
		return nil, err
	}
	trees, err := ListWorktrees(dir)
	if err != nil {
		return nil, err
	}
	for _, t := range trees {
		if t.Path == absDir {
			return &t, nil
		}
	}
	// Try to match by checking if absDir is under a worktree path
	for _, t := range trees {
		rel, err := filepath.Rel(t.Path, absDir)
		if err == nil && !strings.HasPrefix(rel, "..") {
			return &t, nil
		}
	}
	return nil, fmt.Errorf("current directory %s is not a known worktree", absDir)
}

// parsePorcelain parses the porcelain output of `git worktree list --porcelain`.
// Format:
//
//	worktree /path/to/worktree
//	HEAD <sha>
//	branch refs/heads/<name>
//	<blank line>
func parsePorcelain(output string) ([]Worktree, error) {
	var trees []Worktree
	var current *Worktree
	scanner := bufio.NewScanner(strings.NewReader(output))

	for scanner.Scan() {
		line := scanner.Text()

		switch {
		case strings.HasPrefix(line, "worktree "):
			if current != nil {
				trees = append(trees, *current)
			}
			current = &Worktree{Path: strings.TrimPrefix(line, "worktree ")}

		case strings.HasPrefix(line, "HEAD "):
			if current != nil {
				current.Head = strings.TrimPrefix(line, "HEAD ")
			}

		case strings.HasPrefix(line, "branch "):
			if current != nil {
				ref := strings.TrimPrefix(line, "branch ")
				current.Branch = strings.TrimPrefix(ref, "refs/heads/")
			}

		case line == "bare":
			if current != nil {
				current.IsBare = true
			}

		case line == "detached":
			if current != nil && current.Branch == "" {
				if len(current.Head) >= 8 {
					current.Branch = current.Head[:8]
				} else {
					current.Branch = current.Head
				}
			}

		case line == "":
			// block separator
		}
	}
	if current != nil {
		trees = append(trees, *current)
	}
	return trees, scanner.Err()
}
