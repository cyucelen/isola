package proxy

import (
	_ "embed"
	"encoding/base64"
	"html"
	"net/http"
	"regexp"
	"strings"
)

//go:embed logo-dark.png
var logoPNG []byte

// logoDataURI is the embedded logo as an inline data URI, so the error page is
// fully self-contained (no asset fetch).
var logoDataURI = "data:image/png;base64," + base64.StdEncoding.EncodeToString(logoPNG)

// faviconDataURI is a tiny self-contained SVG favicon (a palm, nodding to the
// isola island mark). base64 so it needs no URL escaping.
var faviconDataURI = "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString(
	[]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 100"><text y=".9em" font-size="90">🌴</text></svg>`))

// backtickRe matches `code` spans in a detail message.
var backtickRe = regexp.MustCompile("`([^`]+)`")

// writeProxyError responds with a branded, minimal HTML page for browsers and
// plain text for everything else (curl, agents, fetch). detail is untrusted (it
// echoes the request Host), so it is HTML-escaped for the HTML variant.
func writeProxyError(w http.ResponseWriter, r *http.Request, status int, title, detail string) {
	if prefersHTML(r) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(renderErrorPage(title, detail)))
		return
	}
	http.Error(w, "isola: "+detail, status)
}

// prefersHTML reports whether the client asked for HTML (a browser) rather than
// a programmatic client that should get plain text.
func prefersHTML(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept"), "text/html")
}

// renderErrorPage fills the template by token replacement (not fmt), so the
// CSS's literal % signs need no escaping. Replacement values are never
// re-scanned, so an untrusted detail cannot inject a placeholder.
func renderErrorPage(title, detail string) string {
	return strings.NewReplacer(
		"__FAVICON__", faviconDataURI,
		"__LOGO__", logoDataURI,
		"__TITLE__", html.EscapeString(title),
		"__DETAIL__", formatDetail(detail),
	).Replace(errorPageTmpl)
}

// formatDetail escapes the untrusted detail, then turns `code` spans into
// <code> elements. Escaping happens first, so <code> only ever wraps
// already-safe text.
func formatDetail(detail string) string {
	esc := html.EscapeString(detail)
	return backtickRe.ReplaceAllStringFunc(esc, func(m string) string {
		return "<code>" + m[1:len(m)-1] + "</code>"
	})
}

// errorPageTmpl is a self-contained, minimal page: the isola mark on the same
// #0c1117 ground, terminal-style monospace type, muted #878d95 text, left
// aligned, no chrome. Placeholders: __LOGO__ / __TITLE__ / __DETAIL__.
const errorPageTmpl = `<!doctype html>
<html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<link rel="icon" href="__FAVICON__">
<title>isola &middot; __TITLE__</title>
<style>
:root{color-scheme:dark}
*{box-sizing:border-box}
html,body{height:100%}
body{margin:0;display:flex;align-items:center;justify-content:center;
background:#0c1117;color:#878d95;padding:2rem;
font:14px/1.7 Menlo,Monaco,"DejaVu Sans Mono","Cascadia Mono",Consolas,"Courier New",monospace}
.wrap{width:33rem;max-width:100%;display:flex;flex-direction:column;align-items:flex-start;gap:.5rem;text-align:left}
img{width:132px;height:auto;display:block}
.msg{display:flex;flex-direction:column;gap:.5rem}
h1{margin:0;font-size:.95rem;font-weight:600;color:#878d95}
p{margin:0;color:#878d95;white-space:pre-wrap;word-break:break-word}
code{color:#c9d1d9;background:rgba(255,255,255,.055);padding:.08em .38em;border-radius:5px}
</style></head>
<body><main class="wrap">
<img src="__LOGO__" alt="isola">
<div class="msg">
<h1>__TITLE__</h1>
<p>__DETAIL__</p>
</div>
</main></body></html>
`
