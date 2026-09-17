// Package wake decides when the manager is told that something waits for them,
// and how often. It is the point of the whole tool: one grouped signal instead
// of one message per event, which is the defect switchboard exists to remove.
package wake

import (
	"fmt"
	"time"

	"switchboard/internal/store"
)

// Config tunes the batching.
type Config struct {
	// Debounce is how long a new item waits before a signal goes out, so that a
	// burst of events costs one interruption instead of five.
	Debounce time.Duration
	// MinInterval is the floor between two signals, whatever arrives.
	MinInterval time.Duration
	// Urgent is the debounce used when a dev is actually stopped. The rate limit
	// still applies: being blocked buys an earlier signal, never a louder one.
	Urgent time.Duration
}

// Default is what the server runs with unless told otherwise.
var Default = Config{
	Debounce:    3 * time.Minute,
	MinInterval: 5 * time.Minute,
	Urgent:      30 * time.Second,
}

// Batch is what a signal says. It carries counts, never content: the manager is
// told there is something to look at, and looks at it in one call.
type Batch struct {
	Count   int       `json:"count"`   // everything still pending, not just what is new
	Blocked int       `json:"blocked"` // how many of those are stopped devs
	Oldest  time.Time `json:"oldest"`  // when the oldest unsignalled item landed
	Through time.Time `json:"-"`       // newest item this signal covers
}

// Line is the one line a signal prints. One line is one notification, and one
// notification is what makes an idle session pick the work back up.
func (b Batch) Line() string {
	word := "event"
	if b.Count > 1 {
		word = "events"
	}
	s := fmt.Sprintf("%d %s to handle", b.Count, word)
	if b.Blocked > 0 {
		s += fmt.Sprintf(", %d blocked", b.Blocked)
	}
	return s
}

// Due reports whether a signal is owed right now for the given pending items.
//
// When it is not owed, the second return is false and the third says when the
// answer could change, so a caller can sleep until then instead of spinning. A
// zero time there means nothing is pending and only a new item can change it.
func Due(items []store.Item, cur store.Cursor, cfg Config, now time.Time) (Batch, bool, time.Time) {
	if len(items) == 0 {
		return Batch{}, false, time.Time{}
	}

	batch := Batch{Count: len(items)}
	var oldestFresh, through time.Time
	freshUrgent := false

	for _, it := range items {
		if it.Urgent {
			batch.Blocked++
		}
		if through.IsZero() || it.At.After(through) {
			through = it.At
		}
		// Anything at or before the cursor was already announced: signalling it
		// again would wake the manager for what they already know about.
		if cur.SignaledThrough != nil && !it.At.After(*cur.SignaledThrough) {
			continue
		}
		if oldestFresh.IsZero() || it.At.Before(oldestFresh) {
			oldestFresh = it.At
		}
		if it.Urgent {
			freshUrgent = true
		}
	}

	if oldestFresh.IsZero() {
		// Everything pending has already been announced.
		return Batch{}, false, time.Time{}
	}

	debounce := cfg.Debounce
	if freshUrgent {
		debounce = cfg.Urgent
	}
	readyAt := oldestFresh.Add(debounce)
	if cur.SignaledAt != nil {
		if floor := cur.SignaledAt.Add(cfg.MinInterval); floor.After(readyAt) {
			readyAt = floor
		}
	}
	if now.Before(readyAt) {
		return Batch{}, false, readyAt
	}

	batch.Oldest, batch.Through = oldestFresh, through
	return batch, true, time.Time{}
}
