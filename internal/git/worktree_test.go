package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestBranchSlug(t *testing.T) {
	tests := []struct {
		name   string
		branch string
		want   string
	}{
		{"slash to dash", "feature/auth", "feature-auth"},
		{"underscore to dash", "feature_auth", "feature-auth"},
		{"uppercase to lower", "Feature/Auth", "feature-auth"},
		{"dots to dash", "release.1.0", "release-1-0"},
		{"empty string", "", ""},
		{"already clean", "main", "main"},
		{"multiple special chars", "feature//auth__v2", "feature-auth-v2"},
		{"leading trailing special", "/feature/", "feature"},
		{"only special chars", "///", ""},
		{"mixed separators", "feat.ui/login_page", "feat-ui-login-page"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BranchSlug(tt.branch)
			if got != tt.want {
				t.Errorf("BranchSlug(%q) = %q, want %q", tt.branch, got, tt.want)
			}
		})
	}
}

func TestWorktreeHostLabel(t *testing.T) {
	wt := &Worktree{Branch: "feature/auth"}
	got := wt.HostLabel()
	want := "feature-auth"
	if got != want {
		t.Errorf("Worktree.HostLabel() = %q, want %q", got, want)
	}

	// A label within the limit is the branch slug unchanged.
	if got != BranchSlug(wt.Branch) {
		t.Errorf("Worktree.HostLabel() != BranchSlug(branch)")
	}
}

func TestDetectHostLabelCollisions(t *testing.T) {
	t.Run("no collisions", func(t *testing.T) {
		trees := []Worktree{
			{Path: "/a", Branch: "main"},
			{Path: "/b", Branch: "feature/auth"},
		}
		got := DetectHostLabelCollisions(trees)
		if len(got) != 0 {
			t.Errorf("DetectHostLabelCollisions() = %v, want empty", got)
		}
	})

	t.Run("collision", func(t *testing.T) {
		trees := []Worktree{
			{Path: "/a", Branch: "feature/auth"},
			{Path: "/b", Branch: "feature-auth"},
		}
		got := DetectHostLabelCollisions(trees)
		if len(got) != 1 {
			t.Fatalf("DetectHostLabelCollisions() returned %d collisions, want 1", len(got))
		}
		branches, ok := got["feature-auth"]
		if !ok {
			t.Fatal("expected collision for slug 'feature-auth'")
		}
		if len(branches) != 2 {
			t.Errorf("collision has %d branches, want 2", len(branches))
		}
	})

	t.Run("bare worktrees skipped", func(t *testing.T) {
		trees := []Worktree{
			{Path: "/a", Branch: "main", IsBare: true},
			{Path: "/b", Branch: "main"},
		}
		got := DetectHostLabelCollisions(trees)
		if len(got) != 0 {
			t.Errorf("DetectHostLabelCollisions() = %v, want empty (bare should be skipped)", got)
		}
	})

	t.Run("empty input", func(t *testing.T) {
		got := DetectHostLabelCollisions(nil)
		if len(got) != 0 {
			t.Errorf("DetectHostLabelCollisions(nil) = %v, want empty", got)
		}
	})
}

// runGitIn runs a git command in dir with a deterministic author/committer
// identity. It reuses sanitizedGitEnv so the command targets dir's repo even
// when the suite runs inside a git hook that exports GIT_DIR/GIT_INDEX_FILE.
func runGitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(SanitizedEnv(),
		"GIT_AUTHOR_NAME=test",
		"GIT_AUTHOR_EMAIL=test@test.com",
		"GIT_COMMITTER_NAME=test",
		"GIT_COMMITTER_EMAIL=test@test.com",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// initTestRepo creates a temporary git repo with an initial commit.
func initTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runGitIn(t, dir, "init")
	runGitIn(t, dir, "commit", "--allow-empty", "-m", "init")
	return dir
}

func TestFindRepoRoot(t *testing.T) {
	t.Run("from repo root", func(t *testing.T) {
		dir := initTestRepo(t)
		root, err := FindRepoRoot(dir)
		if err != nil {
			t.Fatalf("FindRepoRoot() error: %v", err)
		}
		// Resolve symlinks for macOS /private/var/folders vs /var/folders
		wantAbs, _ := filepath.EvalSymlinks(dir)
		gotAbs, _ := filepath.EvalSymlinks(root)
		if gotAbs != wantAbs {
			t.Errorf("FindRepoRoot() = %q, want %q", gotAbs, wantAbs)
		}
	})

	t.Run("from subdirectory", func(t *testing.T) {
		dir := initTestRepo(t)
		sub := filepath.Join(dir, "sub", "deep")
		if err := os.MkdirAll(sub, 0755); err != nil {
			t.Fatal(err)
		}
		root, err := FindRepoRoot(sub)
		if err != nil {
			t.Fatalf("FindRepoRoot() error: %v", err)
		}
		wantAbs, _ := filepath.EvalSymlinks(dir)
		gotAbs, _ := filepath.EvalSymlinks(root)
		if gotAbs != wantAbs {
			t.Errorf("FindRepoRoot() = %q, want %q", gotAbs, wantAbs)
		}
	})

	t.Run("not a git repo", func(t *testing.T) {
		dir := t.TempDir()
		_, err := FindRepoRoot(dir)
		if err == nil {
			t.Error("FindRepoRoot() should error for non-git directory")
		}
	})
}

