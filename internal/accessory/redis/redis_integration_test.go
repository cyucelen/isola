//go:build integration

// These tests exercise the real go-redis path against a throwaway container.
// They require Docker and are excluded from the default build; run them with:
//
//	go test -tags=integration ./internal/accessory/redis/
package redis

import (
	"context"
	"fmt"
	"testing"

	"github.com/cyucelen/isola/internal/accessory"
	goredis "github.com/redis/go-redis/v9"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

func startRedis(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	ctr, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        "redis:7-alpine",
			ExposedPorts: []string{"6379/tcp"},
			WaitingFor:   wait.ForListeningPort("6379/tcp"),
		},
		Started: true,
	})
	if err != nil {
		t.Fatalf("starting redis container: %v", err)
	}
	t.Cleanup(func() { _ = testcontainers.TerminateContainer(ctr) })

	host, err := ctr.Host(ctx)
	if err != nil {
		t.Fatalf("container host: %v", err)
	}
	port, err := ctr.MappedPort(ctx, "6379")
	if err != nil {
		t.Fatalf("mapped port: %v", err)
	}
	return fmt.Sprintf("redis://%s:%s", host, port.Port())
}

func newDriver(t *testing.T, serverURL string) *driver {
	t.Helper()
	d, err := New("cache", func(v interface{}) error {
		*(v.(*rdConfig)) = rdConfig{ServerURL: serverURL, Inject: "REDIS_URL"}
		return nil
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return d.(*driver)
}

func clientFor(t *testing.T, url string) *goredis.Client {
	t.Helper()
	opts, err := goredis.ParseURL(url)
	if err != nil {
		t.Fatalf("parse %q: %v", url, err)
	}
	c := goredis.NewClient(opts)
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func TestRedisIntegration(t *testing.T) {
	ctx := context.Background()
	serverURL := startRedis(t)
	d := newDriver(t, serverURL)

	main := accessory.WorktreeInfo{Branch: "main", Slug: "main"}
	feat := accessory.WorktreeInfo{Branch: "feature/x", Slug: "feature-x"}

	a, err := d.Provision(ctx, main)
	if err != nil {
		t.Fatalf("Provision main: %v", err)
	}
	b, err := d.Provision(ctx, feat)
	if err != nil {
		t.Fatalf("Provision feature: %v", err)
	}
	if a.Handle["db"] == b.Handle["db"] {
		t.Fatalf("worktrees must get distinct logical DBs, both got %s", a.Handle["db"])
	}

	// Isolation: a write in main's DB must not be visible in feature's DB.
	ca := clientFor(t, a.Env["REDIS_URL"])
	cb := clientFor(t, b.Env["REDIS_URL"])
	if err := ca.Set(ctx, "greeting", "hello", 0).Err(); err != nil {
		t.Fatalf("set in main db: %v", err)
	}
	if v, err := ca.Get(ctx, "greeting").Result(); err != nil || v != "hello" {
		t.Fatalf("main db read = %q, %v", v, err)
	}
	if _, err := cb.Get(ctx, "greeting").Result(); err != goredis.Nil {
		t.Errorf("key leaked into feature db (want redis.Nil, got %v)", err)
	}

	// Reuse: same worktree resolves to the same DB.
	a2, err := d.Provision(ctx, main)
	if err != nil {
		t.Fatalf("re-Provision main: %v", err)
	}
	if a2.Handle["db"] != a.Handle["db"] {
		t.Errorf("reuse changed db: %s -> %s", a.Handle["db"], a2.Handle["db"])
	}

	// Reset: flushes main's DB back to empty.
	if _, err := d.Reset(ctx, main); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if _, err := ca.Get(ctx, "greeting").Result(); err != goredis.Nil {
		t.Errorf("reset did not clear the db (got %v)", err)
	}

	// Drop: releases main's DB; a fresh provision must still succeed.
	if err := d.Drop(ctx, a.Handle); err != nil {
		t.Fatalf("Drop: %v", err)
	}
	if _, err := d.Provision(ctx, main); err != nil {
		t.Fatalf("Provision after drop: %v", err)
	}
}
