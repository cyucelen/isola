// Package expand provides ${VAR} interpolation shared between service env
// building and accessory drivers, so substitution behaves identically
// everywhere.
package expand

import "strings"

// Braces expands ${VAR} references in s using mapping, leaving bare "$"
// characters untouched. Unlike os.Expand, the bare "$VAR" form is NOT
// interpreted, so config values containing a literal "$" (e.g. passwords like
// "p$ssw0rd") are preserved exactly. A malformed reference with no closing
// brace is left as-is.
func Braces(s string, mapping func(string) string) string {
	if !strings.Contains(s, "${") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		if s[i] == '$' && i+1 < len(s) && s[i+1] == '{' {
			if end := strings.IndexByte(s[i+2:], '}'); end >= 0 {
				b.WriteString(mapping(s[i+2 : i+2+end]))
				i += 2 + end + 1
				continue
			}
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}
