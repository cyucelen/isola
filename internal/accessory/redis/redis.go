// Package redis implements the "redis" accessory kind: each git worktree gets
// its own Redis logical database on a shared, user-managed Redis server. isola
// never manages the server lifecycle; it only allocates a logical DB per
// worktree and flushes it on reset/drop. See docs/adr/005-accessories.md.
package redis

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"net/url"
	"strconv"

	"github.com/cyucelen/isola/internal/accessory"
	goredis "github.com/redis/go-redis/v9"
)

func init() {
	accessory.Register("redis", New)
}

const (
	// ownerKey marks which worktree owns a logical DB. It lives inside that DB
	// (which the owning worktree has to itself), so allocation is collision-free
	// without a central registry.
	ownerKey = "__isola_owner__"
	// defaultDatabases is Redis' default number of logical databases (0-15).
	defaultDatabases = 16
	// handleDB / handleOwner are the Handle keys recording a worktree's resource.
	handleDB    = "db"
	handleOwner = "owner"
)

// rdConfig is the driver-owned config schema for [accessories.<name>].
type rdConfig struct {
	ServerURL string `toml:"server_url"` // existing Redis server, e.g. redis://localhost:6379
	Databases int    `toml:"databases"`  // logical DBs to allocate across (default 16; set to match your server)
}

// store abstracts the owner-marker and flush operations the driver needs on a
// per-DB basis. It is a seam so tests exercise allocation without a real server.
type store interface {
	// setOwnerNX claims db for owner only if it is currently unowned; it reports
	// whether the claim succeeded.
	setOwnerNX(ctx context.Context, db int, owner string) (bool, error)
	// owner returns the current owner of db, or "" if unowned.
	owner(ctx context.Context, db int) (string, error)
	// setOwner records owner for db unconditionally (used to re-mark after a flush).
	setOwner(ctx context.Context, db int, owner string) error
	// flush clears db (FLUSHDB), removing all keys including the owner marker.
	flush(ctx context.Context, db int) error
	close() error
}

// opener establishes a store against serverURL.
type opener func(serverURL string) (store, error)

type driver struct {
	name      string
	cfg       rdConfig
	serverURL *url.URL // ServerURL parsed once at construction, for connURL
	numDB     int
	open      opener
}

// New builds a redis accessory from its decoded config.
func New(name string, dec accessory.Decoder) (accessory.Accessory, error) {
	var c rdConfig
	if err := dec(&c); err != nil {
		return nil, err
	}
	if c.ServerURL == "" {
		return nil, errors.New("server_url is required")
	}
	if _, err := goredis.ParseURL(c.ServerURL); err != nil {
		return nil, fmt.Errorf("server_url is not a valid redis URL: %w", err)
	}
	u, err := url.Parse(c.ServerURL)
	if err != nil {
		return nil, fmt.Errorf("server_url is not a valid URL: %w", err)
	}
	numDB := c.Databases
	if numDB <= 0 {
		numDB = defaultDatabases
	}
	return &driver{name: name, cfg: c, serverURL: u, numDB: numDB, open: realOpen}, nil
}

func (d *driver) Name() string { return d.name }
func (d *driver) Kind() string { return "redis" }

func (d *driver) Provision(ctx context.Context, wt accessory.WorktreeInfo) (accessory.Provisioned, error) {
	s, err := d.open(d.cfg.ServerURL)
	if err != nil {
		return accessory.Provisioned{}, err
	}
	defer func() { _ = s.close() }()

	db, err := d.allocate(ctx, s, ownerID(wt))
	if err != nil {
		return accessory.Provisioned{}, err
	}
	return d.provisioned(wt, db), nil
}

// ownerID is the value written to a logical DB's owner marker. It is qualified
// by project so two projects sharing a Redis server (and a branch name) never
// collide on the same logical DB.
func ownerID(wt accessory.WorktreeInfo) string {
	if wt.Project != "" {
		return wt.Project + ":" + wt.Slug
	}
	return wt.Slug
}

