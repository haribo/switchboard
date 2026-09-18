package store

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var base = time.Date(2026, 9, 17, 10, 0, 0, 0, time.UTC)

// testStore returns a store on a real file, with a clock the test drives.
func testStore(t *testing.T) (*Store, *time.Time, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "switchboard.db")
	now := base
	s, err := Open(path, func() time.Time { return now })
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s, &now, path
}

func TestSessionSinceOnlyMovesOnRealChange(t *testing.T) {
	s, now, _ := testStore(t)

	first, err := s.SaveSession("acme-dev1", StatusActive, "142", "")
	if err != nil {
		t.Fatalf("save: %v", err)
	}

	*now = base.Add(time.Minute)
	again, _ := s.SaveSession("acme-dev1", StatusActive, "142", "")
	if !again.SinceAt.Equal(first.SinceAt) {
		t.Fatal("a heartbeat on the same work reset 'since when'")
	}
	if !again.UpdatedAt.Equal(*now) {
		t.Fatal("the heartbeat did not refresh the last sign of life")
	}

	*now = base.Add(2 * time.Minute)
	moved, _ := s.SaveSession("acme-dev1", StatusActive, "143", "")
	if !moved.SinceAt.Equal(*now) {
		t.Fatal("moving to another ticket did not restart 'since when'")
	}
}

func TestValidationGoesToThePOAndTheRestToTheManager(t *testing.T) {
	s, _, _ := testStore(t)

	v, _ := s.AddEvent(Event{Author: "acme-dev2", Kind: KindValidation, Title: "sign-in screen", Link: "http://localhost:5173"})
	if v.Audience != AudiencePO {
		t.Fatalf("validation went to %q, want the PO", v.Audience)
	}
	for _, kind := range []string{KindInfo, KindQuestion, KindBlocked} {
		e, _ := s.AddEvent(Event{Author: "acme-dev2", Kind: kind, Title: "x"})
		if e.Audience != AudienceManager {
			t.Fatalf("%s went to %q, want the manager", kind, e.Audience)
		}
	}
}

func TestAnEventTakesOneAnswer(t *testing.T) {
	s, now, _ := testStore(t)
	e, _ := s.AddEvent(Event{Author: "acme-dev3", Kind: KindQuestion, Title: "Tab or modal?", Options: []string{"tab", "modal"}})

	*now = base.Add(time.Minute)
	if _, err := s.AddReply(e.ID, "po", "", "tab"); err != nil {
		t.Fatalf("reply: %v", err)
	}
	if _, err := s.AddReply(e.ID, "po", "actually a modal", ""); err == nil {
		t.Fatal("a second answer overwrote the first")
	}

	got, err := s.Event(e.ID)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if got.State != StateAnswered {
		t.Fatalf("state = %q, want %q", got.State, StateAnswered)
	}
	if got.Reply == nil || got.Reply.Option != "tab" {
		t.Fatalf("reply = %+v, want the chosen option", got.Reply)
	}
	if len(got.Options) != 2 {
		t.Fatalf("options = %v, want the two offered", got.Options)
	}
}

