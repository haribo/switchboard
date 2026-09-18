package wake

import (
	"testing"
	"time"

	"switchboard/internal/store"
)

var base = time.Date(2026, 9, 17, 10, 0, 0, 0, time.UTC)

func at(d time.Duration) time.Time { return base.Add(d) }

func ptr(t time.Time) *time.Time { return &t }

// item builds one pending thing. Urgency and wording are separate now: a
// blocked dev and a rewording request both shorten the delay, and the signal
// names them apart.
func item(d time.Duration, urgent bool) store.Item {
	kind := store.ItemAsk
	if urgent {
		kind = store.ItemStop
	}
	return store.Item{At: at(d), Urgent: urgent, Kind: kind}
}

func reword(d time.Duration) store.Item {
	return store.Item{At: at(d), Urgent: true, Kind: store.ItemReword}
}

func TestNothingPendingNeverSignals(t *testing.T) {
	if _, due, _ := Due(nil, store.Cursor{}, Default, at(time.Hour)); due {
		t.Fatal("a signal went out with nothing to signal")
	}
}

func TestDebounceHoldsTheFirstEvent(t *testing.T) {
	items := []store.Item{item(0, false)}

	_, due, next := Due(items, store.Cursor{}, Default, at(2*time.Minute))
	if due {
		t.Fatal("signalled before the debounce window closed")
	}
	if want := at(Default.Debounce); !next.Equal(want) {
		t.Fatalf("next check = %v, want %v", next, want)
	}

	if _, due, _ := Due(items, store.Cursor{}, Default, at(Default.Debounce)); !due {
		t.Fatal("no signal once the debounce window closed")
	}
}

// The whole point: a burst costs one interruption, and the manager is told how
// many, never what.
func TestBurstIsOneSignal(t *testing.T) {
	items := []store.Item{item(0, false), item(30*time.Second, false), item(time.Minute, false)}

	batch, due, _ := Due(items, store.Cursor{}, Default, at(5*time.Minute))
	if !due {
		t.Fatal("no signal for three pending events")
	}
	if batch.Count != 3 {
		t.Fatalf("count = %d, want 3", batch.Count)
	}
	if got, want := batch.Line(), "3 events to handle"; got != want {
		t.Fatalf("line = %q, want %q", got, want)
	}
	if !batch.Oldest.Equal(at(0)) {
		t.Fatalf("oldest = %v, want %v", batch.Oldest, at(0))
	}
	if !batch.Through.Equal(at(time.Minute)) {
		t.Fatalf("through = %v, want %v", batch.Through, at(time.Minute))
	}
}

// A batch that was announced but not yet dealt with must not be announced again.
func TestAlreadySignalledStaysQuiet(t *testing.T) {
	items := []store.Item{item(0, false), item(time.Minute, false)}
	cur := store.Cursor{SignaledAt: ptr(at(5 * time.Minute)), SignaledThrough: ptr(at(time.Minute))}

	if _, due, _ := Due(items, cur, Default, at(2*time.Hour)); due {
		t.Fatal("the same pending events raised a second signal")
	}
}

func TestRateLimitHoldsANewEventBack(t *testing.T) {
	items := []store.Item{item(0, false), item(6*time.Minute, false)}
	cur := store.Cursor{SignaledAt: ptr(at(5 * time.Minute)), SignaledThrough: ptr(at(0))}

	// Debounce on the new event has closed at 9m, but the floor is 5m + 5m.
	_, due, next := Due(items, cur, Default, at(9*time.Minute))
	if due {
		t.Fatal("signalled inside the rate limit")
	}
	if want := at(10 * time.Minute); !next.Equal(want) {
		t.Fatalf("next check = %v, want %v", next, want)
	}

	batch, due, _ := Due(items, cur, Default, at(10*time.Minute))
	if !due {
		t.Fatal("no signal once the rate limit lifted")
	}
	// Both are still pending, so the manager is told two, not one.
	if batch.Count != 2 {
		t.Fatalf("count = %d, want 2", batch.Count)
	}
}

func TestBlockedShortensTheWaitButNotTheRateLimit(t *testing.T) {
	blocked := []store.Item{item(0, true)}

	if _, due, _ := Due(blocked, store.Cursor{}, Default, at(45*time.Second)); !due {
		t.Fatal("a stopped dev waited the full debounce")
	}

	// Urgency buys an earlier signal, never one inside the rate limit.
	cur := store.Cursor{SignaledAt: ptr(at(0)), SignaledThrough: ptr(at(-time.Second))}
	if _, due, next := Due(blocked, cur, Default, at(time.Minute)); due {
		t.Fatal("a stopped dev broke the rate limit")
	} else if want := at(Default.MinInterval); !next.Equal(want) {
		t.Fatalf("next check = %v, want %v", next, want)
	}
}

func TestBlockedIsCountedInTheLine(t *testing.T) {
	items := []store.Item{item(0, false), item(0, true), item(0, true)}
	batch, due, _ := Due(items, store.Cursor{}, Default, at(time.Hour))
	if !due {
		t.Fatal("no signal")
	}
	if got, want := batch.Line(), "3 events to handle, 2 blocked"; got != want {
		t.Fatalf("line = %q, want %q", got, want)
	}
}

func TestOneEventReadsSingular(t *testing.T) {
	batch, _, _ := Due([]store.Item{item(0, true)}, store.Cursor{}, Default, at(time.Hour))
	if got, want := batch.Line(), "1 event to handle, 1 blocked"; got != want {
		t.Fatalf("line = %q, want %q", got, want)
	}
}

// A non-urgent event landing after a blocked one was signalled waits the full
// debounce: the urgency belonged to the event, not to the queue.
func TestUrgencyDoesNotLeakToLaterEvents(t *testing.T) {
	items := []store.Item{item(0, true), item(10*time.Minute, false)}
	cur := store.Cursor{SignaledAt: ptr(at(time.Minute)), SignaledThrough: ptr(at(0))}

	if _, due, _ := Due(items, cur, Default, at(11*time.Minute)); due {
		t.Fatal("a plain event inherited the blocked debounce")
	}
	if _, due, _ := Due(items, cur, Default, at(13*time.Minute)); !due {
		t.Fatal("no signal after the plain debounce closed")
	}
}

// A rewording request shortens the delay like a blocked dev — somebody is
// stopped in front of a page — but the line must not call it blocked, or the
// manager goes hunting for a stopped session that does not exist. Issue #41.
func TestARewordingRequestIsNamedApartFromABlockedDev(t *testing.T) {
	batch, due, _ := Due([]store.Item{reword(0)}, store.Cursor{}, Default, at(45*time.Second))
	if !due {
		t.Fatal("a rewording request waited the full batching delay")
	}
	if got, want := batch.Line(), "1 event to handle, 1 to reword"; got != want {
		t.Fatalf("line = %q, want %q", got, want)
	}

	mixed := []store.Item{item(0, true), reword(0), item(0, false)}
	batch, _, _ = Due(mixed, store.Cursor{}, Default, at(time.Hour))
	if got, want := batch.Line(), "3 events to handle, 1 blocked, 1 to reword"; got != want {
		t.Fatalf("line = %q, want %q", got, want)
	}
}
