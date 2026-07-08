package copyfiles

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestSyncCopiesMissingFiles(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	write(t, filepath.Join(src, ".env"), "A=1")
	write(t, filepath.Join(src, "config/local.yml"), "k: v")
	write(t, filepath.Join(src, "ignored.txt"), "x")

	copied, err := Sync([]string{".env", "config/local.yml"}, src, dst)
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	sort.Strings(copied)
	want := []string{".env", filepath.FromSlash("config/local.yml")}
	if len(copied) != 2 || copied[0] != want[0] || copied[1] != want[1] {
		t.Fatalf("copied = %v, want %v", copied, want)
	}
	if b, _ := os.ReadFile(filepath.Join(dst, ".env")); string(b) != "A=1" {
		t.Errorf(".env content = %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(dst, "config/local.yml")); string(b) != "k: v" {
		t.Errorf("nested content = %q", b)
	}
	if _, err := os.Stat(filepath.Join(dst, "ignored.txt")); err == nil {
		t.Error("unmatched file should not be copied")
	}
}

func TestSyncGlob(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	write(t, filepath.Join(src, ".env"), "A=1")
	write(t, filepath.Join(src, ".env.local"), "B=2")
	write(t, filepath.Join(src, ".env.production"), "C=3")

	copied, err := Sync([]string{".env.*"}, src, dst)
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if len(copied) != 2 { // .env.local, .env.production (not bare .env)
		t.Fatalf("glob copied = %v, want 2", copied)
	}
	if _, err := os.Stat(filepath.Join(dst, ".env")); err == nil {
		t.Error("bare .env should not match .env.*")
	}
}

func TestSyncNeverOverwrites(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	write(t, filepath.Join(src, ".env"), "SRC")
	write(t, filepath.Join(dst, ".env"), "LOCAL")

	copied, err := Sync([]string{".env"}, src, dst)
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if len(copied) != 0 {
		t.Errorf("should not copy over an existing file, copied=%v", copied)
	}
	if b, _ := os.ReadFile(filepath.Join(dst, ".env")); string(b) != "LOCAL" {
		t.Errorf("existing file was clobbered: %q", b)
	}
}

func TestSyncSameRootIsNoop(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, ".env"), "A=1")
	copied, err := Sync([]string{".env"}, root, root)
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if copied != nil {
		t.Errorf("same-root sync should be a no-op, got %v", copied)
	}
}

func TestSyncSkipsDirsAndEscapes(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	write(t, filepath.Join(src, "config/local.yml"), "k: v")   // dir "config" would match "config"
	write(t, filepath.Join(filepath.Dir(src), "outside"), "x") // sibling of src

	// A directory match is skipped.
	if copied, err := Sync([]string{"config"}, src, dst); err != nil || len(copied) != 0 {
		t.Errorf("dir match: copied=%v err=%v", copied, err)
	}
	// An escaping pattern copies nothing into dst.
	if copied, err := Sync([]string{"../outside"}, src, dst); err != nil || len(copied) != 0 {
		t.Errorf("escape match: copied=%v err=%v", copied, err)
	}
	if _, err := os.Stat(filepath.Join(dst, "outside")); err == nil {
		t.Error("escaping match must not be copied")
	}
}

func TestUpsertEnvReplacesAndAppends(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, ".env")
	write(t, p, "# db config\nDATABASE_URL=postgres://main/db\nNODE_ENV=development\n")

	changed, err := UpsertEnv(p, map[string]string{
		"DATABASE_URL": "postgres://iso/feature", // existing -> replaced
		"REDIS_URL":    "redis://localhost:6379/2", // absent -> appended
	})
	if err != nil {
		t.Fatalf("UpsertEnv: %v", err)
	}
	if len(changed) != 2 {
		t.Errorf("changed = %v, want 2", changed)
	}
	got, _ := os.ReadFile(p)
	want := "# db config\nDATABASE_URL=postgres://iso/feature\nNODE_ENV=development\nREDIS_URL=redis://localhost:6379/2\n"
	if string(got) != want {
		t.Errorf("result:\n%q\nwant:\n%q", got, want)
	}
}

func TestUpsertEnvNoFileIsNoop(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, ".env")
	changed, err := UpsertEnv(p, map[string]string{"DATABASE_URL": "x"})
	if err != nil {
		t.Fatalf("UpsertEnv: %v", err)
	}
	if changed != nil {
		t.Errorf("changed = %v, want nil", changed)
	}
	if _, err := os.Stat(p); err == nil {
		t.Error("UpsertEnv must not create a .env file")
	}
}

func TestUpsertEnvPreservesExportAndIgnoresComments(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, ".env")
	write(t, p, "# DATABASE_URL=commented-out\nexport DATABASE_URL=old\n")

	if _, err := UpsertEnv(p, map[string]string{"DATABASE_URL": "new"}); err != nil {
		t.Fatalf("UpsertEnv: %v", err)
	}
	got, _ := os.ReadFile(p)
	want := "# DATABASE_URL=commented-out\nexport DATABASE_URL=new\n"
	if string(got) != want {
		t.Errorf("result:\n%q\nwant:\n%q", got, want)
	}
}
