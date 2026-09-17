package store

import (
	"path/filepath"
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
	s.SaveSession("acme-dev1", StatusWaiting, "142", "waiting on the PO")
	e, _ := s.AddEvent(Event{Author: "acme-dev1", Kind: KindQuestion, Title: "Which date format?"})
	s.SetCursor(AudienceManager, base, base)
	s.Close()

	reopened, err := Open(path, func() time.Time { return *now })
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer reopened.Close()

	sessions, _ := reopened.Sessions()
	if len(sessions) != 1 || sessions[0].Status != StatusWaiting {
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
