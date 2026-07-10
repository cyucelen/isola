// Package postgres implements the "postgres" accessory kind: a per-worktree
// database cloned from a seeded template on a shared, user-managed Postgres
// server. isola never manages the server lifecycle; it only creates and drops
// databases within it over the wire protocol via pgx (no external psql client
// required). See docs/adr/005-accessories.md.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/cyucelen/isola/internal/accessory"
	"github.com/jackc/pgx/v5"
)

func init() {
	accessory.Register("postgres", New)
}

// connectTimeout bounds establishing a connection. The overall per-operation
// deadline is owned by the caller (see accessory.OpTimeout), which wraps the
// context before calling the driver.
const connectTimeout = 10 * time.Second

// handleDatabase is the Handle key under which the driver records the database
// it provisioned, so Drop can find it without re-reading config.
const handleDatabase = "database"

// pgConfig is the driver-owned config schema for [accessories.<name>].
type pgConfig struct {
	ServerURL string `toml:"server_url"` // existing server + maintenance db, for CREATE/DROP
	CloneFrom string `toml:"clone_from"` // seeded source database copied per worktree
	Name      string `toml:"name"`       // per-worktree db name; supports ${VAR}
	URL       string `toml:"url"`        // optional connection-string override; supports ${db}
}

// conn is a live connection for the duration of one driver operation. It is a
// seam so tests can assert the SQL without a real server, and so a single
// operation reuses one connection across its statements.
type conn interface {
	exec(ctx context.Context, sql string) (string, error)
	close(ctx context.Context)
}

// opener establishes a conn to connURL.
type opener func(ctx context.Context, connURL string) (conn, error)

type driver struct {
	name      string
	cfg       pgConfig
	serverURL *url.URL // ServerURL parsed once at construction
	serverDB  string   // maintenance db name, protected from drops
	open      opener
}

// New builds a postgres accessory from its decoded config.
func New(name string, dec accessory.Decoder) (accessory.Accessory, error) {
	var c pgConfig
	if err := dec(&c); err != nil {
		return nil, err
	}
	if c.ServerURL == "" {
		return nil, errors.New("server_url is required")
	}
	if c.CloneFrom == "" {
		return nil, errors.New("clone_from is required")
	}
	if c.Name == "" {
		return nil, errors.New("name is required")
	}
	// clone_from is interpolated into DDL just like the per-worktree name, so
	// hold it to the same identifier rules (fail fast on bad config).
	if err := validIdent(c.CloneFrom); err != nil {
		return nil, fmt.Errorf("clone_from: %w", err)
	}
	u, err := url.Parse(c.ServerURL)
	if err != nil {
		return nil, fmt.Errorf("server_url is not a valid URL: %w", err)
	}
	// The injected connection string is derived from server_url by swapping the
	// database, which only works for URL-form values. A libpq keyword/value DSN
	// (which pgx also accepts for the admin connection) has no scheme and would
	// produce a garbage injected URL, so require a URL unless the user supplies
	// an explicit url override.
	if u.Scheme == "" && c.URL == "" {
		return nil, errors.New("server_url must be a postgres:// URL, or set 'url' to the connection string injected into services")
	}
	return &driver{
		name:      name,
		cfg:       c,
		serverURL: u,
		serverDB:  strings.TrimPrefix(u.Path, "/"),
		open:      pgxOpen,
	}, nil
}

func (d *driver) Name() string { return d.name }
func (d *driver) Kind() string { return "postgres" }

// resolveName expands ${VAR} in the configured name, validates it as a
// Postgres identifier safe to interpolate (we always double-quote it in DDL),
// and refuses names that collide with the clone source or maintenance database
// — provisioning or resetting those would corrupt or destroy shared state.
func (d *driver) resolveName(wt accessory.WorktreeInfo) (string, error) {
	name := wt.Expand(d.cfg.Name, nil)
	if err := validIdent(name); err != nil {
		return "", err
	}
	if name == d.cfg.CloneFrom {
		return "", fmt.Errorf("resolved database name %q collides with clone_from; adjust the name template or rename the branch", name)
	}
	if d.serverDB != "" && name == d.serverDB {
		return "", fmt.Errorf("resolved database name %q collides with the server maintenance database", name)
	}
	return name, nil
}

func (d *driver) Provision(ctx context.Context, wt accessory.WorktreeInfo) (accessory.Provisioned, error) {
	dbName, err := d.resolveName(wt)
	if err != nil {
		return accessory.Provisioned{}, err
	}

	c, err := d.open(ctx, d.cfg.ServerURL)
	if err != nil {
		return accessory.Provisioned{}, err
	}
	defer c.close(ctx)

	exists, err := d.databaseExists(ctx, c, dbName)
	if err != nil {
		return accessory.Provisioned{}, err
	}
	if !exists {
		if err := d.createFromTemplate(ctx, c, dbName); err != nil {
			return accessory.Provisioned{}, err
		}
	}
	return d.provisioned(wt, dbName), nil
}

func (d *driver) Reset(ctx context.Context, wt accessory.WorktreeInfo) (accessory.Provisioned, error) {
	dbName, err := d.resolveName(wt)
	if err != nil {
		return accessory.Provisioned{}, err
	}

	c, err := d.open(ctx, d.cfg.ServerURL)
	if err != nil {
		return accessory.Provisioned{}, err
	}
	defer c.close(ctx)

	if err := d.dropDatabase(ctx, c, dbName); err != nil {
		return accessory.Provisioned{}, err
	}
	if err := d.createFromTemplate(ctx, c, dbName); err != nil {
		return accessory.Provisioned{}, err
	}
	return d.provisioned(wt, dbName), nil
}

