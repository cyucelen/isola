package hooks

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestHookScriptBehavior runs the generated hook under /bin/sh with a fake
// `isola` on PATH, checking that it fires only for a new worktree, honors
// ISOLA_NO_UP, and no-ops without a .isola.toml.
func TestHookScriptBehavior(t *testing.T) {
	const nullSHA = "0000000000000000000000000000000000000000"
	const realSHA = "235bf011055a1e2787d485a4dc91566bf2cea03e"

	hookDir := t.TempDir()
	if _, err := Install(hookDir); err != nil {
		t.Fatal(err)
	}
	hook := filepath.Join(hookDir, HookName)

	binDir := t.TempDir()
	marker := filepath.Join(binDir, "ran")
	fakeIsola := "#!/bin/sh\necho ran >> " + marker + "\n"
	if err := os.WriteFile(filepath.Join(binDir, "isola"), []byte(fakeIsola), 0755); err != nil {
		t.Fatal(err)
	}

	// fired runs the hook in workdir with old/new/flag and extra env, and
	// reports whether the fake isola was invoked.
	fired := func(workdir string, extraEnv []string, old, newRef, flag string) bool {
		_ = os.Remove(marker)
		cmd := exec.Command("/bin/sh", hook, old, newRef, flag)
		cmd.Dir = workdir
		cmd.Env = append(os.Environ(), append([]string{"PATH=" + binDir + ":" + os.Getenv("PATH")}, extraEnv...)...)
		if err := cmd.Run(); err != nil {
			t.Fatalf("hook run: %v", err)
		}
		_, err := os.Stat(marker)
		return err == nil
	}

	configured := t.TempDir()
	if err := os.WriteFile(filepath.Join(configured, ".isola.toml"), []byte("# test\n"), 0644); err != nil {
		t.Fatal(err)
	}
	bare := t.TempDir() // no .isola.toml

	// Orca configured to run isola -> the hook must stand down (avoid double up).
	orcaWired := t.TempDir()
	_ = os.WriteFile(filepath.Join(orcaWired, ".isola.toml"), []byte("# test\n"), 0644)
	_ = os.WriteFile(filepath.Join(orcaWired, "orca.yaml"), []byte("scripts:\n  setup: |\n    isola up\n"), 0644)

	// orca.yaml present but NOT wired for isola -> the hook still runs.
	orcaUnrelated := t.TempDir()
	_ = os.WriteFile(filepath.Join(orcaUnrelated, ".isola.toml"), []byte("# test\n"), 0644)
	_ = os.WriteFile(filepath.Join(orcaUnrelated, "orca.yaml"), []byte("scripts:\n  setup: |\n    pnpm install\n"), 0644)

	cases := []struct {
		name    string
		dir     string
		env     []string
		old     string
		flag    string
		wantRun bool
	}{
		{"new worktree in configured repo", configured, nil, nullSHA, "1", true},
		{"branch switch is skipped", configured, nil, realSHA, "1", false},
		{"file checkout (flag 0) is skipped", configured, nil, nullSHA, "0", false},
		{"ISOLA_NO_UP opts out", configured, []string{"ISOLA_NO_UP=1"}, nullSHA, "1", false},
		{"no .isola.toml is a no-op", bare, nil, nullSHA, "1", false},
		{"orca.yaml wires isola -> hook stands down", orcaWired, nil, nullSHA, "1", false},
		{"orca.yaml unrelated -> hook still runs", orcaUnrelated, nil, nullSHA, "1", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := fired(c.dir, c.env, c.old, realSHA, c.flag); got != c.wantRun {
				t.Errorf("hook fired=%v, want %v", got, c.wantRun)
			}
		})
	}
}

func TestInstallCreatesExecutableHook(t *testing.T) {
	dir := t.TempDir()
	changed, err := Install(dir)
	if err != nil || !changed {
		t.Fatalf("Install: changed=%v err=%v", changed, err)
	}
	path := filepath.Join(dir, HookName)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0100 == 0 {
		t.Errorf("hook not executable: %v", info.Mode())
	}
	body, _ := os.ReadFile(path)
	if !strings.HasPrefix(string(body), shebang) || !strings.Contains(string(body), "isola up") {
		t.Errorf("hook body unexpected:\n%s", body)
	}
	if !Installed(dir) {
		t.Error("Installed should be true after Install")
	}
}

func TestInstallIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	if _, err := Install(dir); err != nil {
		t.Fatal(err)
	}
	changed, err := Install(dir)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Error("second Install should be a no-op")
	}
	body, _ := os.ReadFile(filepath.Join(dir, HookName))
	if strings.Count(string(body), beginMark) != 1 {
		t.Errorf("block must appear exactly once, got:\n%s", body)
	}
}

func TestInstallChainsOntoExistingHook(t *testing.T) {
	dir := t.TempDir()
	existing := "#!/bin/sh\necho existing-hook\n"
	if err := os.WriteFile(filepath.Join(dir, HookName), []byte(existing), 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(dir); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(filepath.Join(dir, HookName))
	if !strings.Contains(string(body), "echo existing-hook") {
		t.Error("existing hook content must be preserved")
	}
	if !strings.Contains(string(body), beginMark) {
		t.Error("isola block must be appended")
	}
}

func TestUninstallRemovesBlockButKeepsOtherContent(t *testing.T) {
	dir := t.TempDir()
	existing := "#!/bin/sh\necho existing-hook\n"
	if err := os.WriteFile(filepath.Join(dir, HookName), []byte(existing), 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(dir); err != nil {
		t.Fatal(err)
	}
	changed, err := Uninstall(dir)
	if err != nil || !changed {
		t.Fatalf("Uninstall: changed=%v err=%v", changed, err)
	}
	body, _ := os.ReadFile(filepath.Join(dir, HookName))
	if strings.Contains(string(body), beginMark) {
		t.Error("isola block should be gone")
	}
	if !strings.Contains(string(body), "echo existing-hook") {
		t.Error("the pre-existing hook must survive uninstall")
	}
}

func TestUninstallDeletesFileItSolelyOwned(t *testing.T) {
	dir := t.TempDir()
	if _, err := Install(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := Uninstall(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, HookName)); !os.IsNotExist(err) {
		t.Error("a hook isola created alone should be removed on uninstall")
	}
}

func TestUninstallNoHookIsNoOp(t *testing.T) {
	dir := t.TempDir()
	changed, err := Uninstall(dir)
	if err != nil || changed {
		t.Errorf("uninstall with no hook: changed=%v err=%v", changed, err)
	}
}
