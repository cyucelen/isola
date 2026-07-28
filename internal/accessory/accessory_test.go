package accessory

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/cyucelen/isola/internal/config"
	"github.com/cyucelen/isola/internal/git"
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

// longBranch is the shape that motivated ExpandWithin: an automated dependency
// branch whose slug alone is 85 bytes.
const longBranch = "dependabot/npm_and_yarn/services/manager-dashboard/react-intersection-observer-10.1.0"

func TestExpandWithinLeavesFittingNamesAlone(t *testing.T) {
	wt := WorktreeInfo{Branch: "feature/auth", Slug: "feature-auth"}

	got, err := wt.ExpandWithin("myapp_${ISOLA_BRANCH_SLUG}", 63, nil)
	if err != nil {
		t.Fatalf("ExpandWithin: %v", err)
	}
	// Byte-identical to Expand, so existing databases keep their names.
	if want := wt.Expand("myapp_${ISOLA_BRANCH_SLUG}", nil); got != want {
		t.Errorf("ExpandWithin = %q, want %q", got, want)
	}
}

func TestExpandWithinShortensToBudget(t *testing.T) {
	wt := WorktreeInfo{Branch: longBranch, Slug: git.BranchSlug(longBranch)}

	got, err := wt.ExpandWithin("myapp_${ISOLA_BRANCH_SLUG}", 63, nil)
	if err != nil {
		t.Fatalf("ExpandWithin: %v", err)
	}
	if len(got) > 63 {
		t.Errorf("ExpandWithin = %q (%d bytes), over the 63-byte budget", got, len(got))
	}
	if !strings.HasPrefix(got, "myapp_dependabot-") {
		t.Errorf("ExpandWithin = %q, want the template's prefix and a readable slug", got)
	}
}

// TestExpandWithinKeepsSharedPrefixBranchesApart is the collision regression:
// two branches identical for their first 63 bytes must not resolve to one name.
func TestExpandWithinKeepsSharedPrefixBranchesApart(t *testing.T) {
	const prefix = "dependabot/npm_and_yarn/services/manager-dashboard/axioss-1.18."
	a := WorktreeInfo{Branch: prefix + "0", Slug: git.BranchSlug(prefix + "0")}
	b := WorktreeInfo{Branch: prefix + "1", Slug: git.BranchSlug(prefix + "1")}
	if a.Slug[:63] != b.Slug[:63] {
		t.Fatalf("fixtures must share their first 63 bytes")
	}

	na, err := a.ExpandWithin("myapp_${ISOLA_BRANCH_SLUG}", 63, nil)
	if err != nil {
		t.Fatalf("ExpandWithin(a): %v", err)
	}
	nb, err := b.ExpandWithin("myapp_${ISOLA_BRANCH_SLUG}", 63, nil)
	if err != nil {
		t.Fatalf("ExpandWithin(b): %v", err)
	}
	if na == nb {
		t.Errorf("both branches resolved to %q", na)
	}
}

func TestExpandWithinBudgetsEachOccurrence(t *testing.T) {
	wt := WorktreeInfo{Branch: longBranch, Slug: git.BranchSlug(longBranch)}

	got, err := wt.ExpandWithin("${ISOLA_BRANCH_SLUG}_${ISOLA_BRANCH_SLUG}", 63, nil)
	if err != nil {
		t.Fatalf("ExpandWithin: %v", err)
	}
	if len(got) > 63 {
		t.Errorf("ExpandWithin = %q (%d bytes), over the 63-byte budget", got, len(got))
	}
}

func TestExpandWithinReportsUnmeetableBudgets(t *testing.T) {
	wt := WorktreeInfo{Branch: longBranch, Slug: git.BranchSlug(longBranch)}

	cases := []struct {
		name, template string
		maxBytes       int
		want           string
	}{
		{
			// Nothing to shorten: the overflow is not the slug's fault.
			name:     "no slug in template",
			template: "myapp_${ISOLA_BRANCH}",
			maxBytes: 20,
			want:     "no ${ISOLA_BRANCH_SLUG} to shorten",
		},
		{
			// The template's own text leaves too little for a usable slug.
			name:     "fixed text crowds out the slug",
			template: strings.Repeat("x", 60) + "_${ISOLA_BRANCH_SLUG}",
			maxBytes: 63,
			want:     "leaves only",
		},
		{
			// The template's own text is over the limit before substitution.
			name:     "fixed text alone over the limit",
			template: strings.Repeat("x", 70) + "_${ISOLA_BRANCH_SLUG}",
			maxBytes: 63,
			want:     "before ${ISOLA_BRANCH_SLUG} is substituted",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := wt.ExpandWithin(c.template, c.maxBytes, nil)
			if err == nil {
				t.Fatalf("ExpandWithin = %q, want an error", got)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error = %v, want it to mention %q", err, c.want)
			}
			// The failure must name the template and its budget, so `isola up`
			// points at the config rather than at a service.
			if !strings.Contains(err.Error(), c.template) {
				t.Errorf("error = %v, want it to name the template", err)
			}
			if !strings.Contains(err.Error(), fmt.Sprint(c.maxBytes)) {
				t.Errorf("error = %v, want it to name the %d-byte budget", err, c.maxBytes)
			}
		})
	}
}