// Reset flushes the worktree's logical DB back to empty (its baseline), keeping
// the assignment. Redis has no template, so the baseline is an empty database.
func (d *driver) Reset(ctx context.Context, wt accessory.WorktreeInfo) (accessory.Provisioned, error) {
	s, err := d.open(d.cfg.ServerURL)
	if err != nil {
		return accessory.Provisioned{}, err
	}
	defer func() { _ = s.close() }()

	db, err := d.allocate(ctx, s, ownerID(wt))
	if err != nil {
		return accessory.Provisioned{}, err
	}
	if err := s.flush(ctx, db); err != nil {
		return accessory.Provisioned{}, fmt.Errorf("flushing redis db %d: %w", db, err)
	}
	// FLUSHDB removed the owner marker; re-establish ownership.
	if err := s.setOwner(ctx, db, ownerID(wt)); err != nil {
		return accessory.Provisioned{}, fmt.Errorf("re-marking redis db %d: %w", db, err)
	}
	return d.provisioned(wt, db), nil
}

func (d *driver) Drop(ctx context.Context, handle map[string]string) error {
	db, err := strconv.Atoi(handle[handleDB])
	if err != nil {
		return fmt.Errorf("invalid redis handle db %q: %w", handle[handleDB], err)
	}
	s, err := d.open(d.cfg.ServerURL)
	if err != nil {
		return err
	}
	defer func() { _ = s.close() }()

	// Only flush if the DB is still owned by this worktree; if the slot was
	// already released and reassigned, leave the new owner's data alone.
	owner, err := s.owner(ctx, db)
	if err != nil {
		return err
	}
	if owner != handle[handleOwner] {
		return nil
	}
	if err := s.flush(ctx, db); err != nil {
		return fmt.Errorf("flushing redis db %d: %w", db, err)
	}
	return nil
}

// allocate finds the logical DB owned by owner (a "project:slug" id), claiming a
// free one if needed. It hashes the owner id to a starting index and
// linear-probes for a free or already-owned DB, mirroring the port allocator.
func (d *driver) allocate(ctx context.Context, s store, owner string) (int, error) {
	base := int(hashSlug(owner) % uint32(d.numDB))
	for i := 0; i < d.numDB; i++ {
		db := (base + i) % d.numDB
		claimed, err := s.setOwnerNX(ctx, db, owner)
		if err != nil {
			return 0, err
		}
		if claimed {
			return db, nil
		}
		cur, err := s.owner(ctx, db)
		if err != nil {
			return 0, err
		}
		if cur == owner {
			return db, nil // already ours; reuse
		}
	}
	return 0, fmt.Errorf("no free Redis logical database (all %d in use); raise the server's `databases` and set it in config", d.numDB)
}

// provisioned builds the Handle and injected env for an assigned DB index.
func (d *driver) provisioned(wt accessory.WorktreeInfo, db int) accessory.Provisioned {
	return accessory.Provisioned{
		Handle: map[string]string{handleDB: strconv.Itoa(db), handleOwner: ownerID(wt)},
		URL:    d.connURL(db),
	}
}

// connURL returns server_url with its logical database set to db.
func (d *driver) connURL(db int) string {
	return accessory.URLWithPath(d.serverURL, strconv.Itoa(db))
}

func hashSlug(slug string) uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(slug))
	return h.Sum32()
}

// redisStore is the real store, backed by go-redis clients (one per DB index).
type redisStore struct {
	opts    *goredis.Options
	clients map[int]*goredis.Client
}

func realOpen(serverURL string) (store, error) {
	opts, err := goredis.ParseURL(serverURL)
	if err != nil {
		return nil, err
	}
	return &redisStore{opts: opts, clients: map[int]*goredis.Client{}}, nil
}

func (r *redisStore) client(db int) *goredis.Client {
	if c, ok := r.clients[db]; ok {
		return c
	}
	o := *r.opts
	o.DB = db
	c := goredis.NewClient(&o)
	r.clients[db] = c
	return c
}

func (r *redisStore) setOwnerNX(ctx context.Context, db int, owner string) (bool, error) {
	return r.client(db).SetNX(ctx, ownerKey, owner, 0).Result()
}

func (r *redisStore) owner(ctx context.Context, db int) (string, error) {
	v, err := r.client(db).Get(ctx, ownerKey).Result()
	if errors.Is(err, goredis.Nil) {
		return "", nil
	}
	return v, err
}

func (r *redisStore) setOwner(ctx context.Context, db int, owner string) error {
	return r.client(db).Set(ctx, ownerKey, owner, 0).Err()
}

func (r *redisStore) flush(ctx context.Context, db int) error {
	return r.client(db).FlushDB(ctx).Err()
}

func (r *redisStore) close() error {
	for _, c := range r.clients {
		_ = c.Close()
	}
	return nil
}
