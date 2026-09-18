package issueref

import "testing"

func TestLabel(t *testing.T) {
	for _, c := range []struct{ in, want, why string }{
		{"", "", "nothing to show"},
		{"https://github.com/acme/app/issues/1886", "#1886", "the reported case"},
		{"https://github.com/acme/app/issues/1886/", "#1886", "a trailing slash"},
		{"https://github.com/acme/app/issues/1886#issuecomment-57", "#1886", "a comment anchor"},
		{"https://github.com/acme/app/issues/1886?x=1", "#1886", "a query"},
		{"142", "#142", "a row written before full URLs were required"},
		{"#142", "#142", "the same, already carrying its hash"},
		// Not an issue reference: shown as it stands, so it cannot be mistaken
		// for one, and no number is invented.
		{"https://github.com/acme/app/milestones", "https://github.com/acme/app/milestones", "not an issue url"},
		{"https://example.com/", "https://example.com/", "no segment at all"},
	} {
		if got := Label(c.in); got != c.want {
			t.Errorf("Label(%q) = %q, want %q — %s", c.in, got, c.want, c.why)
		}
	}
}

func TestURL(t *testing.T) {
	full := "https://github.com/acme/app/issues/1886"
	if got := URL(full); got != full {
		t.Errorf("URL(%q) = %q", full, got)
	}
	// Nothing can turn a bare number into an address; see ADR on issue #14.
	for _, bare := range []string{"", "142", "#142"} {
		if got := URL(bare); got != "" {
			t.Errorf("URL(%q) = %q, want none", bare, got)
		}
	}
}
