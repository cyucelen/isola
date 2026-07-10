package proxy

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPrefersHTML(t *testing.T) {
	cases := map[string]bool{
		"text/html,application/xhtml+xml,*/*;q=0.9": true,
		"*/*":              false,
		"application/json": false,
		"":                 false,
	}
	for accept, want := range cases {
		r := httptest.NewRequest("GET", "/", nil)
		if accept != "" {
			r.Header.Set("Accept", accept)
		}
		if got := prefersHTML(r); got != want {
			t.Errorf("prefersHTML(Accept=%q) = %v, want %v", accept, got, want)
		}
	}
}

func TestWriteProxyErrorHTMLForBrowser(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Accept", "text/html")
	w := httptest.NewRecorder()
	writeProxyError(w, r, http.StatusNotFound, "Unknown project", "detail here")

	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "<html") || !strings.Contains(body, "Unknown project") {
		t.Errorf("HTML body missing structure/title:\n%s", body)
	}
}

func TestWriteProxyErrorPlainForAgents(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil) // no Accept => not a browser
	w := httptest.NewRecorder()
	writeProxyError(w, r, http.StatusNotFound, "Unknown project", "detail here")

	if ct := w.Header().Get("Content-Type"); strings.HasPrefix(ct, "text/html") {
		t.Errorf("agent response should be plain text, got %q", ct)
	}
	if body := w.Body.String(); strings.Contains(body, "<html") || !strings.Contains(body, "isola: detail here") {
		t.Errorf("plain body = %q, want 'isola: detail here' and no HTML", body)
	}
}

func TestErrorPageEscapesUntrustedInput(t *testing.T) {
	// The Host is attacker-controllable, so the detail must be HTML-escaped.
	page := renderErrorPage("Worktree not reachable", `no worktree "<script>alert(1)</script>"`)
	if strings.Contains(page, "<script>alert(1)</script>") {
		t.Error("untrusted detail was not escaped (reflected XSS)")
	}
	if !strings.Contains(page, "&lt;script&gt;") {
		t.Error("expected escaped form of the script tag")
	}
}

func TestFormatDetailCodeSpans(t *testing.T) {
	out := formatDetail("Run `isola up` in that repo.")
	if !strings.Contains(out, "<code>isola up</code>") {
		t.Errorf("backtick span not converted to <code>: %s", out)
	}
	// Dangerous content, inside and outside a code span, must stay escaped.
	danger := formatDetail("x <b>y</b> `<script>alert(1)</script>`")
	if strings.Contains(danger, "<b>") || strings.Contains(danger, "<script>") {
		t.Errorf("unescaped HTML leaked: %s", danger)
	}
	if !strings.Contains(danger, "<code>&lt;script&gt;alert(1)&lt;/script&gt;</code>") {
		t.Errorf("code content should be escaped: %s", danger)
	}
}
