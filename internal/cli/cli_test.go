package cli

import (
	"flag"
	"strings"
	"testing"
	"time"
)

// A dev writes "switchboard await 12 --timeout 10m", not the other way round.
func TestFlagsAreReadOnEitherSideOfTheArgument(t *testing.T) {
	for _, args := range [][]string{
		{"12", "--timeout", "10m"},
		{"--timeout", "10m", "12"},
		{"--timeout=10m", "12"},
	} {
		fs := flag.NewFlagSet("await", flag.ContinueOnError)
		timeout := fs.Duration("timeout", time.Hour, "")
		rest := parse(fs, args)

		if len(rest) != 1 || rest[0] != "12" {
			t.Fatalf("%v: positional = %v, want [12]", args, rest)
		}
		if *timeout != 10*time.Minute {
			t.Fatalf("%v: timeout = %v, want 10m", args, *timeout)
		}
	}
}

func TestAStrayArgumentIsRefused(t *testing.T) {
	fs := flag.NewFlagSet("board", flag.ContinueOnError)
	fs.Bool("json", false, "")
	if err := parseNoArgs(fs, []string{"--json", "oops"}); err == nil {
		t.Fatal("a stray argument was swallowed silently")
	}
	if err := parseNoArgs(fs, []string{"--json"}); err != nil {
		t.Fatalf("a valid call was refused: %v", err)
	}
}

func TestOneIDRejectsWhatIsNotAnID(t *testing.T) {
	if _, err := oneID([]string{"douze"}); err == nil {
		t.Fatal("a non-numeric id was accepted")
	}
	if _, err := oneID(nil); err == nil {
		t.Fatal("a missing id was accepted")
	}
	id, err := oneID([]string{"12"})
	if err != nil || id != 12 {
		t.Fatalf("oneID = %d, %v", id, err)
	}
}

// A dead service must not look like a quiet one: the watch says so on standard
// output, once, and says it again only after a recovery.
func TestAnOutageIsAnnouncedOnceAndOnlyAfterTheDelay(t *testing.T) {
	var o outage
	start := time.Date(2026, 9, 17, 10, 0, 0, 0, time.UTC)
	after := 2 * time.Minute

	if line := o.report(start, after); line != "" {
		t.Fatalf("announced at the first failure: %q", line)
	}
	if line := o.report(start.Add(time.Minute), after); line != "" {
		t.Fatalf("announced before the delay: %q", line)
	}
	line := o.report(start.Add(2*time.Minute), after)
	if line == "" {
		t.Fatal("an outage past the delay was never announced")
	}
	if !strings.Contains(line, "unreachable") {
		t.Fatalf("line = %q", line)
	}
	if again := o.report(start.Add(10*time.Minute), after); again != "" {
		t.Fatalf("the same outage was announced twice: %q", again)
	}

	// The service comes back, then falls over again: that is news.
	o.clear()
	o.report(start.Add(20*time.Minute), after)
	if line := o.report(start.Add(23*time.Minute), after); line == "" {
		t.Fatal("a second outage went unannounced")
	}
}
