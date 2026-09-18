// Package issueref decides how an issue is shown and where it points.
//
// Two renderings need this — the PO's page and the manager's board — and they
// diverged once already: the page was showing `#1886` while the board printed
// `#https://github.com/acme/app/issues/1886`. One definition, two callers.
package issueref

import (
	"path"
	"strings"
)

// Label is the reference to print: `#1886`.
//
// A `#` in front of a number reads as an issue reference; in front of an address
// it means nothing and swallows the line. So the number is taken from the URL's
// last segment — and only when that segment really is a number. Anything else is
// shown as it stands, with no `#`: an address that was not understood must look
// like one, not like a reference to issue "milestones".
func Label(issue string) string {
	if issue == "" {
		return ""
	}
	if !IsURL(issue) {
		// A row written before full URLs were required. Its value is a number.
		return "#" + strings.TrimPrefix(issue, "#")
	}
	if n, ok := number(issue); ok {
		return "#" + n
	}
	return issue
}

// URL is the address to link to, or "" when there is none.
func URL(issue string) string {
	if IsURL(issue) {
		return issue
	}
	return ""
}

// IsURL reports whether a value is an address rather than a bare number.
func IsURL(s string) bool {
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://")
}

// number returns the last path segment when it is entirely digits.
func number(u string) (string, bool) {
	// Drop a query or fragment: .../issues/1886#issuecomment-5 is still 1886.
	if i := strings.IndexAny(u, "?#"); i >= 0 {
		u = u[:i]
	}
	last := path.Base(strings.TrimRight(u, "/"))
	if last == "" || last == "." || last == "/" {
		return "", false
	}
	for _, r := range last {
		if r < '0' || r > '9' {
			return "", false
		}
	}
	return last, true
}
