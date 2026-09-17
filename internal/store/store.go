// Package store holds every durable fact switchboard owns: the living state of
// the sessions, the events addressed to a human, and the answers they got.
// Nothing here duplicates GitHub.
package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// ErrNotFound is returned when an id matches nothing.
var ErrNotFound = errors.New("not found")

// Clock lets tests move time without sleeping.
type Clock func() time.Time

// Store is the whole persistence layer. It is safe for concurrent use.
type Store struct {
	db  *sql.DB
	now Clock

	path string

	mu      sync.Mutex
	changed chan struct{} // closed and replaced on every mutation
}

// Path is the file this store is backed by.
func (s *Store) Path() string { return s.path }

// DB exposes the handle for the maintenance commands (backup, status).
func (s *Store) DB() *sql.DB { return s.db }

// DefaultPath is where the database lives when nothing says otherwise.
func DefaultPath() string {
	dir := os.Getenv("XDG_DATA_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "switchboard.db"
		}
		dir = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(dir, "switchboard", "switchboard.db")
}

// Open opens (and creates if needed) the database at path. A clock of nil means
// time.Now.
func Open(path string, now Clock) (*Store, error) {
	if now == nil {
		now = time.Now
	}
	if dir := filepath.Dir(path); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create %s: %w", dir, err)
		}
	}
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, err
	}
	// One writer at a time: SQLite serialises writes anyway, and this keeps the
	// long-polls from holding a connection each.
	db.SetMaxOpenConns(4)
	if _, _, err := migrate(db, migrateOptions{dbPath: path, now: now}); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db, now: now, changed: make(chan struct{}), path: path}, nil
}

// OpenForInspection opens a database without migrating it, so a status command
// can report on one this binary is too old for instead of refusing to speak.
func OpenForInspection(path string) (*sql.DB, error) {
	return sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)")
}

// Close releases the database.
func (s *Store) Close() error { return s.db.Close() }

// Now is the store's clock, shared so that callers stamp the same time.
func (s *Store) Now() time.Time { return s.now() }

// Changed returns a channel closed on the next mutation. Long-polls wait on it
// instead of re-querying in a loop.
func (s *Store) Changed() <-chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.changed
}

func (s *Store) notify() {
	s.mu.Lock()
	defer s.mu.Unlock()
	close(s.changed)
	s.changed = make(chan struct{})
}

func ms(t time.Time) int64 { return t.UnixMilli() }

func at(v int64) time.Time { return time.UnixMilli(v) }

func atPtr(v sql.NullInt64) *time.Time {
	if !v.Valid {
		return nil
	}
	t := at(v.Int64)
	return &t
}

// --- sessions ---------------------------------------------------------------

// SaveSession records what a session is doing now. since_at only moves when the
// status or the issue actually changes, so the page can say "since when".
func (s *Store) SaveSession(name, status, issue, detail string) (Session, error) {
	now := s.now()
	var prevStatus, prevIssue string
	var since int64
	err := s.db.QueryRow(`SELECT status, issue, since_at FROM sessions WHERE name = ?`, name).
		Scan(&prevStatus, &prevIssue, &since)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		since = ms(now)
	case err != nil:
		return Session{}, err
	case prevStatus != status || prevIssue != issue:
		since = ms(now)
	}
	_, err = s.db.Exec(`
		INSERT INTO sessions (name, status, issue, detail, since_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(name) DO UPDATE SET
			status = excluded.status, issue = excluded.issue,
			detail = excluded.detail, since_at = excluded.since_at,
			updated_at = excluded.updated_at`,
		name, status, issue, detail, since, ms(now))
	if err != nil {
		return Session{}, err
	}
	s.notify()
	return Session{Name: name, Status: status, Issue: issue, Detail: detail, SinceAt: at(since), UpdatedAt: now}, nil
}

