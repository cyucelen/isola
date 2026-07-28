//go:build integration

// These tests exercise the real pgx + Postgres path against a throwaway
// container. They require Docker and are excluded from the default build; run
// them with:
//
//	go test -tags=integration ./internal/accessory/postgres/
package postgres

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"github.com/cyucelen/isola/internal/accessory"
	"github.com/cyucelen/isola/internal/git"
	"github.com/jackc/pgx/v5"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

// startPostgres boots a disposable Postgres and seeds a template database
// (myapp_dev, table widgets with 2 rows), returning the maintenance-db URL.
func startPostgres(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	ctr, err := tcpostgres.Run(ctx, "postgres:16",
		tcpostgres.WithDatabase("isola"),
		tcpostgres.WithUsername("isola"),
		tcpostgres.WithPassword("isola"),
		tcpostgres.BasicWaitStrategies(),
	)
	if err != nil {
		t.Fatalf("starting postgres container: %v", err)
	}
	t.Cleanup(func() { _ = testcontainers.TerminateContainer(ctr) })

	serverURL, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}

	mustRun(t, serverURL, "CREATE DATABASE myapp_dev")
	tmpl := withDB(t, serverURL, "myapp_dev")
	mustRun(t, tmpl, "CREATE TABLE widgets (id serial primary key, name text)")
	mustRun(t, tmpl, "INSERT INTO widgets(name) VALUES ('alpha'), ('beta')")
	return serverURL
}

// mustRun executes SQL via the driver's own pgx connection (dogfooding it) and
// returns the scalar output.
func mustRun(t *testing.T, connURL, sql string) string {
	t.Helper()
	ctx := context.Background()
	c, err := pgxOpen(ctx, connURL)
	if err != nil {
		t.Fatalf("connect %q: %v", connURL, err)
	}
	defer c.close(ctx)
	out, err := c.exec(ctx, sql)
	if err != nil {
		t.Fatalf("sql %q: %v", sql, err)
	}
	return strings.TrimSpace(out)
}

// withDB returns base with its database path swapped for db.
func withDB(t *testing.T, base, db string) string {
	t.Helper()
	u, err := url.Parse(base)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	u.Path = "/" + db
	return u.String()
}

func newDriver(t *testing.T, serverURL string) *driver {
	t.Helper()
	cfg := pgConfig{
		ServerURL: serverURL,
		CloneFrom: "myapp_dev",
		Name:      "myapp_${ISOLA_BRANCH_SLUG}",
	}
	d, err := New("database", func(v interface{}) error { *(v.(*pgConfig)) = cfg; return nil })
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return d.(*driver)
}