func TestCommonDir(t *testing.T) {
	dir := initTestRepo(t)

	common, err := CommonDir(dir)
	if err != nil {
		t.Fatalf("CommonDir() error: %v", err)
	}

	// For a regular repo, CommonDir should point to .git
	wantAbs, _ := filepath.EvalSymlinks(filepath.Join(dir, ".git"))
	gotAbs, _ := filepath.EvalSymlinks(common)
	if gotAbs != wantAbs {
		t.Errorf("CommonDir() = %q, want %q", gotAbs, wantAbs)
	}
}

func TestMainWorktreeRoot(t *testing.T) {
	dir := initTestRepo(t)

	root, err := MainWorktreeRoot(dir)
	if err != nil {
		t.Fatalf("MainWorktreeRoot() error: %v", err)
	}

	wantAbs, _ := filepath.EvalSymlinks(dir)
	gotAbs, _ := filepath.EvalSymlinks(root)
	if gotAbs != wantAbs {
		t.Errorf("MainWorktreeRoot() = %q, want %q", gotAbs, wantAbs)
	}
}

func TestListWorktrees(t *testing.T) {
	dir := initTestRepo(t)

	trees, err := ListWorktrees(dir)
	if err != nil {
		t.Fatalf("ListWorktrees() error: %v", err)
	}

	if len(trees) == 0 {
		t.Fatal("ListWorktrees() returned 0 worktrees")
	}

	// At least the main worktree should be present
	found := false
	for _, tree := range trees {
		if tree.Branch == "main" || tree.Branch == "master" {
			found = true
			break
		}
	}
	if !found {
		t.Error("ListWorktrees() should contain main/master branch")
	}
}

func TestListWorktrees_WithAdditionalWorktree(t *testing.T) {
	dir := initTestRepo(t)

	// Create a branch and worktree
	wtDir := filepath.Join(t.TempDir(), "feature-auth")
	runGitIn(t, dir, "worktree", "add", "-b", "feature/auth", wtDir)

	trees, err := ListWorktrees(dir)
	if err != nil {
		t.Fatalf("ListWorktrees() error: %v", err)
	}

	if len(trees) < 2 {
		t.Fatalf("ListWorktrees() returned %d worktrees, want >= 2", len(trees))
	}

	// Check the additional worktree
	found := false
	for _, tree := range trees {
		if tree.Branch == "feature/auth" {
			found = true
			wantAbs, _ := filepath.EvalSymlinks(wtDir)
			gotAbs, _ := filepath.EvalSymlinks(tree.Path)
			if gotAbs != wantAbs {
				t.Errorf("worktree path = %q, want %q", gotAbs, wantAbs)
			}
			break
		}
	}
	if !found {
		t.Error("ListWorktrees() should contain feature/auth branch")
	}
}

func TestCurrentWorktree(t *testing.T) {
	dir := initTestRepo(t)
	// Resolve symlinks (macOS /var/folders → /private/var/folders)
	dir, _ = filepath.EvalSymlinks(dir)

	tree, err := CurrentWorktree(dir)
	if err != nil {
		t.Fatalf("CurrentWorktree() error: %v", err)
	}

	if tree.Branch != "main" && tree.Branch != "master" {
		t.Errorf("CurrentWorktree().Branch = %q, want main or master", tree.Branch)
	}
}

func TestCurrentWorktree_FromSubdirectory(t *testing.T) {
	dir := initTestRepo(t)
	dir, _ = filepath.EvalSymlinks(dir)
	sub := filepath.Join(dir, "subdir")
	if err := os.MkdirAll(sub, 0755); err != nil {
		t.Fatal(err)
	}

	tree, err := CurrentWorktree(sub)
	if err != nil {
		t.Fatalf("CurrentWorktree() from subdir error: %v", err)
	}

	if tree.Branch != "main" && tree.Branch != "master" {
		t.Errorf("CurrentWorktree().Branch = %q, want main or master", tree.Branch)
	}
}

func TestCurrentWorktree_NotGitRepo(t *testing.T) {
	dir := t.TempDir()
	_, err := CurrentWorktree(dir)
	if err == nil {
		t.Error("CurrentWorktree() should error for non-git directory")
	}
}

