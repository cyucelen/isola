package postgres

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/cyucelen/isola/internal/accessory"
	"github.com/cyucelen/isola/internal/git"
)

// fakeConn records the SQL it is asked to run and returns canned output for the
// existence check.
type fakeConn struct {
	calls    *[]string
	existsRe string // if a pg_database query contains this, report the db exists
}

func (f *fakeConn) exec(ctx context.Context, sql string) (string, error) {
	*f.calls = append(*f.calls, sql)
	if strings.Contains(sql, "FROM pg_database") {
		if f.existsRe != "" && strings.Contains(sql, f.existsRe) {
			return "1\n", nil
		}
		return "\n", nil
	}
	return "", nil
}

func (f *fakeConn) close(ctx context.Context) {}

// cfgLoader returns a config loader (matching New's decode signature) that
// copies cfg into the *pgConfig target.
func cfgLoader(cfg pgConfig) func(interface{}) error {
	return func(v interface{}) error {
		p, ok := v.(*pgConfig)
		if !ok {
			return fmt.Errorf("config target is %T, want *pgConfig", v)
		}
		*p = cfg
		return nil
	}
}

// newTestDriver builds a driver whose connections are fakes recording SQL into
// the returned slice. existsRe controls which db the existence check reports.
func newTestDriver(t *testing.T, cfg pgConfig, existsRe string) (*driver, *[]string) {
	t.Helper()
	d, err := New("primary", cfgLoader(cfg))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	dr, ok := d.(*driver)
	if !ok {
		t.Fatalf("New returned %T, want *driver", d)
	}
	calls := &[]string{}
	dr.open = func(ctx context.Context, connURL string) (conn, error) {
		return &fakeConn{calls: calls, existsRe: existsRe}, nil
	}
	return dr, calls
}

var baseCfg = pgConfig{
	ServerURL: "postgres://isola:isola@localhost:5432/postgres",
	CloneFrom: "myapp_dev",
	Name:      "myapp_${ISOLA_BRANCH_SLUG}",
}

var wt = accessory.WorktreeInfo{Branch: "feature/auth", Slug: "feature-auth"}

