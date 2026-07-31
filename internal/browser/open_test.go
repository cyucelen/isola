package browser

import (
	"strings"
	"testing"
)

func TestBuildURL(t *testing.T) {
	t.Run("standard http", func(t *testing.T) {
		got := BuildURL("http", "feature-auth", "myapp", 3000)
		want := "http://feature-auth.myapp.localhost:3000"
		if got != want {
			t.Errorf("BuildURL() = %q, want %q", got, want)
		}
	})

	t.Run("main branch", func(t *testing.T) {
		got := BuildURL("http", "main", "myapp", 8000)
		want := "http://main.myapp.localhost:8000"
		if got != want {
			t.Errorf("BuildURL() = %q, want %q", got, want)
		}
	})

	t.Run("https", func(t *testing.T) {
		got := BuildURL("https", "feature-auth", "api", 3000)
		want := "https://feature-auth.api.localhost:3000"
		if got != want {
			t.Errorf("BuildURL() = %q, want %q", got, want)
		}
	})

	t.Run("empty project falls back to bare", func(t *testing.T) {
		got := BuildURL("http", "main", "", 3000)
		want := "http://main.localhost:3000"
		if got != want {
			t.Errorf("BuildURL() = %q, want %q", got, want)
		}
	})
}

func TestBuildURL_VariousInputs(t *testing.T) {
	tests := []struct {
		name      string
		scheme    string
		slug      string
		project   string
		proxyPort int
		want      string
	}{
		{"simple slug", "http", "main", "myapp", 3000, "http://main.myapp.localhost:3000"},
		{"hyphenated slug", "http", "feature-auth", "myapp", 8000, "http://feature-auth.myapp.localhost:8000"},
		{"numeric slug", "http", "v2-release", "api", 5000, "http://v2-release.api.localhost:5000"},
		{"single char slug", "http", "a", "b", 3000, "http://a.b.localhost:3000"},
		{"long slug", "http", "very-long-branch-name-here", "proj", 3000, "http://very-long-branch-name-here.proj.localhost:3000"},
		{"https slug", "https", "main", "myapp", 3000, "https://main.myapp.localhost:3000"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BuildURL(tt.scheme, tt.slug, tt.project, tt.proxyPort)
			if got != tt.want {
				t.Errorf("BuildURL(%q, %q, %q, %d) = %q, want %q", tt.scheme, tt.slug, tt.project, tt.proxyPort, got, tt.want)
			}
		})
	}
}

// TestBuildURLFitsTheHostLabel guards the URL constructor itself: an unfitted
// slug reaching it (from a caller that forgot git.HostLabel) must still produce a
// URL a browser can resolve, and must agree with what the proxy matches on.
func TestBuildURLFitsTheHostLabel(t *testing.T) {
	raw := "dependabot-npm-and-yarn-services-manager-dashboard-ai-sdk-react-4-0-40" // 70 bytes
	got := BuildURL("https", raw, "mono", 3000)

	host, _, ok := strings.Cut(strings.TrimPrefix(got, "https://"), ":")
	if !ok {
		t.Fatalf("BuildURL returned %q, expected a host:port", got)
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) > 63 {
			t.Errorf("BuildURL(%q, ...) = %q, whose label %q is %d bytes; a browser will not resolve it",
				raw, got, label, len(label))
		}
	}
	// Fitting is idempotent, so a label already within the limit is untouched.
	if again := BuildURL("https", strings.Split(host, ".")[0], "mono", 3000); again != got {
		t.Errorf("BuildURL is not idempotent: %q then %q", got, again)
	}
}