func TestCommonDir_FromWorktree(t *testing.T) {
	dir := initTestRepo(t)

	wtDir := filepath.Join(t.TempDir(), "wt-test")
	runGitIn(t, dir, "worktree", "add", "-b", "test-branch", wtDir)

	// CommonDir from the additional worktree should point to main's .git
	common, err := CommonDir(wtDir)
	if err != nil {
		t.Fatalf("CommonDir() from worktree error: %v", err)
	}

	mainGit, _ := filepath.EvalSymlinks(filepath.Join(dir, ".git"))
	gotAbs, _ := filepath.EvalSymlinks(common)
	if gotAbs != mainGit {
		t.Errorf("CommonDir() from worktree = %q, want %q", gotAbs, mainGit)
	}
}

func TestMainWorktreeRoot_FromWorktree(t *testing.T) {
	dir := initTestRepo(t)

	wtDir := filepath.Join(t.TempDir(), "wt-test2")
	runGitIn(t, dir, "worktree", "add", "-b", "test-branch2", wtDir)

	root, err := MainWorktreeRoot(wtDir)
	if err != nil {
		t.Fatalf("MainWorktreeRoot() from worktree error: %v", err)
	}

	wantAbs, _ := filepath.EvalSymlinks(dir)
	gotAbs, _ := filepath.EvalSymlinks(root)
	if gotAbs != wantAbs {
		t.Errorf("MainWorktreeRoot() from worktree = %q, want %q", gotAbs, wantAbs)
	}
}

// TestTwoRootSplit_FromWorktree verifies the split that fixes shared state:
// from inside a linked worktree, the .isola state dir must resolve to the
// MAIN worktree root (shared across worktrees), while config loading and a
// service's working directory must stay anchored to the CURRENT worktree.
func TestTwoRootSplit_FromWorktree(t *testing.T) {
	mainDir := initTestRepo(t)
	mainDir, _ = filepath.EvalSymlinks(mainDir)

	wtDir := filepath.Join(t.TempDir(), "feature-auth")
	runGitIn(t, mainDir, "worktree", "add", "-b", "feature/auth", wtDir)
	wtDir, _ = filepath.EvalSymlinks(wtDir)

	// (a) State dir resolves to the MAIN worktree root, not the linked one.
	stateRoot, err := MainWorktreeRoot(wtDir)
	if err != nil {
		t.Fatalf("MainWorktreeRoot() error: %v", err)
	}
	stateRoot, _ = filepath.EvalSymlinks(stateRoot)
	if stateRoot != mainDir {
		t.Errorf("state root = %q, want main worktree %q", stateRoot, mainDir)
	}
	wantStateDir := filepath.Join(mainDir, ".isola")
	if got := filepath.Join(stateRoot, ".isola"); got != wantStateDir {
		t.Errorf("state dir = %q, want %q", got, wantStateDir)
	}

	// (b) Config/exec root stays anchored to the CURRENT (linked) worktree.
	repoRoot, err := FindRepoRoot(wtDir)
	if err != nil {
		t.Fatalf("FindRepoRoot() error: %v", err)
	}
	repoRoot, _ = filepath.EvalSymlinks(repoRoot)
	if repoRoot != wtDir {
		t.Errorf("repo root = %q, want current worktree %q", repoRoot, wtDir)
	}

	// A service's working dir is built from the current worktree path
	// (tree.Path + svc.Dir); it must land under the linked worktree, not main.
	tree, err := CurrentWorktree(wtDir)
	if err != nil {
		t.Fatalf("CurrentWorktree() error: %v", err)
	}
	treePath, _ := filepath.EvalSymlinks(tree.Path)
	serviceDir := filepath.Join(treePath, "web")
	if !strings.HasPrefix(serviceDir, wtDir+string(filepath.Separator)) {
		t.Errorf("service dir %q should resolve under current worktree %q", serviceDir, wtDir)
	}
	if strings.HasPrefix(serviceDir, mainDir+string(filepath.Separator)) {
		t.Errorf("service dir %q must NOT resolve under main worktree %q", serviceDir, mainDir)
	}

	// The two roots must genuinely differ for a linked worktree.
	if stateRoot == repoRoot {
		t.Errorf("state root and repo root should differ from a linked worktree; both = %q", stateRoot)
	}
}

