package cmd

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cyucelen/isola/internal/accessory"
	"github.com/cyucelen/isola/internal/config"
	"github.com/cyucelen/isola/internal/state"
)

// cmdFakeDropped records the database handles dropped by the fake driver so
// tests can assert teardown behavior. Reset it at the start of each test.
var cmdFakeDropped []string

func init() {
	accessory.Register("cmdfake", func(name string, dec accessory.Decoder) (accessory.Accessory, error) {
		return &cmdFakeDriver{name: name}, nil
	})
	// A kind with no Reset method — not Resettable.
	accessory.Register("cmdfake-noreset", func(name string, dec accessory.Decoder) (accessory.Accessory, error) {
		return &cmdFakeNoReset{name: name}, nil
	})
}

type cmdFakeDriver struct{ name string }

func (d *cmdFakeDriver) Name() string { return d.name }
func (d *cmdFakeDriver) Kind() string { return "cmdfake" }
func (d *cmdFakeDriver) Provision(_ context.Context, wt accessory.WorktreeInfo) (accessory.Provisioned, error) {
	return accessory.Provisioned{
		Handle: map[string]string{"id": "res-" + wt.Slug},
		Env:    map[string]string{"DATABASE_URL": "fake://" + wt.Slug},
	}, nil
}
func (d *cmdFakeDriver) Reset(ctx context.Context, wt accessory.WorktreeInfo) (accessory.Provisioned, error) {
	return d.Provision(ctx, wt)
}
func (d *cmdFakeDriver) Drop(_ context.Context, handle map[string]string) error {
	cmdFakeDropped = append(cmdFakeDropped, handle["id"])
	return nil
}

type cmdFakeNoReset struct{ name string }

func (d *cmdFakeNoReset) Name() string { return d.name }
func (d *cmdFakeNoReset) Kind() string { return "cmdfake-noreset" }
func (d *cmdFakeNoReset) Provision(_ context.Context, wt accessory.WorktreeInfo) (accessory.Provisioned, error) {
	return accessory.Provisioned{Handle: map[string]string{"id": "x"}, Env: map[string]string{}}, nil
}
func (d *cmdFakeNoReset) Drop(_ context.Context, handle map[string]string) error { return nil }

const fakeAccessoryConfig = `
[services.web]
command = "sleep 60"
port_range = { min = 19100, max = 19199 }
proxy_port = 3000

[accessories.db]
kind = "cmdfake"
`

func TestDownPruneDropsOrphanedAccessories(t *testing.T) {
	dir := setupGitRepo(t)
	if err := os.WriteFile(filepath.Join(dir, config.FileName), []byte(fakeAccessoryConfig), 0644); err != nil {
		t.Fatal(err)
	}

	// Seed state with an orphaned branch (no matching worktree) that owns an
	// accessory resource.
	store, err := state.NewFileStore(filepath.Join(dir, ".isola"))
	if err != nil {
		t.Fatal(err)
	}
	st, _ := store.Load()
	state.SetAccessoryState(st, "ghost", "db", state.RunningAccessoryState("cmdfake", map[string]string{"id": "res-ghost"}))
	if err := store.Save(st); err != nil {
		t.Fatal(err)
	}

	cmdFakeDropped = nil
	resetRootCmd()
	rootCmd.SetArgs([]string{"down", "--prune"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("down --prune: %v", err)
	}

	if len(cmdFakeDropped) != 1 || cmdFakeDropped[0] != "res-ghost" {
		t.Errorf("dropped = %v, want [res-ghost]", cmdFakeDropped)
	}

	reloaded, _ := store.Load()
	if state.GetAccessoryState(reloaded, "ghost", "db") != nil {
		t.Error("accessory record should be removed after successful prune")
	}
}

func TestAccessoryProvisionAndDrop(t *testing.T) {
	dir := setupGitRepo(t)
	if err := os.WriteFile(filepath.Join(dir, config.FileName), []byte(fakeAccessoryConfig), 0644); err != nil {
		t.Fatal(err)
	}

	cmdFakeDropped = nil
	resetRootCmd()
	rootCmd.SetArgs([]string{"accessory", "provision"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("accessory provision: %v", err)
	}

	// The current (main) worktree's accessory should be recorded.
	store, _ := state.NewFileStore(filepath.Join(dir, ".isola"))
	st, _ := store.Load()
	var branch string
	for b := range st.Accessories {
		branch = b
	}
	if branch == "" || state.GetAccessoryState(st, branch, "db") == nil {
		t.Fatalf("provision did not record accessory state: %+v", st.Accessories)
	}

	resetRootCmd()
	rootCmd.SetArgs([]string{"accessory", "drop"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("accessory drop: %v", err)
	}
	if len(cmdFakeDropped) != 1 {
		t.Errorf("expected one drop, got %v", cmdFakeDropped)
	}
	reloaded, _ := store.Load()
	if state.GetAccessoryState(reloaded, branch, "db") != nil {
		t.Error("accessory drop should clear the accessory record")
	}
}

func TestAccessoryResetUnsupportedKind(t *testing.T) {
	dir := setupGitRepo(t)
	cfgToml := `
[services.web]
command = "sleep 60"
port_range = { min = 19100, max = 19199 }
proxy_port = 3000

[accessories.cache]
kind = "cmdfake-noreset"
`
	if err := os.WriteFile(filepath.Join(dir, config.FileName), []byte(cfgToml), 0644); err != nil {
		t.Fatal(err)
	}

	resetRootCmd()
	rootCmd.SetArgs([]string{"accessory", "reset"})
	err := rootCmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "does not support reset") {
		t.Fatalf("reset on non-resettable kind: err = %v, want 'does not support reset'", err)
	}
}