// Sessions lists every known session, by name.
func (s *Store) Sessions() ([]Session, error) {
	rows, err := s.db.Query(`SELECT name, status, issue, detail, since_at, updated_at FROM sessions ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Session{}
	for rows.Next() {
		var v Session
		var since, updated int64
		if err := rows.Scan(&v.Name, &v.Status, &v.Issue, &v.Detail, &since, &updated); err != nil {
			return nil, err
		}
		v.SinceAt, v.UpdatedAt = at(since), at(updated)
		out = append(out, v)
	}
	return out, rows.Err()
}

// ErrStillWaiting is returned when a session cannot be retired because it still
// has asks on somebody's plate.
var ErrStillWaiting = errors.New("session still has open asks")

// RetireSession removes a session's row from the table.
//
// Its events stay: they are the trace of decisions, and deleting them would
// rewrite what was decided because the session that asked has gone. The page
// renders an ask by its author's name, not by looking the row up, so a card
// outlives the row that raised it.
//
// A session with open asks is refused. Retiring it would leave the PO holding
// cards whose author no longer exists, and an answer nobody is waiting to read.
// Answer them, or withdraw them, then retire.
//
// Retiring is not final: the same name on the next save starts a fresh row —
// a session relaunched on Saturday should not inherit last Tuesday's "since".
func (s *Store) RetireSession(name string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var exists int
	if err := tx.QueryRow(`SELECT count(*) FROM sessions WHERE name = ?`, name).Scan(&exists); err != nil {
		return err
	}
	if exists == 0 {
		return ErrNotFound
	}

	var open int
	if err := tx.QueryRow(
		`SELECT count(*) FROM events WHERE author = ? AND state = 'open' AND kind <> 'info'`,
		name).Scan(&open); err != nil {
		return err
	}
	if open > 0 {
		return fmt.Errorf("%w: %d still open — answer or withdraw them first", ErrStillWaiting, open)
	}

	if _, err := tx.Exec(`DELETE FROM sessions WHERE name = ?`, name); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.notify()
	return nil
}

// --- events -----------------------------------------------------------------

// AddEvent stores a new event and returns it, id filled in.
func (s *Store) AddEvent(e Event) (Event, error) {
	if e.Audience == "" {
		e.Audience = DefaultAudience(e.Kind)
	}
	opts, err := json.Marshal(nonNil(e.Options))
	if err != nil {
		return Event{}, err
	}
	now := s.now()
	if e.Role == "" {
		e.Role = RoleDev
	}
	res, err := s.db.Exec(`
		INSERT INTO events (author, role, kind, title, body, link, issue, audience, options, state, created_at, actionable_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		e.Author, e.Role, e.Kind, e.Title, e.Body, e.Link, e.Issue, e.Audience, string(opts), StateOpen, ms(now), ms(now))
	if err != nil {
		return Event{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Event{}, err
	}
	e.ID, e.State, e.CreatedAt = id, StateOpen, now
	s.notify()
	return e, nil
}

// Event returns one event with its reply, if any.
func (s *Store) Event(id int64) (Event, error) {
	rows, err := s.queryEvents(`WHERE e.id = ?`, id)
	if err != nil {
		return Event{}, err
	}
	if len(rows) == 0 {
		return Event{}, ErrNotFound
	}
	return rows[0], nil
}

// OpenEvents lists the still-open events for one audience, oldest first so the
// order on screen never jumps.
func (s *Store) OpenEvents(audience string) ([]Event, error) {
	return s.queryEvents(`WHERE e.state = 'open' AND e.audience = ? ORDER BY e.created_at`, audience)
}

// AnsweredFor lists events raised by author whose answer has not been seen yet.
// This is the return trip: the manager asked, the PO answered, and nothing told
// the manager.
func (s *Store) AnsweredFor(author string) ([]Event, error) {
	return s.queryEvents(`
		WHERE e.author = ? AND r.id IS NOT NULL AND r.seen_at IS NULL
		ORDER BY r.created_at`, author)
}

func (s *Store) queryEvents(where string, args ...any) ([]Event, error) {
	const base = `
		SELECT e.id, e.author, e.role, e.kind, e.title, e.body, e.link, e.issue, e.audience,
		       e.options, e.state, e.created_at, e.closed_at, e.closed_by,
		       r.id, r.author, r.text, r.option, r.created_at, r.seen_at
		FROM events e
		LEFT JOIN replies r ON r.event_id = e.id
		`
	rows, err := s.db.Query(base+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Event{}
	for rows.Next() {
		var e Event
		var opts string
		var created int64
		var closed sql.NullInt64
		var rID sql.NullInt64
		var rAuthor, rText, rOption sql.NullString
		var rCreated, rSeen sql.NullInt64
		if err := rows.Scan(&e.ID, &e.Author, &e.Role, &e.Kind, &e.Title, &e.Body, &e.Link, &e.Issue, &e.Audience,
			&opts, &e.State, &created, &closed, &e.ClosedBy,
			&rID, &rAuthor, &rText, &rOption, &rCreated, &rSeen); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(opts), &e.Options); err != nil {
			return nil, err
		}
		e.CreatedAt, e.ClosedAt = at(created), atPtr(closed)
		if rID.Valid {
			e.Reply = &Reply{
				ID: rID.Int64, EventID: e.ID, Author: rAuthor.String,
				Text: rText.String, Option: rOption.String,
				CreatedAt: at(rCreated.Int64), SeenAt: atPtr(rSeen),
			}
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// Reroute changes who an event is addressed to, leaving it open. This is the
// manager handing a dev's question over to the PO.
func (s *Store) Reroute(id int64, audience string) error {
	res, err := s.db.Exec(`UPDATE events SET audience = ? WHERE id = ? AND state = 'open'`, audience, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	s.notify()
	return nil
}

// ErrNotOpen is returned when an event cannot be withdrawn because it is no
// longer waiting on anybody.
var ErrNotOpen = errors.New("event is not open")

// ErrNotAllowed is returned when somebody tries to withdraw an event that is
// not theirs to withdraw.
var ErrNotAllowed = errors.New("not yours to withdraw")

// Withdraw closes an open event without answering it — the ask turned out not
// to need one, usually because the asker found the answer themselves.
//
// Only the author can withdraw their own ask; the manager can withdraw any of
// them, because dispatching is their job. Someone is always accountable for a
// card disappearing from under the reader, so who did it is recorded.
//
// An answered event is refused: taking an answer back is Undo, and the two must
// not overlap.
func (s *Store) Withdraw(eventID int64, by string) (Event, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return Event{}, err
	}
	defer tx.Rollback()

	var author, state string
	err = tx.QueryRow(`SELECT author, state FROM events WHERE id = ?`, eventID).Scan(&author, &state)
	if errors.Is(err, sql.ErrNoRows) {
		return Event{}, ErrNotFound
	}
	if err != nil {
		return Event{}, err
	}
	if state != StateOpen {
		return Event{}, fmt.Errorf("%w: it is %s", ErrNotOpen, state)
	}
	if by != author && by != AudienceManager {
		return Event{}, fmt.Errorf("%w: %s was raised by %s", ErrNotAllowed, by, author)
	}

	now := s.now()
	if _, err := tx.Exec(`UPDATE events SET state = ?, closed_at = ?, closed_by = ? WHERE id = ?`,
		StateWithdrawn, ms(now), by, eventID); err != nil {
		return Event{}, err
	}
	if err := tx.Commit(); err != nil {
		return Event{}, err
	}
	s.notify()
	return s.Event(eventID)
}

// --- replies ----------------------------------------------------------------

// AddReply answers an event. An event takes one answer: answering twice is a
// mistake, not an edit, so the second call fails.
func (s *Store) AddReply(eventID int64, author, text, option string) (Reply, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return Reply{}, err
	}
	defer tx.Rollback()

	var state string
	if err := tx.QueryRow(`SELECT state FROM events WHERE id = ?`, eventID).Scan(&state); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Reply{}, ErrNotFound
		}
		return Reply{}, err
	}
	if state != StateOpen {
		return Reply{}, fmt.Errorf("event %d is already %s", eventID, state)
	}

	now := s.now()
	res, err := tx.Exec(`INSERT INTO replies (event_id, author, text, option, created_at) VALUES (?, ?, ?, ?, ?)`,
		eventID, author, text, option, ms(now))
	if err != nil {
		return Reply{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Reply{}, err
	}
	if _, err := tx.Exec(`UPDATE events SET state = ?, closed_at = ? WHERE id = ?`,
		StateAnswered, ms(now), eventID); err != nil {
		return Reply{}, err
	}
	if err := tx.Commit(); err != nil {
		return Reply{}, err
	}
	s.notify()
	return Reply{ID: id, EventID: eventID, Author: author, Text: text, Option: option, CreatedAt: now}, nil
}

// MarkReplySeen records that the person who asked has read the answer. It is
// what makes an undo know whether somebody already acted on it.
func (s *Store) MarkReplySeen(eventID int64) error {
	res, err := s.db.Exec(`UPDATE replies SET seen_at = ? WHERE event_id = ? AND seen_at IS NULL`,
		ms(s.now()), eventID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		s.notify()
	}
	return nil
}

// AnsweredWithin lists the events author answered less than window ago — the
// ones they can still take back.
func (s *Store) AnsweredWithin(author string, window time.Duration) ([]Event, error) {
	cutoff := ms(s.now().Add(-window))
	return s.queryEvents(`
		WHERE r.author = ? AND r.created_at > ? AND e.state = 'answered'
		ORDER BY r.created_at`, author, cutoff)
}

// UndoReply takes back an answer inside the window and puts the event back on
// the plate. It reports whether the answer had already been read, because an
// answer somebody acted on cannot simply be erased.
//
// The event becomes actionable again as of now, not as of its creation: the
// batching must treat it as something new, or nobody would be told.
func (s *Store) UndoReply(eventID int64, author string, window time.Duration) (Event, bool, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return Event{}, false, err
	}
	defer tx.Rollback()

	var replyID, created int64
	var replyAuthor string
	var seen sql.NullInt64
	err = tx.QueryRow(`SELECT id, author, created_at, seen_at FROM replies WHERE event_id = ?`, eventID).
		Scan(&replyID, &replyAuthor, &created, &seen)
	if errors.Is(err, sql.ErrNoRows) {
		return Event{}, false, ErrNotFound
	}
	if err != nil {
		return Event{}, false, err
	}
	if replyAuthor != author {
		return Event{}, false, fmt.Errorf("that answer was written by %s, not by %s", replyAuthor, author)
	}
	now := s.now()
	if elapsed := now.Sub(at(created)); elapsed > window {
		return Event{}, false, fmt.Errorf("that answer is %s old, past the %s undo window",
			elapsed.Round(time.Second), window)
	}

	if _, err := tx.Exec(`DELETE FROM replies WHERE id = ?`, replyID); err != nil {
		return Event{}, false, err
	}
	if _, err := tx.Exec(`
		UPDATE events SET state = ?, closed_at = NULL, actionable_at = ? WHERE id = ?`,
		StateOpen, ms(now), eventID); err != nil {
		return Event{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return Event{}, false, err
	}
	s.notify()

	e, err := s.Event(eventID)
	return e, seen.Valid, err
}

// Ack marks as read what only had to be read: the answers raised by author, and
// the open `info` events addressed to them. It never closes a question that is
// still waiting for somebody.
func (s *Store) Ack(author, audience string) (int64, error) {
	now := ms(s.now())
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	res, err := tx.Exec(`
		UPDATE replies SET seen_at = ?
		WHERE seen_at IS NULL AND event_id IN (SELECT id FROM events WHERE author = ?)`, now, author)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()

	res, err = tx.Exec(`
		UPDATE events SET state = 'done', closed_at = ?
		WHERE state = 'open' AND kind = 'info' AND audience = ?`, now, audience)
	if err != nil {
		return 0, err
	}
	m, _ := res.RowsAffected()

	if err := tx.Commit(); err != nil {
		return 0, err
	}
	s.notify()
	return n + m, nil
}

// --- wake -------------------------------------------------------------------

// Item is one thing a party still has to act on. `At` is when it became
// actionable, which is what the debounce window measures from.
type Item struct {
	At     time.Time
	Urgent bool // a dev is actually stopped
}

// Cursor records how far a party has already been signalled. Without it, a
// batch that was announced but not yet dealt with would raise a second signal
// as soon as the rate limit allowed — waking the manager for what they already
// know about.
type Cursor struct {
	SignaledAt      *time.Time // when the last signal went out
	SignaledThrough *time.Time // the newest item that signal covered
}

// PendingFor lists everything party still has to act on: the open events
// addressed to them, and the answers to their own questions that they have not
// read yet. `info` is never in there — it is published so that nobody has to
// read it now.
func (s *Store) PendingFor(party string) ([]Item, error) {
	rows, err := s.db.Query(`
		SELECT e.actionable_at, e.kind = 'blocked'
		FROM events e
		WHERE e.state = 'open' AND e.audience = ? AND e.kind <> 'info'
		UNION ALL
		SELECT r.created_at, 0
		FROM replies r JOIN events e ON e.id = r.event_id
		WHERE r.seen_at IS NULL AND e.author = ?`, party, party)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Item{}
	for rows.Next() {
		var at64 int64
		var urgent bool
		if err := rows.Scan(&at64, &urgent); err != nil {
			return nil, err
		}
		out = append(out, Item{At: at(at64), Urgent: urgent})
	}
	return out, rows.Err()
}

// Cursor reads how far party has been signalled.
func (s *Store) Cursor(party string) (Cursor, error) {
	var signaled, through sql.NullInt64
	err := s.db.QueryRow(`SELECT signaled_at, signaled_through FROM wake WHERE audience = ?`, party).
		Scan(&signaled, &through)
	if errors.Is(err, sql.ErrNoRows) {
		return Cursor{}, nil
	}
	if err != nil {
		return Cursor{}, err
	}
	return Cursor{SignaledAt: atPtr(signaled), SignaledThrough: atPtr(through)}, nil
}

// SetCursor records that a signal went out at `when`, covering every item up to
// and including `through`.
func (s *Store) SetCursor(party string, when, through time.Time) error {
	_, err := s.db.Exec(`
		INSERT INTO wake (audience, signaled_at, signaled_through) VALUES (?, ?, ?)
		ON CONFLICT(audience) DO UPDATE SET
			signaled_at = excluded.signaled_at, signaled_through = excluded.signaled_through`,
		party, ms(when), ms(through))
	return err
}

func nonNil(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}
