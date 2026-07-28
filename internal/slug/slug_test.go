package slug

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestMake(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"main", "main"},
		{"feature/auth", "feature-auth"},
		{"Feature/Auth", "feature-auth"},
		{"dependabot/npm_and_yarn/axios-1.18.0", "dependabot-npm-and-yarn-axios-1-18-0"},
		{"/leading/and/trailing/", "leading-and-trailing"},
		{"a///b", "a-b"},
		{"", ""},
	}
	for _, c := range cases {
		if got := Make(c.in); got != c.want {
			t.Errorf("Make(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestFitLeavesShortEnoughSlugsAlone(t *testing.T) {
	// Names that already fit must come back byte-identical: worktrees keep the
	// resources they were provisioned under.
	for _, s := range []string{"main", "feature-auth", strings.Repeat("a", 63)} {
		if got := Fit(s, 63); got != s {
			t.Errorf("Fit(%q, 63) = %q, want it unchanged", s, got)
		}
	}
}

func TestFitRespectsBudget(t *testing.T) {
	long := Make("dependabot/npm_and_yarn/services/manager-dashboard/react-intersection-observer-10.1.0")
	if len(long) != 85 {
		t.Fatalf("fixture slug is %d bytes, expected 85", len(long))
	}
	for _, budget := range []int{63, 57, 40, 20, MinFit, MinFit - 1, hashLen, 4, 1} {
		got := Fit(long, budget)
		if len(got) > budget {
			t.Errorf("Fit(_, %d) = %q (%d bytes), over budget", budget, got, len(got))
		}
		if got == "" {
			t.Errorf("Fit(_, %d) = empty", budget)
		}
	}
}

// TestFitDistinguishesSharedPrefixes is the regression this package exists for:
// automated branches share a long constant prefix and differ only in the version
// at the very end, which truncation alone would discard — handing two worktrees
// one database.
func TestFitDistinguishesSharedPrefixes(t *testing.T) {
	const prefix = "dependabot/npm_and_yarn/services/manager-dashboard/axioss-1.18."
	a := Make(prefix + "0")
	b := Make(prefix + "1")
	if a[:63] != b[:63] {
		t.Fatalf("fixtures must share their first 63 bytes; got %q and %q", a[:63], b[:63])
	}

	for _, budget := range []int{63, 57, 40, 20, MinFit} {
		fa, fb := Fit(a, budget), Fit(b, budget)
		if fa == fb {
			t.Errorf("Fit(_, %d) mapped two distinct branches onto %q", budget, fa)
		}
	}
}

func TestFitIsStable(t *testing.T) {
	// The digest is re-derived on every `isola up`; if it moved, a worktree would
	// be handed a fresh database and orphan the one it had.
	got := Fit(Make("dependabot/npm_and_yarn/services/manager-dashboard/react-intersection-observer-10.1.0"), 63)
	const want = "dependabot-npm-and-yarn-se--tersection-observer-10-1-0-8l5fsz8a"
	if len(want) > 63 {
		// Guard the fixture itself: 63 is the budget, so want must fit it.
		t.Fatalf("fixture %q is %d bytes, over the 63-byte budget", want, len(want))
	}
	if got != want {
		t.Errorf("Fit(...) = %q, want %q", got, want)
	}
}

func TestFitKeepsBothEnds(t *testing.T) {
	s := Make("dependabot/npm_and_yarn/services/manager-dashboard/react-intersection-observer-10.1.0")
	got := Fit(s, 63)
	if !strings.HasPrefix(got, "dependabot-") {
		t.Errorf("Fit(...) = %q, want it to keep the head", got)
	}
	if !strings.Contains(got, "10-1-0") {
		t.Errorf("Fit(...) = %q, want it to keep the version at the tail", got)
	}
	if !strings.Contains(got, elision) {
		t.Errorf("Fit(...) = %q, want the elision marker %q", got, elision)
	}
}

func TestFitProducesSlugSafeOutput(t *testing.T) {
	// Fit's output goes into hostnames, filenames, and SQL identifiers, so it must
	// stay within the character set Make guarantees.
	for _, budget := range []int{63, 30, 12, MinFit} {
		got := Fit(Make("Feature/Ünïcode—Brränch/that/is/quite/long/indeed/x-2.4.11"), budget)
		if len(got) > budget {
			t.Errorf("Fit(_, %d) = %q, over budget", budget, got)
		}
		for i := 0; i < len(got); i++ {
			c := got[i]
			ok := (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-'
			if !ok {
				t.Errorf("Fit(_, %d) = %q contains illegal byte %q", budget, got, c)
			}
		}
	}
}

func TestFitNeverSplitsARune(t *testing.T) {
	// Make strips non-ASCII, but Fit is also handed names isola did not slugify
	// (a configured template, a directory name), so a cut must land on a rune
	// boundary rather than emit half a codepoint.
	s := strings.Repeat("é", 60) // 120 bytes
	for budget := MinFit; budget <= 60; budget++ {
		got := Fit(s, budget)
		if len(got) > budget {
			t.Fatalf("Fit(_, %d) = %d bytes, over budget", budget, len(got))
		}
		if !utf8.ValidString(got) {
			t.Fatalf("Fit(_, %d) = %q split a UTF-8 sequence", budget, got)
		}
	}
}
