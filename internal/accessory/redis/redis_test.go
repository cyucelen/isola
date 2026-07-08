package redis

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/cyucelen/isola/internal/accessory"
)

// fakeStore simulates per-DB owner markers in memory.
type fakeStore struct {
	owners  map[int]string
	flushed []int
}

func newFakeStore() *fakeStore { return &fakeStore{owners: map[int]string{}} }

func (f *fakeStore) setOwnerNX(_ context.Context, db int, owner string) (bool, error) {
	if _, ok := f.owners[db]; ok {
		return false, nil
	}
	f.owners[db] = owner
	return true, nil
}
func (f *fakeStore) owner(_ context.Context, db int) (string, error) { return f.owners[db], nil }
func (f *fakeStore) setOwner(_ context.Context, db int, owner string) error {
	f.owners[db] = owner
	return nil
}
func (f *fakeStore) flush(_ context.Context, db int) error {
	delete(f.owners, db)
	f.flushed = append(f.flushed, db)
	return nil
}
func (f *fakeStore) close() error { return nil }

func newTestDriver(t *testing.T, cfg rdConfig, s store) *driver {
	t.Helper()
	d, err := New("cache", func(v interface{}) error { *(v.(*rdConfig)) = cfg; return nil })
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	dr := d.(*driver)
	dr.open = func(string) (store, error) { return s, nil }
	return dr
}

var baseCfg = rdConfig{ServerURL: "redis://localhost:6379", Inject: "REDIS_URL"}

var wt = accessory.WorktreeInfo{Branch: "feature/auth", Slug: "feature-auth"}

func TestNewValidation(t *testing.T) {
	tests := []struct {
		name string
		mut  func(*rdConfig)
		want string
	}{
		{"missing server_url", func(c *rdConfig) { c.ServerURL = "" }, "server_url is required"},
		{"missing inject", func(c *rdConfig) { c.Inject = "" }, "inject is required"},
		{"bad server_url", func(c *rdConfig) { c.ServerURL = "http://not-redis" }, "not a valid redis URL"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := baseCfg
			tt.mut(&c)
			_, err := New("cache", func(v interface{}) error { *(v.(*rdConfig)) = c; return nil })
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestProvisionAllocatesAndInjects(t *testing.T) {
	s := newFakeStore()
	d := newTestDriver(t, baseCfg, s)

	got, err := d.Provision(context.Background(), wt)
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	db := got.Handle["db"]
	if db == "" {
		t.Fatal("Handle missing db")
	}
	if got.Handle["owner"] != "feature-auth" {
		t.Errorf("Handle owner = %q", got.Handle["owner"])
	}
	want := "redis://localhost:6379/" + db
	if got.Env["REDIS_URL"] != want {
		t.Errorf("REDIS_URL = %q, want %q", got.Env["REDIS_URL"], want)
	}
	if s.owners[mustAtoi(t, db)] != "feature-auth" {
		t.Errorf("owner marker not set for db %s", db)
	}
}

func TestProvisionReusesSameDB(t *testing.T) {
	s := newFakeStore()
	d := newTestDriver(t, baseCfg, s)

	first, err := d.Provision(context.Background(), wt)
	if err != nil {
		t.Fatalf("first Provision: %v", err)
	}
	second, err := d.Provision(context.Background(), wt)
	if err != nil {
		t.Fatalf("second Provision: %v", err)
	}
	if first.Handle["db"] != second.Handle["db"] {
		t.Errorf("reuse changed db: %s -> %s", first.Handle["db"], second.Handle["db"])
	}
}

func TestAllocateProbesPastOtherOwner(t *testing.T) {
	s := newFakeStore()
	d := newTestDriver(t, baseCfg, s)

	// Pre-own this slug's hashed base index with a different worktree.
	base := int(hashSlug(wt.Slug) % uint32(d.numDB))
	s.owners[base] = "someone-else"

	got, err := d.Provision(context.Background(), wt)
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if got.Handle["db"] == strconv.Itoa(base) {
		t.Errorf("should not have claimed the occupied base db %d", base)
	}
	if s.owners[base] != "someone-else" {
		t.Error("must not steal another worktree's db")
	}
}

func TestNoFreeDB(t *testing.T) {
	c := baseCfg
	c.Databases = 1 // only db 0 available
	s := newFakeStore()
	s.owners[0] = "occupant"
	d := newTestDriver(t, c, s)

	if _, err := d.Provision(context.Background(), wt); err == nil || !strings.Contains(err.Error(), "no free Redis") {
		t.Fatalf("err = %v, want 'no free Redis'", err)
	}
}

func TestDropOnlyWhenOwned(t *testing.T) {
	s := newFakeStore()
	d := newTestDriver(t, baseCfg, s)
	prov, _ := d.Provision(context.Background(), wt)

	// Owner mismatch: the slot was reassigned; drop must not flush it.
	if err := d.Drop(context.Background(), map[string]string{"db": prov.Handle["db"], "owner": "stale"}); err != nil {
		t.Fatalf("Drop: %v", err)
	}
	if len(s.flushed) != 0 {
		t.Errorf("must not flush a db owned by someone else, flushed=%v", s.flushed)
	}

	// Correct owner: flushes and frees the slot.
	if err := d.Drop(context.Background(), prov.Handle); err != nil {
		t.Fatalf("Drop: %v", err)
	}
	if len(s.flushed) != 1 || strconv.Itoa(s.flushed[0]) != prov.Handle["db"] {
		t.Errorf("expected flush of db %s, got %v", prov.Handle["db"], s.flushed)
	}
}

func TestResetFlushesAndReMarks(t *testing.T) {
	s := newFakeStore()
	d := newTestDriver(t, baseCfg, s)
	prov, _ := d.Provision(context.Background(), wt)
	db := mustAtoi(t, prov.Handle["db"])

	if _, err := d.Reset(context.Background(), wt); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if len(s.flushed) != 1 || s.flushed[0] != db {
		t.Errorf("expected flush of db %d, got %v", db, s.flushed)
	}
	if s.owners[db] != "feature-auth" {
		t.Errorf("ownership should be re-marked after reset, owner=%q", s.owners[db])
	}
}

func mustAtoi(t *testing.T, s string) int {
	t.Helper()
	n, err := strconv.Atoi(s)
	if err != nil {
		t.Fatalf("atoi(%q): %v", s, err)
	}
	return n
}