func TestNewValidation(t *testing.T) {
	tests := []struct {
		name string
		mut  func(*pgConfig)
		want string
	}{
		{"missing server_url", func(c *pgConfig) { c.ServerURL = "" }, "server_url is required"},
		{"missing clone_from", func(c *pgConfig) { c.CloneFrom = "" }, "clone_from is required"},
		{"missing name", func(c *pgConfig) { c.Name = "" }, "name is required"},
		{"invalid clone_from", func(c *pgConfig) { c.CloneFrom = `bad"name` }, "clone_from"},
		{"dsn server_url without url override", func(c *pgConfig) {
			c.ServerURL = "host=localhost port=5432 dbname=postgres"
		}, "must be a postgres:// URL"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := baseCfg
			tt.mut(&c)
			_, err := New("primary", cfgLoader(c))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestNewAllowsDSNWithURLOverride(t *testing.T) {
	c := baseCfg
	c.ServerURL = "host=localhost port=5432 dbname=postgres"
	c.URL = "postgres://app:app@localhost:5432/${db}"
	if _, err := New("primary", cfgLoader(c)); err != nil {
		t.Fatalf("DSN server_url with url override should be allowed: %v", err)
	}
}

func TestProvisionCreatesWhenAbsent(t *testing.T) {
	d, calls := newTestDriver(t, baseCfg, "") // db does not exist
	got, err := d.Provision(context.Background(), wt)
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if got.Handle["database"] != "myapp_feature-auth" {
		t.Errorf("Handle[database] = %q, want myapp_feature-auth", got.Handle["database"])
	}
	if got.URL != "postgres://isola:isola@localhost:5432/myapp_feature-auth" {
		t.Errorf("injected URL = %q", got.URL)
	}

	joined := strings.Join(*calls, "\n")
	if !strings.Contains(joined, "pg_terminate_backend") {
		t.Error("expected pg_terminate_backend safety net before create")
	}
	if !strings.Contains(joined, `CREATE DATABASE "myapp_feature-auth" TEMPLATE "myapp_dev"`) {
		t.Errorf("missing/incorrect CREATE statement in:\n%s", joined)
	}
}

func TestProvisionReusesWhenPresent(t *testing.T) {
	d, calls := newTestDriver(t, baseCfg, "myapp_feature-auth") // db already exists
	if _, err := d.Provision(context.Background(), wt); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	for _, sql := range *calls {
		if strings.Contains(sql, "CREATE DATABASE") {
			t.Errorf("should not CREATE when db exists, got: %s", sql)
		}
	}
}

func TestResetDropsThenCreates(t *testing.T) {
	d, calls := newTestDriver(t, baseCfg, "")
	if _, err := d.Reset(context.Background(), wt); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	joined := strings.Join(*calls, "\n")
	dropIdx := strings.Index(joined, "DROP DATABASE")
	createIdx := strings.Index(joined, "CREATE DATABASE")
	if dropIdx < 0 || createIdx < 0 || dropIdx > createIdx {
		t.Errorf("Reset should DROP before CREATE, calls:\n%s", joined)
	}
}

// TestResetRefusesTemplateCollision covers the data-loss guard: a branch whose
// resolved name equals clone_from must never reach a DROP of the template.
func TestResetRefusesTemplateCollision(t *testing.T) {
	d, calls := newTestDriver(t, baseCfg, "")
	devWt := accessory.WorktreeInfo{Branch: "dev", Slug: "dev"} // name -> myapp_dev == clone_from
	if _, err := d.Reset(context.Background(), devWt); err == nil || !strings.Contains(err.Error(), "collides with clone_from") {
		t.Fatalf("Reset err = %v, want clone_from collision", err)
	}
	if len(*calls) != 0 {
		t.Errorf("collision must be caught before any SQL, got %v", *calls)
	}
}

func TestProvisionRefusesTemplateCollision(t *testing.T) {
	d, calls := newTestDriver(t, baseCfg, "myapp_dev")
	devWt := accessory.WorktreeInfo{Branch: "dev", Slug: "dev"}
	if _, err := d.Provision(context.Background(), devWt); err == nil || !strings.Contains(err.Error(), "collides with clone_from") {
		t.Fatalf("Provision err = %v, want clone_from collision", err)
	}
	if len(*calls) != 0 {
		t.Errorf("collision must be caught before any SQL, got %v", *calls)
	}
}

func TestResolveNameRefusesMaintenanceDBCollision(t *testing.T) {
	c := baseCfg
	c.Name = "postgres" // equals the server maintenance db in server_url path
	d, _ := newTestDriver(t, c, "")
	if _, err := d.resolveName(wt); err == nil || !strings.Contains(err.Error(), "maintenance database") {
		t.Fatalf("resolveName err = %v, want maintenance-db collision", err)
	}
}

func TestDropGuards(t *testing.T) {
	d, calls := newTestDriver(t, baseCfg, "")
	if err := d.Drop(context.Background(), map[string]string{"database": "myapp_dev"}); err == nil || !strings.Contains(err.Error(), "clone_from") {
		t.Errorf("dropping clone_from should be refused, got %v", err)
	}
	if err := d.Drop(context.Background(), map[string]string{"database": "postgres"}); err == nil || !strings.Contains(err.Error(), "maintenance") {
		t.Errorf("dropping server db should be refused, got %v", err)
	}
	if len(*calls) != 0 {
		t.Errorf("guarded drops must not issue SQL, got %v", *calls)
	}

	if err := d.Drop(context.Background(), map[string]string{"database": "myapp_feature-auth"}); err != nil {
		t.Fatalf("normal drop: %v", err)
	}
	if !strings.Contains((*calls)[0], `DROP DATABASE IF EXISTS "myapp_feature-auth" WITH (FORCE)`) {
		t.Errorf("unexpected drop SQL: %s", (*calls)[0])
	}
}

func TestConnURLOverride(t *testing.T) {
	c := baseCfg
	c.URL = "postgres://app:app@localhost:5432/${db}?sslmode=disable"
	d, _ := newTestDriver(t, c, "myapp_feature-auth")
	got, err := d.Provision(context.Background(), wt)
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	want := "postgres://app:app@localhost:5432/myapp_feature-auth?sslmode=disable"
	if got.URL != want {
		t.Errorf("injected URL = %q, want %q", got.URL, want)
	}
}

func TestResolveNameRejectsBadIdent(t *testing.T) {
	c := baseCfg
	c.Name = "bad\"name"
	d, calls := newTestDriver(t, c, "")
	if _, err := d.Provision(context.Background(), wt); err == nil || !strings.Contains(err.Error(), "illegal character") {
		t.Errorf("expected illegal-character rejection, got %v", err)
	}
	if len(*calls) != 0 {
		t.Error("must not touch server when name is invalid")
	}
}

// TestProvisionFitsLongBranchNames covers the reported failure: a long branch
// (an automated dependency bump) used to resolve to a name past Postgres'
// identifier limit, so provisioning failed and every service that needed the
// database came up without one.
func TestProvisionFitsLongBranchNames(t *testing.T) {
	const branch = "dependabot/npm_and_yarn/services/manager-dashboard/react-intersection-observer-10.1.0"
	longWt := accessory.WorktreeInfo{Branch: branch, Slug: git.BranchSlug(branch)}

	d, calls := newTestDriver(t, baseCfg, "")
	got, err := d.Provision(context.Background(), longWt)
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	name := got.Handle["database"]
	if len(name) > maxIdentBytes {
		t.Errorf("database name %q is %d bytes, over the %d-byte limit", name, len(name), maxIdentBytes)
	}
	if !strings.Contains(strings.Join(*calls, "\n"), `CREATE DATABASE "`+name+`"`) {
		t.Errorf("CREATE did not use the resolved name %q, calls:\n%s", name, strings.Join(*calls, "\n"))
	}
	// The name must still be traceable back to the worktree by eye.
	if !strings.HasPrefix(name, "myapp_dependabot-") {
		t.Errorf("database name %q should stay readable", name)
	}
}

// TestProvisionKeepsSharedPrefixBranchesApart is the collision regression: two
// branches whose slugs agree for the first 63 bytes (routine for automated
// bumps, which differ only in the trailing version) must get separate databases.
// Sharing one is worse than failing to start: each worktree would read the
// other's rows and migrate the other's schema.
func TestProvisionKeepsSharedPrefixBranchesApart(t *testing.T) {
	const prefix = "dependabot/npm_and_yarn/services/manager-dashboard/axioss-1.18."
	names := map[string]bool{}
	for _, branch := range []string{prefix + "0", prefix + "1"} {
		w := accessory.WorktreeInfo{Branch: branch, Slug: git.BranchSlug(branch)}
		d, _ := newTestDriver(t, baseCfg, "")
		got, err := d.Provision(context.Background(), w)
		if err != nil {
			t.Fatalf("Provision(%s): %v", branch, err)
		}
		names[got.Handle["database"]] = true
	}
	if len(names) != 2 {
		t.Errorf("both branches share one database name: %v", names)
	}
}

// TestProvisionReportsUnfittableName covers the loud-failure path: when the
// configured template cannot fit the limit even with a shortened slug, the error
// names the template and the budget instead of leaving a partly-started stack.
func TestProvisionReportsUnfittableName(t *testing.T) {
	c := baseCfg
	c.Name = strings.Repeat("x", 60) + "_${ISOLA_BRANCH_SLUG}"
	d, calls := newTestDriver(t, c, "")

	const branch = "dependabot/npm_and_yarn/services/manager-dashboard/axios-1.18.0"
	longWt := accessory.WorktreeInfo{Branch: branch, Slug: git.BranchSlug(branch)}
	_, err := d.Provision(context.Background(), longWt)
	if err == nil {
		t.Fatal("Provision should fail when the name cannot fit the identifier limit")
	}
	for _, want := range []string{"database name", c.Name, "63"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %v should mention %q", err, want)
		}
	}
	if len(*calls) != 0 {
		t.Errorf("must not touch the server when the name cannot fit, got %v", *calls)
	}
}

func TestValidIdent(t *testing.T) {
	if err := validIdent(strings.Repeat("x", 64)); err == nil {
		t.Error("64-byte name should exceed limit")
	}
	if err := validIdent(strings.Repeat("x", 63)); err != nil {
		t.Errorf("63-byte name should be valid: %v", err)
	}
	if err := validIdent("myapp_feature-auth"); err != nil {
		t.Errorf("hyphenated name should be valid (we double-quote): %v", err)
	}
	for _, bad := range []string{`a"b`, "a'b", `a\b`, "a\x00b", "a\nb"} {
		if err := validIdent(bad); err == nil {
			t.Errorf("name %q should be rejected", bad)
		}
	}
}