// `info` is published so that nobody has to read it now: it must never put the
// manager on the hook.
func TestInfoNeverWaitsOnAnybody(t *testing.T) {
	s, _, _ := testStore(t)
	s.AddEvent(Event{Author: "acme-dev1", Kind: KindInfo, Title: "e2e gate started, about 30 min"})

	items, err := s.PendingFor(AudienceManager)
	if err != nil {
		t.Fatalf("pending: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("an info put %d item(s) on the manager's plate", len(items))
	}

	open, _ := s.OpenEvents(AudienceManager)
	if len(open) != 1 {
		t.Fatal("the info did not reach the board at all")
	}
}

func TestPendingCarriesTheUnreadAnswersToOwnQuestions(t *testing.T) {
	s, now, _ := testStore(t)

	// The return trip: the manager asks the PO, the PO answers, and nothing
	// told the manager.
	ask, _ := s.AddEvent(Event{Author: AudienceManager, Kind: KindQuestion, Audience: AudiencePO, Title: "Do we keep the free text field?"})
	*now = base.Add(time.Minute)
	if _, err := s.AddReply(ask.ID, "po", "yes", ""); err != nil {
		t.Fatalf("reply: %v", err)
	}

	items, _ := s.PendingFor(AudienceManager)
	if len(items) != 1 {
		t.Fatalf("pending = %d, want the PO's unread answer", len(items))
	}
	if !items[0].At.Equal(base.Add(time.Minute)) {
		t.Fatalf("the answer became actionable at %v, want when it was written", items[0].At)
	}

	answered, _ := s.AnsweredFor(AudienceManager)
	if len(answered) != 1 || answered[0].Reply.Text != "yes" {
		t.Fatalf("AnsweredFor = %+v, want the answered question", answered)
	}
}

func TestBlockedIsFlaggedUrgent(t *testing.T) {
	s, _, _ := testStore(t)
	s.AddEvent(Event{Author: "acme-dev4", Kind: KindBlocked, Title: "migration failing"})

	items, _ := s.PendingFor(AudienceManager)
	if len(items) != 1 || !items[0].Urgent {
		t.Fatalf("items = %+v, want one urgent item", items)
	}
}

func TestAckReadsWhatOnlyHadToBeRead(t *testing.T) {
	s, now, _ := testStore(t)

	info, _ := s.AddEvent(Event{Author: "acme-dev1", Kind: KindInfo, Title: "gate finished, 822 tests green"})
	question, _ := s.AddEvent(Event{Author: "acme-dev2", Kind: KindQuestion, Title: "Which label?"})
	ask, _ := s.AddEvent(Event{Author: AudienceManager, Kind: KindQuestion, Audience: AudiencePO, Title: "Phase 2?"})
	s.AddReply(ask.ID, "po", "later", "")

	*now = base.Add(time.Minute)
	if _, err := s.Ack(AudienceManager, AudienceManager); err != nil {
		t.Fatalf("ack: %v", err)
	}

	if got, _ := s.Event(info.ID); got.State != StateDone {
		t.Fatalf("info state = %q, want %q", got.State, StateDone)
	}
	if got, _ := s.Event(question.ID); got.State != StateOpen {
		t.Fatal("ack closed a question that nobody had answered")
	}
	items, _ := s.PendingFor(AudienceManager)
	if len(items) != 1 {
		t.Fatalf("pending after ack = %d, want only the unanswered question", len(items))
	}
	if items[0].Urgent {
		t.Fatal("the remaining item should be the plain question")
	}
}

func TestRerouteHandsAQuestionToThePO(t *testing.T) {
	s, _, _ := testStore(t)
	e, _ := s.AddEvent(Event{Author: "acme-dev5", Kind: KindQuestion, Title: "Two columns or three?"})

	if err := s.Reroute(e.ID, AudiencePO); err != nil {
		t.Fatalf("reroute: %v", err)
	}
	if open, _ := s.OpenEvents(AudienceManager); len(open) != 0 {
		t.Fatal("the question stayed on the manager's plate")
	}
	if open, _ := s.OpenEvents(AudiencePO); len(open) != 1 {
		t.Fatal("the question did not reach the PO")
	}
}

// A session can die and come back; what it published must not die with it.
func TestEverythingSurvivesARestart(t *testing.T) {
	s, now, path := testStore(t)
	s.SaveSession("acme-dev1", StatusActive, "142", "waiting on the PO")
	e, _ := s.AddEvent(Event{Author: "acme-dev1", Kind: KindQuestion, Title: "Which date format?"})
	s.SetCursor(AudienceManager, base, base)
	s.Close()

	reopened, err := Open(path, func() time.Time { return *now })
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer reopened.Close()

	sessions, _ := reopened.Sessions()
	if len(sessions) != 1 || sessions[0].Status != StatusActive {
		t.Fatalf("sessions = %+v, want the waiting dev", sessions)
	}
	got, err := reopened.Event(e.ID)
	if err != nil || got.Title != "Which date format?" {
		t.Fatalf("event after restart = %+v, %v", got, err)
	}
	cur, _ := reopened.Cursor(AudienceManager)
	if cur.SignaledAt == nil || !cur.SignaledAt.Equal(base) {
		t.Fatalf("cursor after restart = %+v, want the batch bookkeeping intact", cur)
	}
}

func TestChangedFiresOnMutation(t *testing.T) {
	s, _, _ := testStore(t)
	ch := s.Changed()
	s.AddEvent(Event{Author: "acme-dev1", Kind: KindInfo, Title: "x"})
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("a mutation did not wake the long-polls")
	}
}