func (d *driver) Drop(ctx context.Context, handle map[string]string) error {
	dbName := handle[handleDatabase]
	if err := validIdent(dbName); err != nil {
		return err
	}
	if err := d.guardDrop(dbName); err != nil {
		return err
	}

	c, err := d.open(ctx, d.cfg.ServerURL)
	if err != nil {
		return err
	}
	defer c.close(ctx)

	return d.dropDatabase(ctx, c, dbName)
}

// provisioned assembles the Handle and exposed URL for a resolved database name.
func (d *driver) provisioned(wt accessory.WorktreeInfo, dbName string) accessory.Provisioned {
	return accessory.Provisioned{
		Handle: map[string]string{handleDatabase: dbName},
		URL:    d.connURL(wt, dbName),
	}
}

// connURL derives the connection string injected into services. If a url
// override is set it is expanded with ${db} bound to the database name;
// otherwise the parsed server_url has its database swapped for dbName.
func (d *driver) connURL(wt accessory.WorktreeInfo, dbName string) string {
	if d.cfg.URL != "" {
		return wt.Expand(d.cfg.URL, map[string]string{"db": dbName})
	}
	u := *d.serverURL // copy; do not mutate the shared parsed URL
	u.Path = "/" + dbName
	return u.String()
}

func (d *driver) databaseExists(ctx context.Context, c conn, dbName string) (bool, error) {
	out, err := c.exec(ctx, "SELECT 1 FROM pg_database WHERE datname = "+quoteLiteral(dbName))
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) == "1", nil
}

// createFromTemplate terminates any lingering connections to the template (a
// physical-copy CREATE requires a quiescent source) and clones it.
func (d *driver) createFromTemplate(ctx context.Context, c conn, dbName string) error {
	if _, err := c.exec(ctx,
		"SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = "+
			quoteLiteral(d.cfg.CloneFrom)+" AND pid <> pg_backend_pid()"); err != nil {
		return fmt.Errorf("terminating connections to template %q: %w", d.cfg.CloneFrom, err)
	}
	if _, err := c.exec(ctx,
		"CREATE DATABASE "+quoteIdent(dbName)+" TEMPLATE "+quoteIdent(d.cfg.CloneFrom)); err != nil {
		return fmt.Errorf("creating database %q from template %q: %w", dbName, d.cfg.CloneFrom, err)
	}
	return nil
}

func (d *driver) dropDatabase(ctx context.Context, c conn, dbName string) error {
	// WITH (FORCE) terminates lingering connections (Postgres 13+).
	if _, err := c.exec(ctx, "DROP DATABASE IF EXISTS "+quoteIdent(dbName)+" WITH (FORCE)"); err != nil {
		return fmt.Errorf("dropping database %q: %w", dbName, err)
	}
	return nil
}

// guardDrop refuses to drop databases isola must never touch: the clone source
// and the server's maintenance database.
func (d *driver) guardDrop(dbName string) error {
	if dbName == d.cfg.CloneFrom {
		return fmt.Errorf("refusing to drop clone_from database %q", dbName)
	}
	if d.serverDB != "" && dbName == d.serverDB {
		return fmt.Errorf("refusing to drop server maintenance database %q", dbName)
	}
	return nil
}

// validIdent checks a database name is safe to embed both as a double-quoted
// identifier and inside a single-quoted string literal: non-empty, within
// Postgres' 63-byte limit, and free of the quote/escape/control characters that
// could break out of either quoting regardless of standard_conforming_strings.
func validIdent(s string) error {
	if s == "" {
		return errors.New("database name is empty")
	}
	if len(s) > 63 {
		return fmt.Errorf("database name %q exceeds Postgres' 63-byte identifier limit", s)
	}
	if strings.ContainsAny(s, "\"'\\\x00\n\r") {
		return fmt.Errorf("database name %q contains an illegal character", s)
	}
	return nil
}

// quoteIdent wraps a validated identifier in double quotes so names with
// hyphens (from branch slugs) are legal in DDL.
func quoteIdent(s string) string {
	return `"` + s + `"`
}

// quoteLiteral renders s as a single-quoted SQL string literal. Callers only
// pass validIdent-checked values, which contain no quote or backslash.
func quoteLiteral(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// pgxConn is the real conn, backed by a pgx connection.
type pgxConn struct{ conn *pgx.Conn }

// pgxOpen connects over the Postgres wire protocol. The simple query protocol
// is used because Postgres forbids CREATE/DROP DATABASE under the extended
// (prepared-statement) protocol, and it runs each statement in autocommit
// rather than an implicit transaction.
func pgxOpen(ctx context.Context, connURL string) (conn, error) {
	cfg, err := pgx.ParseConfig(connURL)
	if err != nil {
		return nil, err
	}
	cfg.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	if cfg.ConnectTimeout == 0 {
		cfg.ConnectTimeout = connectTimeout
	}
	c, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return &pgxConn{conn: c}, nil
}

// exec runs one statement and joins the first column of any returned rows with
// newlines (mirroring psql's tuples-only output for the scalar SELECTs used
// here); DDL returns no rows and yields an empty string.
func (p *pgxConn) exec(ctx context.Context, sql string) (string, error) {
	rows, err := p.conn.Query(ctx, sql)
	if err != nil {
		return "", err
	}
	defer rows.Close()

	var lines []string
	for rows.Next() {
		vals, err := rows.Values()
		if err != nil {
			return "", err
		}
		if len(vals) > 0 && vals[0] != nil {
			lines = append(lines, fmt.Sprintf("%v", vals[0]))
		}
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	return strings.Join(lines, "\n"), nil
}

func (p *pgxConn) close(ctx context.Context) { _ = p.conn.Close(ctx) }
