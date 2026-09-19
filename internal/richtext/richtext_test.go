package richtext

import (
	"strings"
	"testing"
)

func TestWhatAnAskNeedsSurvives(t *testing.T) {
	in := `<p>Running on <a href="http://localhost:5173/login">localhost:5173/login</a>. ` +
		`The button says <code>Save</code>. What changed:</p><ul><li>the <strong>grid</strong> matches</li></ul>`
	out := Clean(in)

	for _, want := range []string{"<p>", "<a href=", "localhost:5173/login", "<code>Save</code>", "<ul>", "<li>", "<strong>grid</strong>"} {
		if !strings.Contains(out, want) {
			t.Fatalf("%q was dropped from %q", want, out)
		}
	}
}

func TestScriptsAndHandlersNeverRender(t *testing.T) {
	cases := []string{
		`<script>fetch("/v1/events/1/replies",{method:"POST"})</script>`,
		`<img src=x onerror="alert(1)">`,
		`<a href="javascript:alert(1)">click</a>`,
		`<iframe src="http://evil.test"></iframe>`,
		`<style>body{display:none}</style>`,
		`<p onclick="steal()">text</p>`,
	}
	for _, in := range cases {
		out := Clean(in)
		for _, forbidden := range []string{"<script", "<iframe", "<style", "onerror", "onclick", "javascript:"} {
			if strings.Contains(strings.ToLower(out), forbidden) {
				t.Fatalf("%q survived sanitizing of %q", forbidden, in)
			}
		}
	}
}

// A description that is only plain text must come out readable. It is now
// wrapped in a paragraph, because it is read as Markdown first — before #45 it
// came back untouched and the page rendered it as one block.
func TestPlainTextBecomesAParagraph(t *testing.T) {
	in := "the migration fails on the test database, I am looking at the unique constraint"
	if got, want := Clean(in), "<p>"+in+"</p>"; got != want {
		t.Fatalf("Clean(%q) = %q, want %q", in, got, want)
	}
}

// Anything outside the list is shown as what it is, rather than vanishing.
func TestUnknownTagsBecomeTheirText(t *testing.T) {
	out := Clean(`<h1>Heading</h1><blockquote>quoted</blockquote>`)
	if strings.Contains(out, "<h1") || strings.Contains(out, "<blockquote") {
		t.Fatalf("an unlisted tag rendered: %q", out)
	}
	for _, want := range []string{"Heading", "quoted"} {
		if !strings.Contains(out, want) {
			t.Fatalf("%q was lost entirely: %q", want, out)
		}
	}
}

func TestExternalLinksLeaveThePage(t *testing.T) {
	out := Clean(`<a href="https://github.com/acme/app/issues/142">#142</a>`)
	for _, want := range []string{`target="_blank"`, `rel=`, "noreferrer"} {
		if !strings.Contains(out, want) {
			t.Fatalf("%q missing from %q", want, out)
		}
	}
}

// Sessions write Markdown — in issues, in commits, and here. Seven descriptions
// out of eight in the first live database carried no markup at all: blank lines
// and asterisks that HTML swallowed, leaving the PO a wall of text. Issue #45.
func TestMarkdownIsWhatSessionsActuallyWrite(t *testing.T) {
	out := Clean(`Audit published on the issue. Three points that change the scope:

**Half the issue is delivered.** ` + "`just img-gen`" + ` and the sidecar arrived with #1543.

Two premises moved.`)

	if n := strings.Count(out, "<p>"); n != 3 {
		t.Fatalf("paragraphs = %d, want 3 — blank lines are what carried the structure:\n%s", n, out)
	}
	if !strings.Contains(out, "<strong>Half the issue is delivered.</strong>") {
		t.Fatalf("bold was left as asterisks: %q", out)
	}
	if !strings.Contains(out, "<code>just img-gen</code>") {
		t.Fatalf("code was left as backticks: %q", out)
	}
}

// A single newline breaks the line: a session writing a short list of points
// without blank lines between them still gets them apart.
func TestASingleNewlineBreaksTheLine(t *testing.T) {
	out := Clean("first point\nsecond point")
	if !strings.Contains(out, "<br>") {
		t.Fatalf("no line break: %q", out)
	}
}

func TestMarkdownListsAndLinks(t *testing.T) {
	out := Clean("- one\n- two\n\nSee https://example.com/x for the rest.")
	if strings.Count(out, "<li>") != 2 {
		t.Fatalf("list items lost: %q", out)
	}
	if !strings.Contains(out, `href="https://example.com/x"`) {
		t.Fatalf("a bare URL was not made a link: %q", out)
	}
}

// A fenced block is the ordinary case here — a command, a log line — and it
// renders as pre+code, so pre has to survive or the block loses its shape.
func TestAFencedBlockKeepsItsShape(t *testing.T) {
	out := Clean("run it:\n\n```\njust deploy\n```")
	if !strings.Contains(out, "<pre>") || !strings.Contains(out, "just deploy") {
		t.Fatalf("fenced block lost: %q", out)
	}
}

// Markdown is rendered first and sanitized second, never the other way round:
// a tag the renderer produces has to be checked too. Nothing a session writes —
// in Markdown or in raw HTML — may reach the page unsanitized.
func TestSanitizingComesAfterRendering(t *testing.T) {
	for _, in := range []string{
		`<script>alert(1)</script>`,
		"text\n\n<script>alert(1)</script>",
		`[click](javascript:alert(1))`,
		`<img src=x onerror="alert(1)">`,
		"```\n<script>alert(1)</script>\n```",
	} {
		out := strings.ToLower(Clean(in))
		for _, forbidden := range []string{"<script", "onerror", "javascript:"} {
			if strings.Contains(out, forbidden) {
				t.Fatalf("%q survived %q", forbidden, in)
			}
		}
	}
}
