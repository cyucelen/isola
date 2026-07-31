package cmd

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cyucelen/isola/internal/accessory"
	"github.com/cyucelen/isola/internal/config"
	"github.com/cyucelen/isola/internal/git"
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
		URL:    "fake://" + wt.Slug,
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
	return accessory.Provisioned{Handle: map[string]string{"id": "x"}, URL: "fake://x"}, nil
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
	rootCmd.SetArgs([]string{"accessory", "up"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("accessory up: %v", err)
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

// jsonAccessory mirrors one `accessory ls --json` row. Resource is decoded into a
// generic map so the test can assert JSON *types* (a Redis logical database must
// arrive as a number, not a string) rather than only field names.
type jsonAccessory struct {
	Worktree    string         `json:"worktree"`
	Accessory   string         `json:"accessory"`
	Kind        string         `json:"kind"`
	Provisioned bool           `json:"provisioned"`
	Resource    map[string]any `json:"resource"`
}

// accessoryLsJSON runs `isola accessory ls --json` and returns the raw stdout
// alongside the decoded rows, so a test can assert both shape and encoding.
func accessoryLsJSON(t *testing.T) (string, []jsonAccessory) {
	t.Helper()
	resetRootCmd()
	rootCmd.SetArgs([]string{"accessory", "ls", "--json"})
	var execErr error
	out := captureStdout(t, func() { execErr = rootCmd.Execute() })
	if execErr != nil {
		t.Fatalf("accessory ls --json: %v", execErr)
	}
	var rows []jsonAccessory
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("output is not a JSON array: %v\n%s", err, out)
	}
	return out, rows
}

// seedAccessory records a provisioned accessory for a branch, as `up` would.
func seedAccessory(t *testing.T, repoDir, branch, name, kind string, handle map[string]string) {
	t.Helper()
	store, err := state.NewFileStore(filepath.Join(repoDir, ".isola"))
	if err != nil {
		t.Fatal(err)
	}
	st, _ := store.Load()
	state.SetAccessoryState(st, branch, name, state.RunningAccessoryState(kind, handle))
	if err := store.Save(st); err != nil {
		t.Fatal(err)
	}
}

// pgRedisConfig declares one accessory of each built-in kind. `ls` reads state and
// the kind discriminator only, so the drivers are never constructed and no real
// server is needed.
const pgRedisConfig = `
[services.web]
command = "sleep 60"
port_range = { min = 19100, max = 19199 }
proxy_port = 3000

[accessories.database]
kind = "postgres"
server_url = "postgres://isola@localhost:5432/postgres"
clone_from = "myapp_dev"
name = "myapp_${ISOLA_BRANCH_SLUG}"

[accessories.cache]
kind = "redis"
server_url = "redis://localhost:6379"
`

func TestAccessoryLsJSONTypedResources(t *testing.T) {
	dir := setupGitRepo(t)
	if err := os.WriteFile(filepath.Join(dir, config.FileName), []byte(pgRedisConfig), 0644); err != nil {
		t.Fatal(err)
	}
	branch := currentBranch(t, dir)
	const dbName = "bount_api_dependabot-npm-and-ya--shboard-shaka-player-5-2-3-h4pyt33q"
	seedAccessory(t, dir, branch, "database", "postgres", map[string]string{"database": dbName})
	seedAccessory(t, dir, branch, "cache", "redis", map[string]string{"db": "12", "owner": "mono:" + branch})

	out, rows := accessoryLsJSON(t)
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2:\n%s", len(rows), out)
	}

	byName := map[string]jsonAccessory{}
	for _, r := range rows {
		byName[r.Accessory] = r
	}

	pg := byName["database"]
	if pg.Worktree != branch || pg.Kind != "postgres" || !pg.Provisioned {
		t.Errorf("postgres row = %+v", pg)
	}
	if got := pg.Resource["database"]; got != dbName {
		t.Errorf("resource.database = %v, want the provisioned name %q", got, dbName)
	}
	if _, isString := pg.Resource["database"].(string); !isString {
		t.Errorf("resource.database should be a string, got %T", pg.Resource["database"])
	}

	rd := byName["cache"]
	if rd.Kind != "redis" || !rd.Provisioned {
		t.Errorf("redis row = %+v", rd)
	}
	// A logical database indexes a fixed set, so it must be a JSON number: a
	// consumer building a connection URL should not have to parse it.
	if got, ok := rd.Resource["db"].(float64); !ok || got != 12 {
		t.Errorf("resource.db = %#v (%T), want the number 12", rd.Resource["db"], rd.Resource["db"])
	}
	if !strings.Contains(out, `"db":12`) {
		t.Errorf("db should be encoded unquoted, got:\n%s", out)
	}
	if got := rd.Resource["owner"]; got != "mono:"+branch {
		t.Errorf("resource.owner = %v, want %q", got, "mono:"+branch)
	}
}