func TestParsePorcelain(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    []Worktree
		wantErr bool
	}{
		{
			name: "single worktree",
			input: `worktree /home/user/project
HEAD abc1234567890123456789012345678901234567
branch refs/heads/main

`,
			want: []Worktree{
				{Path: "/home/user/project", Head: "abc1234567890123456789012345678901234567", Branch: "main"},
			},
		},
		{
			name: "two worktrees",
			input: `worktree /home/user/project
HEAD abc1234567890123456789012345678901234567
branch refs/heads/main

worktree /home/user/project-feature
HEAD def1234567890123456789012345678901234567
branch refs/heads/feature/auth

`,
			want: []Worktree{
				{Path: "/home/user/project", Head: "abc1234567890123456789012345678901234567", Branch: "main"},
				{Path: "/home/user/project-feature", Head: "def1234567890123456789012345678901234567", Branch: "feature/auth"},
			},
		},
		{
			name: "bare worktree",
			input: `worktree /home/user/project.git
HEAD abc1234567890123456789012345678901234567
branch refs/heads/main
bare

`,
			want: []Worktree{
				{Path: "/home/user/project.git", Head: "abc1234567890123456789012345678901234567", Branch: "main", IsBare: true},
			},
		},
		{
			name: "detached head",
			input: `worktree /home/user/project
HEAD abc12345abcdef01234567890123456789012345
detached

`,
			want: []Worktree{
				{Path: "/home/user/project", Head: "abc12345abcdef01234567890123456789012345", Branch: "abc12345"},
			},
		},
		{
			name:  "empty input",
			input: "",
			want:  nil,
		},
		{
			name: "branch stripping refs/heads/",
			input: `worktree /tmp/wt
HEAD aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
branch refs/heads/release/v2.0

`,
			want: []Worktree{
				{Path: "/tmp/wt", Head: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Branch: "release/v2.0"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parsePorcelain(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parsePorcelain() error = %v, wantErr %v", err, tt.wantErr)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("parsePorcelain() returned %d worktrees, want %d", len(got), len(tt.want))
			}
			for i := range got {
				if got[i].Path != tt.want[i].Path {
					t.Errorf("worktree[%d].Path = %q, want %q", i, got[i].Path, tt.want[i].Path)
				}
				if got[i].Branch != tt.want[i].Branch {
					t.Errorf("worktree[%d].Branch = %q, want %q", i, got[i].Branch, tt.want[i].Branch)
				}
				if got[i].Head != tt.want[i].Head {
					t.Errorf("worktree[%d].Head = %q, want %q", i, got[i].Head, tt.want[i].Head)
				}
				if got[i].IsBare != tt.want[i].IsBare {
					t.Errorf("worktree[%d].IsBare = %v, want %v", i, got[i].IsBare, tt.want[i].IsBare)
				}
			}
		})
	}
}

// longBranch is the shape that motivated fitting the host label: an automated
// dependency branch whose slug is 70 bytes, over the 63-byte DNS label limit.
const longBranch = "dependabot/npm_and_yarn/services/manager-dashboard/ai-sdk/react-4.0.40"

func TestHostLabelFitsTheDNSLabelLimit(t *testing.T) {
	raw := BranchSlug(longBranch)
	if len(raw) <= 63 {
		t.Fatalf("fixture slug is %d bytes; the test needs one over 63", len(raw))
	}

	label := HostLabel(longBranch)
	if len(label) > 63 {
		t.Errorf("HostLabel(%q) = %q (%d bytes), over the 63-byte DNS label limit", longBranch, label, len(label))
	}
	// Still recognizable as this worktree in a URL bar.
	if !strings.HasPrefix(label, "dependabot-") {
		t.Errorf("HostLabel = %q, want a readable prefix", label)
	}
	// Legal as a DNS label: lowercase alphanumerics and hyphens, no edge hyphen.
	if label[0] == '-' || label[len(label)-1] == '-' {
		t.Errorf("HostLabel = %q, must not start or end with a hyphen", label)
	}
	legal := func(c byte) bool {
		return c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-'
	}
	for i := 0; i < len(label); i++ {
		if !legal(label[i]) {
			t.Errorf("HostLabel = %q contains %q, not legal in a DNS label", label, label[i])
		}
	}
}

func TestHostLabelLeavesShortBranchesAlone(t *testing.T) {
	// Every worktree that works today keeps the exact URL it has.
	for _, branch := range []string{"main", "feature/auth", "release.1.0"} {
		if got, want := HostLabel(branch), BranchSlug(branch); got != want {
			t.Errorf("HostLabel(%q) = %q, want %q unchanged", branch, got, want)
		}
	}
}

// TestHostLabelKeepsLongBranchesDistinct is the collision regression: truncating
// to 63 would map two automated bumps of the same package onto one label, and the
// proxy would route both worktrees to whichever it found first.
func TestHostLabelKeepsLongBranchesDistinct(t *testing.T) {
	const prefix = "dependabot/npm_and_yarn/services/manager-dashboard/ai-sdk/react-4.0.4"
	a, b := HostLabel(prefix+"0"), HostLabel(prefix+"1")
	if BranchSlug(prefix + "0")[:63] != BranchSlug(prefix + "1")[:63] {
		t.Fatal("fixtures must agree for their first 63 bytes")
	}
	if a == b {
		t.Errorf("two branches share the host label %q", a)
	}
}
