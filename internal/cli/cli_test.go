package cli

import (
	"flag"
	"os"
	"regexp"
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

var base = time.Date(2026, 9, 18, 11, 0, 0, 0, time.UTC)

// A restart is not an outage. Announcing the loss the moment a connection
// breaks cries at every deploy, and teaches its reader to ignore the line — so
// the day the service really stays down, it reads like all the others.
// Issue #18.
func TestARestartProducesNoLineAtAll(t *testing.T) {
	var o outage
	at := func(d time.Duration) time.Time { return base.Add(d) }
	const after = 2 * time.Minute

	// A deploy: the connection breaks, a few attempts fail, it comes back.
	for _, d := range []time.Duration{0, 5 * time.Second, 10 * time.Second} {
		if line := o.failed(at(d), after); line != "" {
			t.Fatalf("announced %q after %s — a restart must produce nothing", line, d)
		}
	}
	if line := o.recovered(at(12 * time.Second)); line != "" {
		t.Fatalf("announced a recovery from an outage never announced: %q", line)
	}
}

// A service that stays down is announced once, after the delay.
func TestAnOutageIsAnnouncedOnceAfterTheDelay(t *testing.T) {
	var o outage
	at := func(d time.Duration) time.Time { return base.Add(d) }
	const after = 2 * time.Minute

	if line := o.failed(at(0), after); line != "" {
		t.Fatalf("announced at the first failure: %q", line)
	}
	if line := o.failed(at(time.Minute), after); line != "" {
		t.Fatalf("announced before the delay: %q", line)
	}
	line := o.failed(at(2*time.Minute), after)
	if line == "" {
		t.Fatal("an outage past the delay was never announced")
	}
	if !strings.Contains(line, "unreachable") {
		t.Fatalf("line = %q", line)
	}
	if again := o.failed(at(10*time.Minute), after); again != "" {
		t.Fatalf("the same outage was announced twice: %q", again)
	}
}

// A reader told the line is dead needs to be told it is alive again, or they go
// on believing the outage is still running.
func TestComingBackIsAnnouncedOnlyIfTheOutageWas(t *testing.T) {
	var o outage
	at := func(d time.Duration) time.Time { return base.Add(d) }
	const after = 2 * time.Minute

	o.failed(at(0), after)
	o.failed(at(3*time.Minute), after) // announced here
	back := o.recovered(at(5 * time.Minute))
	if back == "" {
		t.Fatal("the service came back and nothing said so")
	}
	if !strings.Contains(back, "answering again") {
		t.Fatalf("recovery line = %q — it has to say the service is alive", back)
	}
	// And a second success says nothing more.
	if again := o.recovered(at(6 * time.Minute)); again != "" {
		t.Fatalf("recovery announced twice: %q", again)
	}

	// A fresh outage afterwards is announced again.
	o.failed(at(10*time.Minute), after)
	if line := o.failed(at(13*time.Minute), after); line == "" {
		t.Fatal("a second outage went unannounced")
	}
}

// The config file is never overwritten by an upgrade, so a setting the product
// dropped survives in it and is read by nobody. Nothing said so. Issue #16.
func TestStraySettingsAreNamed(t *testing.T) {
	env := []string{
		"SWITCHBOARD_ADDR=127.0.0.1:8787",  // known
		"SWITCHBOARD_UNDO_WINDOW=10s",      // known
		"SWITCHBOARD_URL=http://127.0.0.1", // the client's, legitimately here
		"SWITCHBOARD_REPO=",                // dropped by #14
		"SWITCHBOARD_DEBOUCE=10m",          // a typo for DEBOUNCE
		"SWITCHBOARD_WHATEVER=1",           // never existed
		"PATH=/usr/bin",                    // none of our business
	}
	unknown, retired := straySettings(env)

	if len(retired) != 1 || !strings.HasPrefix(retired[0], "SWITCHBOARD_REPO") {
		t.Fatalf("retired = %v, want SWITCHBOARD_REPO named apart", retired)
	}
	if !strings.Contains(retired[0], "full URL") {
		t.Fatalf("retired = %v, does not say why it is gone", retired)
	}
	want := []string{"SWITCHBOARD_DEBOUCE", "SWITCHBOARD_WHATEVER"}
	if len(unknown) != len(want) {
		t.Fatalf("unknown = %v, want %v", unknown, want)
	}
	for i, w := range want {
		if unknown[i] != w {
			t.Fatalf("unknown = %v, want %v (sorted, so a report does not shuffle)", unknown, want)
		}
	}
}

func TestKnownSettingsSayNothing(t *testing.T) {
	var env []string
	for name := range knownSettings {
		env = append(env, name+"=x")
	}
	unknown, retired := straySettings(env)
	if len(unknown) != 0 || len(retired) != 0 {
		t.Fatalf("a fully known environment reported %v / %v", unknown, retired)
	}
}

// Every setting the serve command reads has to be in the known list, or it
// reports itself as unknown the moment somebody sets it.
func TestEverySettingTheServiceReadsIsKnown(t *testing.T) {
	source, err := os.ReadFile("cli.go")
	if err != nil {
		t.Fatalf("read cli.go: %v", err)
	}
	// Not preceded by a letter, so os.Getenv("SWITCHBOARD_" + key) is not a hit.
	used := regexp.MustCompile(`[^A-Za-z]env(?:Duration)?\("([A-Z_]+)"`).FindAllStringSubmatch(string(source), -1)
	if len(used) == 0 {
		t.Fatal("found no settings at all — has the reading changed shape?")
	}
	for _, m := range used {
		name := "SWITCHBOARD_" + m[1]
		if !knownSettings[name] {
			t.Errorf("%s is read by the service but missing from knownSettings", name)
		}
	}
}

// While the service is missing, the caller must stop blocking on it: the long
// poll holds a connection for minutes, so a service coming back would not be
// noticed until it expired — and the reader would go on believing the outage
// still runs. Issue #18.
func TestAMissingServiceIsProbedWithoutBlocking(t *testing.T) {
	var o outage
	at := func(d time.Duration) time.Time { return base.Add(d) }

	if o.ongoing() {
		t.Fatal("a healthy service reports as missing")
	}
	o.failed(at(0), time.Minute)
	if !o.ongoing() {
		t.Fatal("a broken connection does not put the caller in probing mode")
	}
	o.recovered(at(10 * time.Second))
	if o.ongoing() {
		t.Fatal("still probing after the service came back")
	}
}