func TestAccessoryLsJSONUnprovisioned(t *testing.T) {
	dir := setupGitRepo(t)
	if err := os.WriteFile(filepath.Join(dir, config.FileName), []byte(pgRedisConfig), 0644); err != nil {
		t.Fatal(err)
	}

	out, rows := accessoryLsJSON(t)
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2:\n%s", len(rows), out)
	}
	for _, r := range rows {
		if r.Provisioned {
			t.Errorf("%s should not be provisioned: %+v", r.Accessory, r)
		}
		if r.Resource != nil {
			t.Errorf("%s resource = %v, want null", r.Accessory, r.Resource)
		}
		// The kind still comes from config, so a consumer can see what it would get.
		if r.Kind == "" {
			t.Errorf("%s should report its configured kind", r.Accessory)
		}
	}
	// The key must be present and null, not omitted.
	if !strings.Contains(out, `"resource":null`) {
		t.Errorf("resource should be an explicit null, got:\n%s", out)
	}
}

func TestAccessoryLsJSONEmptyConfig(t *testing.T) {
	dir := setupGitRepo(t)
	noAccessories := `
[services.web]
command = "sleep 60"
port_range = { min = 19100, max = 19199 }
proxy_port = 3000
`
	if err := os.WriteFile(filepath.Join(dir, config.FileName), []byte(noAccessories), 0644); err != nil {
		t.Fatal(err)
	}

	out, rows := accessoryLsJSON(t)
	if len(rows) != 0 {
		t.Errorf("got %d rows, want none", len(rows))
	}
	if strings.TrimSpace(out) != "[]" {
		t.Errorf("stdout = %q, want an empty array", strings.TrimSpace(out))
	}
}

// TestAccessoryLsJSONMatchesTableRows pins the two renderings together: same rows,
// same order, so a consumer switching to --json sees nothing appear or vanish.
func TestAccessoryLsJSONMatchesTableRows(t *testing.T) {
	dir := setupGitRepo(t)
	if err := os.WriteFile(filepath.Join(dir, config.FileName), []byte(pgRedisConfig), 0644); err != nil {
		t.Fatal(err)
	}
	branch := currentBranch(t, dir)
	addWorktree(t, dir, "feature/two", filepath.Join(t.TempDir(), "wt2"))
	seedAccessory(t, dir, branch, "database", "postgres", map[string]string{"database": "myapp_main"})
	seedAccessory(t, dir, "feature/two", "cache", "redis", map[string]string{"db": "3", "owner": "p:feature-two"})

	_, rows := accessoryLsJSON(t)

	resetRootCmd()
	rootCmd.SetArgs([]string{"accessory", "ls"})
	table := captureStdout(t, func() {
		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("accessory ls: %v", err)
		}
	})

	var tableRows [][2]string
	for _, line := range strings.Split(strings.TrimSpace(table), "\n")[1:] { // skip header
		f := strings.Fields(line)
		if len(f) >= 2 {
			tableRows = append(tableRows, [2]string{f[0], f[1]})
		}
	}
	if len(rows) != len(tableRows) {
		t.Fatalf("json has %d rows, table has %d:\n%s", len(rows), len(tableRows), table)
	}
	for i, r := range rows {
		if r.Worktree != tableRows[i][0] || r.Accessory != tableRows[i][1] {
			t.Errorf("row %d: json (%s, %s) != table (%s, %s)", i, r.Worktree, r.Accessory, tableRows[i][0], tableRows[i][1])
		}
	}
	// Both worktrees and both accessories are represented.
	if len(rows) != 4 {
		t.Errorf("got %d rows, want 4 (2 worktrees x 2 accessories)", len(rows))
	}
}

