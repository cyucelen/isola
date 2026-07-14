package orca

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), FileName)
	if body != "" {
		if err := os.WriteFile(path, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func TestUpsertCreatesFile(t *testing.T) {
	path := write(t, "")
	changed, err := Upsert(path)
	if err != nil || !changed {
		t.Fatalf("Upsert: changed=%v err=%v", changed, err)
	}
	out, _ := os.ReadFile(path)
	s := string(out)
	if !strings.Contains(s, "scripts:") || !strings.Contains(s, "isola up") {
		t.Errorf("created orca.yaml missing scripts/setup:\n%s", s)
	}
	// Teardown is handled by reconcile, not an archive hook.
	if strings.Contains(s, "archive:") {
		t.Errorf("orca.yaml should not wire an archive hook:\n%s", s)
	}
}

func TestUpsertPreservesExistingSetupAndKeys(t *testing.T) {
	path := write(t, `scripts:
  setup: |
    pnpm install
other:
  keep: me
`)
	changed, err := Upsert(path)
	if err != nil || !changed {
		t.Fatalf("Upsert: changed=%v err=%v", changed, err)
	}
	out, _ := os.ReadFile(path)
	s := string(out)
	if !strings.Contains(s, "pnpm install") {
		t.Error("existing setup command must be preserved")
	}
	if !strings.Contains(s, "isola up") {
		t.Error("isola up must be appended")
	}
	if !strings.Contains(s, "keep: me") {
		t.Error("unrelated top-level keys must be preserved")
	}
	// isola up should come after the existing command.
	if strings.Index(s, "pnpm install") > strings.Index(s, "isola up") {
		t.Error("isola up should be appended after existing setup commands")
	}
}

func TestUpsertIsIdempotent(t *testing.T) {
	path := write(t, "")
	if _, err := Upsert(path); err != nil {
		t.Fatal(err)
	}
	changed, err := Upsert(path)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Error("second Upsert should be a no-op")
	}
	out, _ := os.ReadFile(path)
	if strings.Count(string(out), "isola up") != 1 {
		t.Errorf("isola up must appear once, got:\n%s", out)
	}
}

func TestUpsertRejectsNonMappingRoot(t *testing.T) {
	path := write(t, "- just\n- a\n- list\n")
	if _, err := Upsert(path); err == nil {
		t.Error("a non-mapping orca.yaml should be refused, not mangled")
	}
}