func TestPostgresIntegration(t *testing.T) {
	ctx := context.Background()
	serverURL := startPostgres(t)
	templateURL := withDB(t, serverURL, "myapp_dev")

	t.Run("clone from template, isolate, and reuse", func(t *testing.T) {
		d := newDriver(t, serverURL)
		wt := accessory.WorktreeInfo{Branch: "feature/one", Slug: "feature-one"}

		prov, err := d.Provision(ctx, wt)
		if err != nil {
			t.Fatalf("Provision: %v", err)
		}
		if prov.Handle["database"] != "myapp_feature-one" {
			t.Errorf("Handle[database] = %q", prov.Handle["database"])
		}
		dbURL := prov.URL

		// Seed data was physically copied from the template.
		if got := mustRun(t, dbURL, "select count(*) from widgets"); got != "2" {
			t.Errorf("cloned db has %s rows, want 2", got)
		}

		// Writes to the clone must not touch the template.
		mustRun(t, dbURL, "insert into widgets(name) values('local')")
		if got := mustRun(t, templateURL, "select count(*) from widgets"); got != "2" {
			t.Errorf("template mutated: %s rows, want 2", got)
		}

		// Re-provisioning reuses the existing db (does not wipe the local write).
		if _, err := d.Provision(ctx, wt); err != nil {
			t.Fatalf("re-Provision: %v", err)
		}
		if got := mustRun(t, dbURL, "select count(*) from widgets"); got != "3" {
			t.Errorf("reuse changed row count to %s, want 3", got)
		}

		if err := d.Drop(ctx, prov.Handle); err != nil {
			t.Fatalf("Drop: %v", err)
		}
	})

	t.Run("reset restores the clone_from baseline", func(t *testing.T) {
		d := newDriver(t, serverURL)
		wt := accessory.WorktreeInfo{Branch: "feature/two", Slug: "feature-two"}

		prov, err := d.Provision(ctx, wt)
		if err != nil {
			t.Fatalf("Provision: %v", err)
		}
		dbURL := prov.URL
		mustRun(t, dbURL, "insert into widgets(name) values('x'), ('y')")
		if got := mustRun(t, dbURL, "select count(*) from widgets"); got != "4" {
			t.Fatalf("pre-reset rows = %s, want 4", got)
		}

		if _, err := d.Reset(ctx, wt); err != nil {
			t.Fatalf("Reset: %v", err)
		}
		if got := mustRun(t, dbURL, "select count(*) from widgets"); got != "2" {
			t.Errorf("post-reset rows = %s, want 2 (baseline)", got)
		}
		_ = d.Drop(ctx, prov.Handle)
	})

	t.Run("drop removes the database and guards protect template/server", func(t *testing.T) {
		d := newDriver(t, serverURL)
		wt := accessory.WorktreeInfo{Branch: "feature/three", Slug: "feature-three"}

		prov, err := d.Provision(ctx, wt)
		if err != nil {
			t.Fatalf("Provision: %v", err)
		}
		if got := mustRun(t, serverURL, "select count(*) from pg_database where datname = 'myapp_feature-three'"); got != "1" {
			t.Fatalf("db not created: %s", got)
		}
		if err := d.Drop(ctx, prov.Handle); err != nil {
			t.Fatalf("Drop: %v", err)
		}
		if got := mustRun(t, serverURL, "select count(*) from pg_database where datname = 'myapp_feature-three'"); got != "0" {
			t.Errorf("db still present after drop: %s", got)
		}

		// Guards must refuse to drop the template or the maintenance db, and
		// must not touch them.
		if err := d.Drop(ctx, map[string]string{"database": "myapp_dev"}); err == nil || !strings.Contains(err.Error(), "clone_from") {
			t.Errorf("dropping clone_from: err = %v", err)
		}
		if err := d.Drop(ctx, map[string]string{"database": "isola"}); err == nil || !strings.Contains(err.Error(), "maintenance") {
			t.Errorf("dropping server db: err = %v", err)
		}
		if got := mustRun(t, templateURL, "select count(*) from widgets"); got != "2" {
			t.Errorf("template harmed by guarded drops: %s rows", got)
		}
	})

	t.Run("provision terminates lingering template connections", func(t *testing.T) {
		// Hold an open backend on the template so a naive CREATE ... TEMPLATE
		// would fail; the driver's pg_terminate_backend safety net must clear it.
		cfg, err := pgx.ParseConfig(templateURL)
		if err != nil {
			t.Fatal(err)
		}
		linger, err := pgx.ConnectConfig(ctx, cfg)
		if err != nil {
			t.Fatalf("open lingering connection: %v", err)
		}
		defer func() { _ = linger.Close(ctx) }()
		if err := linger.Ping(ctx); err != nil {
			t.Fatalf("ping lingering connection: %v", err)
		}

		d := newDriver(t, serverURL)
		wt := accessory.WorktreeInfo{Branch: "feature/linger", Slug: "feature-linger"}
		prov, err := d.Provision(ctx, wt)
		if err != nil {
			t.Fatalf("Provision with lingering template connection: %v", err)
		}
		if got := mustRun(t, prov.URL, "select count(*) from widgets"); got != "2" {
			t.Errorf("cloned db rows = %s, want 2", got)
		}
		_ = d.Drop(ctx, prov.Handle)
	})

	// A long branch (an automated dependency bump) resolves to a name past
	// Postgres' identifier limit. Two such branches routinely share their first 63
	// bytes and differ only in the trailing version, so the name must be
	// shortened with a hash rather than truncated — and the database isola creates
	// must be the one it later connects to.
	t.Run("long branches get distinct, reachable databases", func(t *testing.T) {
		const prefix = "dependabot/npm_and_yarn/services/manager-dashboard/axioss-1.18."
		seen := map[string]bool{}
		for _, branch := range []string{prefix + "0", prefix + "1"} {
			d := newDriver(t, serverURL)
			wt := accessory.WorktreeInfo{Branch: branch, Slug: git.BranchSlug(branch)}

			prov, err := d.Provision(ctx, wt)
			if err != nil {
				t.Fatalf("Provision(%s): %v", branch, err)
			}
			name := prov.Handle["database"]
			if len(name) > maxIdentBytes {
				t.Errorf("database name %q is %d bytes, over the limit", name, len(name))
			}
			if seen[name] {
				t.Fatalf("branch %s reused database %q", branch, name)
			}
			seen[name] = true

			// The name Postgres stored must match the one isola recorded: if the
			// server had truncated it, the injected URL would point elsewhere.
			stored := mustRun(t, prov.URL, "select current_database()")
			if stored != name {
				t.Errorf("connected to %q, but provisioned %q", stored, name)
			}
			if got := mustRun(t, prov.URL, "select count(*) from widgets"); got != "2" {
				t.Errorf("cloned db rows = %s, want 2", got)
			}
			t.Cleanup(func() { _ = d.Drop(ctx, prov.Handle) })
		}
	})

	// Documents why isola shortens names itself rather than passing a long one
	// through or truncating it: Postgres accepts an over-long identifier and
	// silently truncates it to NAMEDATALEN-1, so two names that agree for 63 bytes
	// become one database. Worse, the driver's own existence check could not tell:
	// comparing datname (type "name") against a longer literal truncates the
	// literal too, so the check reports a database isola never created. If a future
	// Postgres rejects over-long identifiers instead, this test says so.
	t.Run("postgres silently truncates over-long identifiers", func(t *testing.T) {
		long := "trunc_" + strings.Repeat("x", maxIdentBytes) // 69 bytes
		truncated := long[:maxIdentBytes]
		mustRun(t, serverURL, `CREATE DATABASE "`+long+`"`)
		t.Cleanup(func() { mustRun(t, serverURL, `DROP DATABASE IF EXISTS "`+truncated+`" WITH (FORCE)`) })

		// Stored under the truncated name, not the one requested.
		if got := mustRun(t, serverURL, "select length(datname::text) from pg_database where datname = '"+long+"'"); got != "63" {
			t.Errorf("stored name length = %s, want 63 (truncation assumption no longer holds)", got)
		}
		if got := mustRun(t, serverURL, "select count(*) from pg_database where datname::text = '"+long+"'"); got != "0" {
			t.Errorf("over-long name stored verbatim (count %s)", got)
		}

		// And the driver's existence check cannot distinguish the two, which is why
		// resolveName must fit the name before any SQL is issued.
		d := newDriver(t, serverURL)
		c, err := d.open(ctx, serverURL)
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		defer c.close(ctx)
		neverCreated := truncated + "-never-created"
		exists, err := d.databaseExists(ctx, c, neverCreated)
		if err != nil {
			t.Fatalf("databaseExists: %v", err)
		}
		if !exists {
			t.Skip("this Postgres distinguishes over-long names; the truncation hazard no longer applies")
		}
	})
}
