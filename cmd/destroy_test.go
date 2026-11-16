package cmd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/cyucelen/isola/internal/accessory"
	"github.com/cyucelen/isola/internal/config"
	"github.com/cyucelen/isola/internal/git"
	"github.com/cyucelen/isola/internal/process"
	"github.com/cyucelen/isola/internal/state"
)

func init() {
	accessory.Register("cmdfake-faildrop", func(name string, dec accessory.Decoder) (accessory.Accessory, error) {
		return &cmdFakeFailDrop{name: name}, nil
	})
}

// cmdFakeFailDrop is an accessory whose Drop always fails, as a database server
// that is down would.
type cmdFakeFailDrop struct{ name string }

func (d *cmdFakeFailDrop) Name() string { return d.name }
func (d *cmdFakeFailDrop) Kind() string { return "cmdfake-faildrop" }
func (d *cmdFakeFailDrop) Provision(_ context.Context, wt accessory.WorktreeInfo) (accessory.Provisioned, error) {
	return accessory.Provisioned{Handle: map[string]string{"id": "res-" + wt.Slug}, URL: "fake://" + wt.Slug}, nil
}
func (d *cmdFakeFailDrop) Drop(context.Context, map[string]string) error {
	return errors.New("server unreachable")
}

// When the database fails to drop, destroy still cleans up the services'
// files and state, keeps the accessory record for a retry, and fails.
func TestDestroyKeepsAccessoryThatFailsToDrop(t *testing.T) {
	dir := setupGitRepo(t)
	body := `
[services.web]
command = "sleep 60"
port_range = { min = 19100, max = 19199 }
proxy_port = 3000

[accessories.db]
kind = "cmdfake-faildrop"
`
	if err := os.WriteFile(filepath.Join(dir, config.FileName), []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	tree, err := git.CurrentWorktree(dir)
	if err != nil {
		t.Fatal(err)
	}
	branch := tree.Branch

	store, err := state.NewFileStore(filepath.Join(dir, ".isola"))
	if err != nil {
		t.Fatal(err)
	}
	st, _ := store.Load()
	state.SetPortAssignment(st, branch, "web", 19100)
	state.SetAccessoryState(st, branch, "db", state.RunningAccessoryState("cmdfake-faildrop", map[string]string{"id": "res"}))
	if err := store.Save(st); err != nil {
		t.Fatal(err)
	}
	logPath := process.LogPath(store.Dir(), tree.HostLabel(), "web")
	for _, f := range []string{logPath, store.ServiceLockPath(branch, "web")} {
		if err := os.MkdirAll(filepath.Dir(f), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f, nil, 0600); err != nil {
			t.Fatal(err)
		}
	}

	resetRootCmd()
	rootCmd.SetArgs([]string{"destroy"})
	if err := rootCmd.Execute(); err == nil {
		t.Fatal("destroy should fail when an accessory fails to drop")
	}

	st, _ = store.Load()
	if state.GetAccessoryState(st, branch, "db") == nil {
		t.Error("the accessory that failed to drop must keep its record for a retry")
	}
	if state.GetPortAssignment(st, branch, "web") != 0 {
		t.Error("the service's port should still be released")
	}
	if _, err := os.Stat(logPath); !os.IsNotExist(err) {
		t.Error("the service's log should still be deleted")
	}
	if _, err := os.Stat(store.ServiceLockPath(branch, "web")); !os.IsNotExist(err) {
		t.Error("the service's lock file should still be deleted")
	}
}
