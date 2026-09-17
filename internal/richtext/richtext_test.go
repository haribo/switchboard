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

// A description that is only plain text must come out readable, not empty.
func TestPlainTextIsLeftAlone(t *testing.T) {
	in := "the migration fails on the test database, I am looking at the unique constraint"
	if got := Clean(in); got != in {
		t.Fatalf("plain text came back as %q", got)
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