func TestMissingEventIsNotFound(t *testing.T) {
	s, _, _ := testStore(t)
	if _, err := s.Event(404); err != ErrNotFound {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	if _, err := s.AddReply(404, "po", "x", ""); err != ErrNotFound {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestAnAnswerCanBeTakenBackInsideTheWindow(t *testing.T) {
	s, now, _ := testStore(t)
	e, _ := s.AddEvent(Event{Author: "acme-dev1", Kind: KindQuestion, Title: "Valider ou Confirmer ?",
		Options: []string{"Valider", "Confirmer"}, Audience: AudiencePO})
	s.AddReply(e.ID, "po", "", "Valider")

	*now = base.Add(4 * time.Second)
	back, wasRead, err := s.UndoReply(e.ID, "po", 10*time.Second)
	if err != nil {
		t.Fatalf("undo: %v", err)
	}
	if wasRead {
		t.Fatal("the answer was reported as read when nobody had read it")
	}
	if back.State != StateOpen || back.Reply != nil {
		t.Fatalf("event after undo = %s with reply %+v, want open and no reply", back.State, back.Reply)
	}
	// The PO can now answer again.
	if _, err := s.AddReply(e.ID, "po", "", "Confirmer"); err != nil {
		t.Fatalf("second answer refused: %v", err)
	}
}

func TestAnAnswerCannotBeTakenBackAfterTheWindow(t *testing.T) {
	s, now, _ := testStore(t)
	e, _ := s.AddEvent(Event{Author: "acme-dev1", Kind: KindQuestion, Title: "x", Audience: AudiencePO})
	s.AddReply(e.ID, "po", "yes", "")

	*now = base.Add(11 * time.Second)
	if _, _, err := s.UndoReply(e.ID, "po", 10*time.Second); err == nil {
		t.Fatal("an answer was taken back past the window")
	}
	if got, _ := s.Event(e.ID); got.State != StateAnswered {
		t.Fatalf("state = %q, want it untouched", got.State)
	}
}

func TestOnlyTheAuthorTakesBackTheirOwnAnswer(t *testing.T) {
	s, _, _ := testStore(t)
	e, _ := s.AddEvent(Event{Author: "acme-dev1", Kind: KindQuestion, Title: "x"})
	s.AddReply(e.ID, AudienceManager, "ISO 8601", "")

	if _, _, err := s.UndoReply(e.ID, "po", 10*time.Second); err == nil {
		t.Fatal("the PO took back the manager's answer")
	}
}

// An answer the dev already read cannot simply be erased: somebody acted on it.
func TestUndoReportsThatTheAnswerHadBeenRead(t *testing.T) {
	s, now, _ := testStore(t)
	e, _ := s.AddEvent(Event{Author: "acme-dev1", Kind: KindQuestion, Title: "x", Audience: AudiencePO})
	s.AddReply(e.ID, "po", "yes", "")
	if err := s.MarkReplySeen(e.ID); err != nil {
		t.Fatalf("mark seen: %v", err)
	}

	*now = base.Add(3 * time.Second)
	_, wasRead, err := s.UndoReply(e.ID, "po", 10*time.Second)
	if err != nil {
		t.Fatalf("undo: %v", err)
	}
	if !wasRead {
		t.Fatal("the undo did not report that the dev had already read the answer")
	}
}

// A reopened event must wake the manager again; the batching measures
// actionable_at, not creation.
func TestAReopenedEventCountsAsNew(t *testing.T) {
	s, now, _ := testStore(t)
	e, _ := s.AddEvent(Event{Author: "acme-dev1", Kind: KindQuestion, Title: "x"})
	s.AddReply(e.ID, AudienceManager, "yes", "")

	*now = base.Add(5 * time.Second)
	if _, _, err := s.UndoReply(e.ID, AudienceManager, 10*time.Second); err != nil {
		t.Fatalf("undo: %v", err)
	}

	items, _ := s.PendingFor(AudienceManager)
	if len(items) != 1 {
		t.Fatalf("pending = %d, want the reopened question", len(items))
	}
	if !items[0].At.Equal(base.Add(5 * time.Second)) {
		t.Fatalf("actionable at %v, want the moment it was reopened", items[0].At)
	}
}

func TestAnsweredWithinListsOnlyWhatIsStillUndoable(t *testing.T) {
	s, now, _ := testStore(t)
	old, _ := s.AddEvent(Event{Author: "d", Kind: KindQuestion, Title: "older", Audience: AudiencePO})
	s.AddReply(old.ID, "po", "yes", "")

	*now = base.Add(30 * time.Second)
	fresh, _ := s.AddEvent(Event{Author: "d", Kind: KindQuestion, Title: "recent", Audience: AudiencePO})
	s.AddReply(fresh.ID, "po", "non", "")

	*now = base.Add(35 * time.Second)
	got, err := s.AnsweredWithin("po", 10*time.Second)
	if err != nil {
		t.Fatalf("answered within: %v", err)
	}
	if len(got) != 1 || got[0].ID != fresh.ID {
		t.Fatalf("undoable = %+v, want only the recent one", got)
	}
}

// An ask sometimes answers itself: the dev finds the answer after publishing
// the question. Leaving the card up makes the PO decide something that no
// longer means anything. Issue #1.
func TestAnAuthorCanWithdrawTheirOwnOpenAsk(t *testing.T) {
	s, now, _ := testStore(t)
	e, _ := s.AddEvent(Event{Author: "acme-dev1", Kind: KindQuestion, Audience: AudiencePO, Title: "Tab or modal?"})

	*now = base.Add(time.Minute)
	got, err := s.Withdraw(e.ID, "acme-dev1")
	if err != nil {
		t.Fatalf("withdraw: %v", err)
	}
	if got.State != StateWithdrawn {
		t.Fatalf("state = %q, want %q", got.State, StateWithdrawn)
	}
	if got.ClosedBy != "acme-dev1" {
		t.Fatalf("closed_by = %q, want the session that withdrew it", got.ClosedBy)
	}
	if got.ClosedAt == nil {
		t.Fatal("a withdrawn event has no closing time")
	}
	if open, _ := s.OpenEvents(AudiencePO); len(open) != 0 {
		t.Fatal("the withdrawn ask is still on the PO's plate")
	}
	// And it stops counting towards the manager's wake-up.
	if items, _ := s.PendingFor(AudiencePO); len(items) != 0 {
		t.Fatalf("pending = %d, want 0", len(items))
	}
}

// Dispatching is the manager's job, so they can withdraw anybody's ask.
func TestTheManagerCanWithdrawAnyOpenAsk(t *testing.T) {
	s, _, _ := testStore(t)
	e, _ := s.AddEvent(Event{Author: "acme-dev2", Kind: KindQuestion, Title: "Which label?"})

	if _, err := s.Withdraw(e.ID, AudienceManager); err != nil {
		t.Fatalf("manager withdraw: %v", err)
	}
}

func TestSomebodyElseCannotWithdrawYourAsk(t *testing.T) {
	s, _, _ := testStore(t)
	e, _ := s.AddEvent(Event{Author: "acme-dev1", Kind: KindQuestion, Title: "Which label?"})

	if _, err := s.Withdraw(e.ID, "acme-dev2"); !errors.Is(err, ErrNotAllowed) {
		t.Fatalf("err = %v, want ErrNotAllowed", err)
	}
	if got, _ := s.Event(e.ID); got.State != StateOpen {
		t.Fatalf("state = %q, want it untouched", got.State)
	}
}

// Taking an answer back is Undo; the two must not overlap.
func TestAnAnsweredEventCannotBeWithdrawn(t *testing.T) {
	s, _, _ := testStore(t)
	e, _ := s.AddEvent(Event{Author: "acme-dev1", Kind: KindQuestion, Audience: AudiencePO, Title: "Which label?"})
	s.AddReply(e.ID, "po", "Save", "")

	if _, err := s.Withdraw(e.ID, "acme-dev1"); !errors.Is(err, ErrNotOpen) {
		t.Fatalf("err = %v, want ErrNotOpen", err)
	}
	if got, _ := s.Event(e.ID); got.State != StateAnswered || got.Reply == nil {
		t.Fatalf("the answer was disturbed: %+v", got)
	}
}

func TestWithdrawingTwiceIsRefused(t *testing.T) {
	s, _, _ := testStore(t)
	e, _ := s.AddEvent(Event{Author: "acme-dev1", Kind: KindQuestion, Title: "Which label?"})
	s.Withdraw(e.ID, "acme-dev1")

	if _, err := s.Withdraw(e.ID, "acme-dev1"); !errors.Is(err, ErrNotOpen) {
		t.Fatalf("err = %v, want ErrNotOpen", err)
	}
}

// A typo becomes a permanent row, and a session stopped for a few days keeps
// its line. The table is useful because it is short and stable. Issue #2.
func TestRetiringASessionRemovesItsRow(t *testing.T) {
	s, _, _ := testStore(t)
	s.SaveSession("acme-dev3", StatusActive, "150", "CSV export")
	s.SaveSession("acme-dev33", StatusActive, "", "") // the typo

	if err := s.RetireSession("acme-dev33"); err != nil {
		t.Fatalf("retire: %v", err)
	}
	sessions, _ := s.Sessions()
	if len(sessions) != 1 || sessions[0].Name != "acme-dev3" {
		t.Fatalf("sessions = %+v, want only the real one", sessions)
	}
}

// Events are the trace of decisions, not the session's property.
func TestARetiredSessionsEventsSurvive(t *testing.T) {
	s, now, _ := testStore(t)
	s.SaveSession("acme-dev3", StatusActive, "150", "")
	e, _ := s.AddEvent(Event{Author: "acme-dev3", Kind: KindQuestion, Audience: AudiencePO, Title: "Which date format?"})
	s.AddReply(e.ID, "po", "ISO 8601", "")

	*now = base.Add(time.Hour)
	if err := s.RetireSession("acme-dev3"); err != nil {
		t.Fatalf("retire: %v", err)
	}

	got, err := s.Event(e.ID)
	if err != nil {
		t.Fatalf("the event went with the row: %v", err)
	}
	if got.Author != "acme-dev3" || got.Reply == nil || got.Reply.Text != "ISO 8601" {
		t.Fatalf("event after retiring = %+v, want it intact with its answer", got)
	}
}

// Retiring a session with open asks would leave the PO holding cards whose
// author no longer exists.
func TestASessionWithOpenAsksCannotBeRetired(t *testing.T) {
	s, _, _ := testStore(t)
	s.SaveSession("acme-dev3", StatusActive, "150", "")
	e, _ := s.AddEvent(Event{Author: "acme-dev3", Kind: KindQuestion, Audience: AudiencePO, Title: "Tab or modal?"})

	err := s.RetireSession("acme-dev3")
	if !errors.Is(err, ErrStillWaiting) {
		t.Fatalf("err = %v, want ErrStillWaiting", err)
	}
	if !strings.Contains(err.Error(), "withdraw") {
		t.Fatalf("the message does not say what to do about it: %v", err)
	}
	if sessions, _ := s.Sessions(); len(sessions) != 1 {
		t.Fatal("the row went away despite the refusal")
	}

	// Withdrawing the ask clears the way — the two features compose.
	if _, err := s.Withdraw(e.ID, "acme-dev3"); err != nil {
		t.Fatalf("withdraw: %v", err)
	}
	if err := s.RetireSession("acme-dev3"); err != nil {
		t.Fatalf("retire after withdrawing: %v", err)
	}
}

// An info waits on nobody, so it does not hold a session back.
func TestAnInfoDoesNotBlockRetirement(t *testing.T) {
	s, _, _ := testStore(t)
	s.SaveSession("acme-dev5", StatusIdle, "", "")
	s.AddEvent(Event{Author: "acme-dev5", Kind: KindInfo, Title: "gate finished, 822 tests green"})

	if err := s.RetireSession("acme-dev5"); err != nil {
		t.Fatalf("an info blocked retirement: %v", err)
	}
}

// A session relaunched on Saturday should not inherit last Tuesday's "since".
func TestTheSameNameComesBackOnAFreshRow(t *testing.T) {
	s, now, _ := testStore(t)
	s.SaveSession("acme-dev3", StatusActive, "150", "CSV export")
	s.RetireSession("acme-dev3")

	*now = base.Add(48 * time.Hour)
	back, err := s.SaveSession("acme-dev3", StatusActive, "151", "migration")
	if err != nil {
		t.Fatalf("save after retiring: %v", err)
	}
	if !back.SinceAt.Equal(*now) {
		t.Fatalf("since = %v, want the moment it came back", back.SinceAt)
	}
	if back.Issue != "151" || back.Detail != "migration" {
		t.Fatalf("the resurrected row carried old work: %+v", back)
	}
}

func TestRetiringASessionThatIsNotThereIsNotFound(t *testing.T) {
	s, _, _ := testStore(t)
	if err := s.RetireSession("acme-dev9"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}
