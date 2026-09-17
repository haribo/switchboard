// Package richtext decides what a description may contain.
//
// A description is written by a session, and a session sometimes relays writing
// from elsewhere — an issue comment, a log line. It reaches a page the PO clicks
// on, so it is never rendered as given: only a short list of tags survives, and
// everything else is shown as the text it is.
package richtext

import (
	"github.com/microcosm-cc/bluemonday"
)

// allowed is built once: the policy is the decision, not a per-call choice.
var allowed = policy()

func policy() *bluemonday.Policy {
	p := bluemonday.NewPolicy()

	// Enough to write a readable ask: a link, emphasis, code, a list.
	p.AllowElements("p", "br", "strong", "b", "em", "i", "code", "ul", "ol", "li")

	// Links carry the point of the whole feature — an address the PO can open
	// without retyping it.
	p.AllowAttrs("href").OnElements("a")
	p.AllowURLSchemes("http", "https", "mailto")
	p.RequireParseableURLs(true)
	// The PO's page must never be the thing that gets replaced.
	p.AddTargetBlankToFullyQualifiedLinks(true)
	p.AddSpaceWhenStrippingTag(true)
	p.RequireNoReferrerOnFullyQualifiedLinks(true)

	return p
}

// Clean returns the description as it may be rendered. Anything outside the
// list survives as text, so a description is never silently emptied.
func Clean(html string) string {
	return allowed.Sanitize(html)
}
