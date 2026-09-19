// Package richtext decides what a description may contain, and turns what its
// callers actually write into it.
//
// Sessions write Markdown. They write it in issues, in commits, in every other
// place they type, and they wrote it here too — seven descriptions out of eight
// in the first live database carried no markup at all, just blank lines and
// asterisks that HTML swallowed. Demanding HTML of them would be demanding that
// they stop writing the way they write.
//
// So a description is parsed as Markdown, then sanitized: only a short list of
// tags survives, and everything else is shown as the text it is. A description
// is written by a session, and a session sometimes relays writing from
// elsewhere — an issue comment, a log line — onto a page the PO clicks on.
package richtext

import (
	"bytes"
	"strings"

	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/renderer/html"
)

// allowed is built once: the policy is the decision, not a per-call choice.
var allowed = policy()

func policy() *bluemonday.Policy {
	p := bluemonday.NewPolicy()

	// Enough to write a readable ask: a link, emphasis, code, a list. `pre`
	// comes with `code` because a fenced block renders as both, and a session
	// pasting a command or a log line is the ordinary case here.
	p.AllowElements("p", "br", "strong", "b", "em", "i", "code", "pre", "ul", "ol", "li")

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

// md renders Markdown the way a session writes it: a blank line starts a
// paragraph, a single newline breaks the line. Raw HTML in the source is left
// for the sanitizer rather than dropped here, so nothing disappears silently.
var md = goldmark.New(
	goldmark.WithExtensions(extension.Linkify),
	goldmark.WithRendererOptions(
		html.WithHardWraps(),
		html.WithUnsafe(),
	),
)

// Clean turns a description into what may be rendered.
//
// Markdown first, sanitizing second, and in that order: the sanitizer must be
// the last thing the text passes through, or a tag produced by the renderer
// would never be checked.
func Clean(source string) string {
	if strings.TrimSpace(source) == "" {
		return ""
	}
	var rendered bytes.Buffer
	if err := md.Convert([]byte(source), &rendered); err != nil {
		// Markdown has no parse errors worth the name, but a body that cannot
		// be rendered must still reach its reader: sanitize it as it came.
		return allowed.Sanitize(source)
	}
	return strings.TrimSpace(allowed.Sanitize(rendered.String()))
}
