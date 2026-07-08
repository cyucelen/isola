package accessory

import (
	"context"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/cyucelen/isola/internal/config"
)

// stubAccessory is a no-op driver used to exercise the registry and BuildAll.
type stubAccessory struct{ name, kind string }

func (s *stubAccessory) Name() string { return s.name }
func (s *stubAccessory) Kind() string { return s.kind }
func (s *stubAccessory) Provision(context.Context, WorktreeInfo) (Provisioned, error) {
	return Provisioned{}, nil
}
func (s *stubAccessory) Drop(context.Context, map[string]string) error { return nil }

func init() {
	Register("stub", func(name string, dec Decoder) (Accessory, error) {
		var c struct {
			Kind string `toml:"kind"`
		}
		if err := dec(&c); err != nil {
			return nil, err
		}
		return &stubAccessory{name: name, kind: c.Kind}, nil
	})
}

// cfgFrom builds a config.Config with accessories from a TOML fragment.
func cfgFrom(t *testing.T, tomlStr string) *config.Config {
	t.Helper()
	var raw struct {
		Accessories map[string]toml.Primitive `toml:"accessories"`
	}
	md, err := toml.Decode(tomlStr, &raw)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	return &config.Config{Accessories: raw.Accessories, Meta: md}
}

func TestBuildAll(t *testing.T) {
	cfg := cfgFrom(t, `
[accessories.primary]
kind = "stub"

[accessories.secondary]
kind = "stub"
`)
	accs, err := BuildAll(cfg)
	if err != nil {
		t.Fatalf("BuildAll: %v", err)
	}
	if len(accs) != 2 {
		t.Fatalf("got %d accessories, want 2", len(accs))
	}
	if accs["primary"].Kind() != "stub" || accs["primary"].Name() != "primary" {
		t.Errorf("primary = %+v", accs["primary"])
	}
}

func TestBuildAllUnknownKind(t *testing.T) {
	cfg := cfgFrom(t, `
[accessories.primary]
kind = "does-not-exist"
`)
	_, err := BuildAll(cfg)
	if err == nil || !strings.Contains(err.Error(), "unknown kind") {
		t.Fatalf("err = %v, want 'unknown kind'", err)
	}
}

func TestBuildAllThirdPartyKindReserved(t *testing.T) {
	cfg := cfgFrom(t, `
[accessories.primary]
kind = "acme/clickhouse"
`)
	_, err := BuildAll(cfg)
	if err == nil || !strings.Contains(err.Error(), "third-party") {
		t.Fatalf("err = %v, want 'third-party ... not supported yet'", err)
	}
}

func TestRegisterDuplicatePanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("registering a kind twice should panic")
		}
	}()
	Register("stub", func(string, Decoder) (Accessory, error) { return nil, nil })
}

func TestWorktreeInfoExpand(t *testing.T) {
	wt := WorktreeInfo{Branch: "feature/auth", Slug: "feature-auth"}

	if got := wt.Expand("myapp_${ISOLA_BRANCH_SLUG}", nil); got != "myapp_feature-auth" {
		t.Errorf("slug expansion = %q", got)
	}
	if got := wt.Expand("${ISOLA_BRANCH}", nil); got != "feature/auth" {
		t.Errorf("branch expansion = %q", got)
	}
	if got := wt.Expand("db=${db}", map[string]string{"db": "myapp_x"}); got != "db=myapp_x" {
		t.Errorf("extra expansion = %q", got)
	}
}