// TestAccessoryLsJSONUndescribableResource covers a record this build cannot shape:
// a kind with no registered resource shape (a third-party driver, or one dropped
// from the build). The row must still be emitted with a null resource rather than
// crashing or failing the command.
func TestAccessoryLsJSONUndescribableResource(t *testing.T) {
	dir := setupGitRepo(t)
	if err := os.WriteFile(filepath.Join(dir, config.FileName), []byte(fakeAccessoryConfig), 0644); err != nil {
		t.Fatal(err)
	}
	branch := currentBranch(t, dir)
	// cmdfake registers a driver but no resource shape.
	seedAccessory(t, dir, branch, "db", "cmdfake", map[string]string{"id": "res-x"})

	_, rows := accessoryLsJSON(t)
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	if !rows[0].Provisioned {
		t.Error("the record exists, so provisioned should stay true")
	}
	if rows[0].Resource != nil {
		t.Errorf("resource = %v, want null for a kind with no shape", rows[0].Resource)
	}
}

// TestAccessoryLsJSONMalformedHandle covers a Handle whose own kind rejects it:
// a redis record whose logical database is not a number.
func TestAccessoryLsJSONMalformedHandle(t *testing.T) {
	dir := setupGitRepo(t)
	if err := os.WriteFile(filepath.Join(dir, config.FileName), []byte(pgRedisConfig), 0644); err != nil {
		t.Fatal(err)
	}
	branch := currentBranch(t, dir)
	seedAccessory(t, dir, branch, "cache", "redis", map[string]string{"db": "not-a-number", "owner": "p:x"})
	seedAccessory(t, dir, branch, "database", "postgres", map[string]string{"database": "myapp_main"})

	_, rows := accessoryLsJSON(t)
	byName := map[string]jsonAccessory{}
	for _, r := range rows {
		byName[r.Accessory] = r
	}
	if byName["cache"].Resource != nil {
		t.Errorf("malformed redis resource = %v, want null", byName["cache"].Resource)
	}
	// One bad record must not cost the caller the good ones.
	if got := byName["database"].Resource["database"]; got != "myapp_main" {
		t.Errorf("the readable row should still carry its resource, got %v", got)
	}
}

// currentBranch returns the branch the test repo's main worktree is on, which git
// picks by init.defaultBranch and so cannot be hardcoded.
func currentBranch(t *testing.T, dir string) string {
	t.Helper()
	tree, err := git.CurrentWorktree(dir)
	if err != nil {
		t.Fatal(err)
	}
	return tree.Branch
}

// addWorktree adds a second worktree on a new branch, so multi-worktree output can
// be exercised.
func addWorktree(t *testing.T, repoDir, branch, path string) {
	t.Helper()
	cmd := exec.Command("git", "worktree", "add", "-q", "-b", branch, path)
	cmd.Dir = repoDir
	cmd.Env = git.SanitizedEnv()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git worktree add: %v\n%s", err, out)
	}
	t.Cleanup(func() {
		rm := exec.Command("git", "worktree", "remove", "--force", path)
		rm.Dir = repoDir
		rm.Env = git.SanitizedEnv()
		_, _ = rm.CombinedOutput()
	})
}
