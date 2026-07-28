// Package slug derives the names isola gives per-worktree resources — proxy
// hostnames, log files, database names — from a worktree's identity.
//
// The identity is a git ref, which has no length bound, while the things named
// after it do: a Postgres identifier caps at 63 bytes, a DNS label at 63, a
// path component at 255. Fit shortens a slug against a caller-supplied budget
// without letting two distinct identities collapse onto the same name, which
// plain truncation would do: automated branches (`dependabot/npm_and_yarn/…`)
// share a long constant prefix and carry their only distinguishing part, the
// version, at the very end — exactly what truncation discards.
package slug

import (
	"hash/fnv"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	// hashLen is how many base36 characters of the identity's hash a shortened
	// slug carries. 36^8 ≈ 2.8e12, so the chance any two of a thousand
	// worktrees collide is about 2e-7 — negligible, for 8 bytes of budget.
	hashLen = 8
	// elision marks where the middle of a slug was cut. Runs of non-alphanumerics
	// collapse to a single hyphen in Make, so a double hyphen never occurs
	// naturally and unambiguously reads as "something was removed here".
	elision = "--"
	// minSplit is the smallest readable budget worth spending on both ends of the
	// slug: two characters either side of the elision. Below it, Fit keeps only
	// the head.
	minSplit = 2 + len(elision) + 2
	// MinFit is the smallest budget Fit can honour while keeping any of the
	// identity readable: the hash, its separator, and two characters of slug.
	// Callers with a smaller budget should report it rather than name a resource
	// after a bare hash.
	MinFit = hashLen + 1 + 2
)

var nonAlphaNum = regexp.MustCompile(`[^a-zA-Z0-9]+`)

// Make converts an identity (usually a branch name) into a lowercase slug of
// alphanumerics and hyphens — safe in a URL, a filename, and a double-quoted
// SQL identifier alike. It imposes no length bound; callers that have one pass
// the result to Fit.
func Make(s string) string {
	return strings.ToLower(strings.Trim(nonAlphaNum.ReplaceAllString(s, "-"), "-"))
}

// Fit returns s when it already fits maxBytes, and otherwise elides its middle
// and appends a hash of the whole input:
//
//	dependabot-npm-and-yarn-se--tersection-observer-10-1-0-8l5fsz8a
//
// The hash covers the untruncated input, so identities that differ only in what
// was cut still get different names. Both ends are kept because which one
// identifies a worktree depends on the naming scheme, and whoever reads
// `psql -l` or a log filename needs to map the name back to a worktree.
//
// Budgets are counted in bytes (Postgres and DNS both count bytes) and cuts
// never split a UTF-8 sequence. With a budget below MinFit there is no room for
// a readable part and the hash alone is returned, truncated if even that does
// not fit; the result is always at most maxBytes.
func Fit(s string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(s) <= maxBytes {
		return s
	}

	h := fingerprint(s)
	if maxBytes < MinFit {
		return headBytes(h, maxBytes)
	}

	readable := maxBytes - hashLen - 1 // -1 for the hash separator
	var text string
	if readable >= minSplit {
		head := (readable - len(elision) + 1) / 2
		tail := readable - len(elision) - head
		text = trimHyphens(headBytes(s, head)) + elision + trimHyphens(tailBytes(s, tail))
	} else {
		text = trimHyphens(headBytes(s, readable))
	}
	return text + "-" + h
}

// fingerprint is the stable base36 digest Fit appends. It must never change:
// resource names are re-derived on every `isola up`, so a different digest would
// orphan the database a worktree already has and provision a fresh one.
func fingerprint(s string) string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(s))
	const space = 36 * 36 * 36 * 36 * 36 * 36 * 36 * 36 // 36^hashLen
	d := strconv.FormatUint(h.Sum64()%space, 36)
	if len(d) < hashLen {
		d = strings.Repeat("0", hashLen-len(d)) + d
	}
	return d
}

// headBytes returns the longest prefix of s that is at most n bytes without
// splitting a UTF-8 sequence.
func headBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// tailBytes returns the longest suffix of s that is at most n bytes without
// splitting a UTF-8 sequence.
func tailBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	i := len(s) - n
	for i < len(s) && !utf8.RuneStart(s[i]) {
		i++
	}
	return s[i:]
}

// trimHyphens drops hyphens left dangling at a cut, so an elision reads as a
// single "--" rather than a run of hyphens.
func trimHyphens(s string) string {
	return strings.Trim(s, "-")
}
