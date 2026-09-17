package api

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"switchboard/internal/store"
)

var base = time.Date(2026, 9, 17, 10, 0, 0, 0, time.UTC)

type harness struct {
	*httptest.Server
	st  *store.Store
	now *time.Time
	t   *testing.T
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	now := base
	st, err := store.Open(filepath.Join(t.TempDir(), "s.db"), func() time.Time { return now })
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	srv := httptest.NewServer(New(st, DefaultConfig, nil))
	t.Cleanup(srv.Close)
	return &harness{Server: srv, st: st, now: &now, t: t}
}

func (h *harness) do(method, path string, body any) (*http.Response, map[string]any) {
	h.t.Helper()
	var r *http.Request
	var err error
	if body != nil {
		buf, _ := json.Marshal(body)
		r, err = http.NewRequest(method, h.URL+path, bytes.NewReader(buf))
	} else {
		r, err = http.NewRequest(method, h.URL+path, nil)
	}
	if err != nil {
		h.t.Fatalf("request: %v", err)
	}
	res, err := http.DefaultClient.Do(r)
	if err != nil {
		h.t.Fatalf("%s %s: %v", method, path, err)
	}
	var out map[string]any
	if res.StatusCode != http.StatusNoContent {
		json.NewDecoder(res.Body).Decode(&out)
	}
	res.Body.Close()
	return res, out
}

func (h *harness) mustDo(method, path string, body any, want int) map[string]any {
	h.t.Helper()
	res, out := h.do(method, path, body)
	if res.StatusCode != want {
		h.t.Fatalf("%s %s = %d (%v), want %d", method, path, res.StatusCode, out, want)
	}
	return out
}

func list(v any) []any {
	if v == nil {
		return nil
	}
	return v.([]any)
}

func first(v any) map[string]any { return list(v)[0].(map[string]any) }

// The whole trip: a dev asks, the manager hands it to the PO, the PO answers
// on their page, and the dev picks its work back up on its own.
func TestQuestionTravelsFromDevToPOAndBack(t *testing.T) {
	h := newHarness(t)

	h.mustDo("PUT", "/v1/sessions/acme-dev3",
		map[string]string{"status": "active", "issue": "142"}, http.StatusOK)

	created := h.mustDo("POST", "/v1/events", map[string]any{
		"author": "acme-dev3", "kind": "question", "issue": "142",
		"title": "Tab or modal?", "options": []string{"tab", "modal"},
	}, http.StatusCreated)
	id := int64(created["id"].(float64))
	path := "/v1/events/" + itoa(id)

	state := h.mustDo("GET", "/v1/state", nil, http.StatusOK)
	if got := len(list(state["waiting"])); got != 1 {
		t.Fatalf("the manager sees %d question(s), want 1", got)
	}
	if got := state["pending"].(float64); got != 1 {
		t.Fatalf("pending = %v, want 1", got)
	}

	h.mustDo("POST", path+"/reroute", map[string]string{"audience": "po"}, http.StatusOK)

	po := h.mustDo("GET", "/v1/po", nil, http.StatusOK)
	if got := len(list(po["for_you"])); got != 1 {
		t.Fatalf("the PO sees %d item(s), want 1", got)
	}
	if got := len(list(first(po["for_you"])["options"])); got != 2 {
		t.Fatal("the offered options did not reach the page")
	}
	if got := len(list(po["sessions"])); got != 1 {
		t.Fatalf("the PO sees %d session(s), want 1", got)
	}

	h.mustDo("POST", path+"/replies",
		map[string]string{"author": "po", "option": "tab"}, http.StatusCreated)

	// The dev was waiting on this call and comes straight back.
	answered := h.mustDo("GET", path+"/reply?wait=5", nil, http.StatusOK)
	reply := answered["reply"].(map[string]any)
	if reply["option"] != "tab" {
		t.Fatalf("the dev read %v, want the PO's choice", reply["option"])
	}
	if answered["state"] != "answered" {
		t.Fatalf("state = %v, want answered", answered["state"])
	}

	if po = h.mustDo("GET", "/v1/po", nil, http.StatusOK); len(list(po["for_you"])) != 0 {
		t.Fatal("an answered question stayed on the PO's page")
	}
}

// The defect this tool exists to remove: no flow of information on the PO's page.
func TestNoInfoEverReachesThePOsList(t *testing.T) {
	h := newHarness(t)
	h.mustDo("POST", "/v1/events", map[string]string{
		"author": "acme-dev1", "kind": "info", "title": "e2e gate started, about 30 min",
	}, http.StatusCreated)
	// Even addressed to the PO on purpose, an info stays out of the list.
	h.mustDo("POST", "/v1/events", map[string]string{
		"author": "acme-dev1", "kind": "info", "audience": "po", "title": "gate finished",
	}, http.StatusCreated)

	po := h.mustDo("GET", "/v1/po", nil, http.StatusOK)
	if got := len(list(po["for_you"])); got != 0 {
		t.Fatalf("%d info(s) reached the PO's list", got)
	}
	if got := len(list(po["infos"])); got != 2 {
		t.Fatalf("infos = %d, want both, in the folded zone", got)
	}
}

func TestValidationCarriesItsLink(t *testing.T) {
	h := newHarness(t)

	h.mustDo("POST", "/v1/events", map[string]string{
		"author": "acme-dev2", "kind": "validation", "title": "sign-in screen",
	}, http.StatusBadRequest)

	h.mustDo("POST", "/v1/events", map[string]string{
		"author": "acme-dev2", "kind": "validation", "title": "sign-in screen",
		"link": "http://localhost:5173/login",
	}, http.StatusCreated)

	po := h.mustDo("GET", "/v1/po", nil, http.StatusOK)
	item := first(po["for_you"])
	if item["link"] != "http://localhost:5173/login" {
		t.Fatalf("link = %v, want the one to open", item["link"])
	}
}

func TestBadInputIsRefused(t *testing.T) {
	h := newHarness(t)
	for _, body := range []map[string]string{
		{"kind": "question", "title": "x"},                  // no author
		{"author": "d", "kind": "rumination", "title": "x"}, // unknown kind
		{"author": "d", "kind": "question"},                 // no text
		{"author": "d", "kind": "question", "title": "x", "audience": "ceo"},
	} {
		h.mustDo("POST", "/v1/events", body, http.StatusBadRequest)
	}
	h.mustDo("PUT", "/v1/sessions/acme-dev1", map[string]string{"status": "napping"}, http.StatusBadRequest)
	h.mustDo("GET", "/v1/events/999", nil, http.StatusNotFound)
}

func TestAnsweringTwiceIsRefused(t *testing.T) {
	h := newHarness(t)
	created := h.mustDo("POST", "/v1/events", map[string]string{
		"author": "acme-dev1", "kind": "question", "title": "Which label?",
	}, http.StatusCreated)
	path := "/v1/events/" + itoa(int64(created["id"].(float64))) + "/replies"

	h.mustDo("POST", path, map[string]string{"author": "po", "text": "Save"}, http.StatusCreated)
	h.mustDo("POST", path, map[string]string{"author": "po", "text": "Confirm"}, http.StatusConflict)
	h.mustDo("POST", path, map[string]string{"author": "po"}, http.StatusBadRequest)
}

// One signal for a burst, carrying the count and never the content.
func TestWakeGroupsTheBurst(t *testing.T) {
	h := newHarness(t)
	for _, text := range []string{"quel format ?", "Which label?", "Which colour?"} {
		h.mustDo("POST", "/v1/events", map[string]string{
			"author": "acme-dev1", "kind": "question", "title": text,
		}, http.StatusCreated)
	}

	// Inside the debounce window, nothing goes out.
	res, _ := h.do("GET", "/v1/wake?wait=1", nil)
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("wake = %d, want 204 inside the debounce window", res.StatusCode)
	}

	*h.now = base.Add(4 * time.Minute)
	batch := h.mustDo("GET", "/v1/wake?wait=1", nil, http.StatusOK)
	if got := batch["count"].(float64); got != 3 {
		t.Fatalf("count = %v, want 3", got)
	}
	if got := batch["line"].(string); got != "3 events to handle" {
		t.Fatalf("line = %q", got)
	}
	for _, key := range []string{"text", "author", "events"} {
		if _, leaked := batch[key]; leaked {
			t.Fatalf("the signal carried %q; it must carry counts only", key)
		}
	}

	// Same three events, already announced: the manager is not woken again.
	*h.now = base.Add(2 * time.Hour)
	res, _ = h.do("GET", "/v1/wake?wait=1", nil)
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("wake = %d, want 204 for an already-announced batch", res.StatusCode)
	}
}

// An idle manager is woken by the event itself, without polling.
func TestWakeReturnsAsSoonAsSomethingLands(t *testing.T) {
	h := newHarness(t)
	h.mustDo("POST", "/v1/events", map[string]string{
		"author": "acme-dev4", "kind": "blocked", "title": "migration failing",
	}, http.StatusCreated)
	// A stopped dev waits 30s, not 3 minutes.
	*h.now = base.Add(time.Minute)

	done := make(chan map[string]any, 1)
	go func() { done <- h.mustDo("GET", "/v1/wake?wait=10", nil, http.StatusOK) }()

	select {
	case batch := <-done:
		if got := batch["line"].(string); got != "1 event to handle, 1 blocked" {
			t.Fatalf("line = %q", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the long-poll did not come back for a blocked dev")
	}
}

func TestAckClearsWhatOnlyHadToBeRead(t *testing.T) {
	h := newHarness(t)
	h.mustDo("POST", "/v1/events", map[string]string{
		"author": "acme-dev1", "kind": "info", "title": "822 tests green",
	}, http.StatusCreated)
	h.mustDo("POST", "/v1/events", map[string]string{
		"author": "acme-dev1", "kind": "question", "title": "Do we merge?",
	}, http.StatusCreated)

	h.mustDo("POST", "/v1/ack", nil, http.StatusOK)

	state := h.mustDo("GET", "/v1/state", nil, http.StatusOK)
	if got := len(list(state["infos"])); got != 0 {
		t.Fatalf("infos after ack = %d, want 0", got)
	}
	if got := len(list(state["waiting"])); got != 1 {
		t.Fatal("ack closed a question nobody had answered")
	}
}

func itoa(v int64) string { return strconv.FormatInt(v, 10) }

// The wrong button, caught in the ten seconds that follow.
func TestAnAnswerCanBeTakenBackRightAfter(t *testing.T) {
	h := newHarness(t)
	created := h.mustDo("POST", "/v1/events", map[string]any{
		"author": "acme-dev1", "kind": "question", "audience": "po",
		"title": "Save or Confirm?", "options": []string{"Save", "Confirm"},
	}, http.StatusCreated)
	id := int64(created["id"].(float64))
	path := "/v1/events/" + itoa(id)

	h.mustDo("POST", path+"/replies", map[string]string{"author": "po", "option": "Save"}, http.StatusCreated)

	po := h.mustDo("GET", "/v1/po", nil, http.StatusOK)
	if got := len(list(po["recent"])); got != 1 {
		t.Fatalf("recent = %d, want the answer that can still be taken back", got)
	}
	if got := po["undo_window"].(float64); got != 10 {
		t.Fatalf("undo_window = %v, want 10 seconds", got)
	}

	*h.now = base.Add(4 * time.Second)
	res := h.mustDo("POST", path+"/undo", map[string]string{"author": "po"}, http.StatusOK)
	if res["was_read"].(bool) {
		t.Fatal("nobody had read it, yet the undo said otherwise")
	}

	po = h.mustDo("GET", "/v1/po", nil, http.StatusOK)
	if got := len(list(po["for_you"])); got != 1 {
		t.Fatal("the question did not come back on the PO's page")
	}
	if got := len(list(po["recent"])); got != 0 {
		t.Fatalf("recent = %d, want it emptied by the undo", got)
	}
	h.mustDo("POST", path+"/replies", map[string]string{"author": "po", "option": "Confirm"}, http.StatusCreated)
}

func TestTakingBackTooLateIsRefused(t *testing.T) {
	h := newHarness(t)
	created := h.mustDo("POST", "/v1/events", map[string]string{
		"author": "acme-dev1", "kind": "question", "audience": "po", "title": "x",
	}, http.StatusCreated)
	path := "/v1/events/" + itoa(int64(created["id"].(float64)))
	h.mustDo("POST", path+"/replies", map[string]string{"author": "po", "text": "yes"}, http.StatusCreated)

	*h.now = base.Add(11 * time.Second)
	h.mustDo("POST", path+"/undo", map[string]string{"author": "po"}, http.StatusConflict)
	if po := h.mustDo("GET", "/v1/po", nil, http.StatusOK); len(list(po["recent"])) != 0 {
		t.Fatal("an answer past the window was still offered for undo")
	}
}

// A dev already back at work cannot be silently un-answered: the manager is
// the one who can reach them, so the correction lands on the manager's plate.
func TestUndoingAnAnswerTheDevReadRaisesItToTheManager(t *testing.T) {
	h := newHarness(t)
	created := h.mustDo("POST", "/v1/events", map[string]string{
		"author": "acme-dev3", "kind": "question", "audience": "po", "issue": "142",
		"title": "Tab or modal?",
	}, http.StatusCreated)
	id := int64(created["id"].(float64))
	path := "/v1/events/" + itoa(id)

	h.mustDo("POST", path+"/replies", map[string]string{"author": "po", "text": "tab"}, http.StatusCreated)
	h.mustDo("GET", path+"/reply?wait=1", nil, http.StatusOK) // the dev reads it
	h.mustDo("POST", path+"/seen", nil, http.StatusNoContent)

	*h.now = base.Add(3 * time.Second)
	res := h.mustDo("POST", path+"/undo", map[string]string{"author": "po"}, http.StatusOK)
	if !res["was_read"].(bool) {
		t.Fatal("the undo did not notice the dev had already read it")
	}

	state := h.mustDo("GET", "/v1/state", nil, http.StatusOK)
	var catchUp map[string]any
	for _, raw := range list(state["waiting"]) {
		e := raw.(map[string]any)
		if strings.Contains(e["title"].(string), "Answer withdrawn") {
			catchUp = e
		}
	}
	if catchUp == nil {
		t.Fatalf("no catch-up reached the manager: %v", state["waiting"])
	}
	if !strings.Contains(catchUp["title"].(string), "acme-dev3") {
		t.Fatalf("the catch-up does not name the dev to reach: %q", catchUp["title"])
	}
	if catchUp["issue"] != "142" {
		t.Fatalf("ticket = %v, want the one at stake", catchUp["issue"])
	}

	// The catch-up is a real item on the manager's plate, so it wakes them.
	// The reopened question itself is the PO's again, not the manager's.
	if got := state["pending"].(float64); got != 1 {
		t.Fatalf("pending = %v, want the catch-up alone", got)
	}
	po := h.mustDo("GET", "/v1/po", nil, http.StatusOK)
	if got := len(list(po["for_you"])); got != 1 {
		t.Fatalf("the PO sees %d item(s), want the reopened question back", got)
	}
}

func TestOnlyTheAuthorTakesBackTheirAnswer(t *testing.T) {
	h := newHarness(t)
	created := h.mustDo("POST", "/v1/events", map[string]string{
		"author": "acme-dev1", "kind": "question", "title": "x",
	}, http.StatusCreated)
	path := "/v1/events/" + itoa(int64(created["id"].(float64)))
	h.mustDo("POST", path+"/replies", map[string]string{"author": "manager", "text": "ISO 8601"}, http.StatusCreated)

	h.mustDo("POST", path+"/undo", map[string]string{"author": "po"}, http.StatusConflict)
	h.mustDo("POST", path+"/undo", map[string]string{"author": "manager"}, http.StatusOK)
}

// The two states that matter are worked out by the service, not declared by a
// session — a status nobody has to refresh cannot go stale.
func TestTheTableWorksOutBlockedAndWaitingOnYou(t *testing.T) {
	h := newHarness(t)
	for _, name := range []string{"acme-dev1", "acme-dev2", "acme-dev3", "acme-dev4"} {
		h.mustDo("PUT", "/v1/sessions/"+name,
			map[string]string{"status": "active", "issue": "14" + name[len(name)-1:]}, http.StatusOK)
	}
	h.mustDo("PUT", "/v1/sessions/acme-dev4", map[string]string{"status": "idle"}, http.StatusOK)

	// dev1 has an ask sitting with the PO; dev2 is stopped; dev3 just works.
	h.mustDo("POST", "/v1/events", map[string]string{
		"author": "acme-dev1", "kind": "question", "audience": "po", "title": "Tab or modal?",
	}, http.StatusCreated)
	h.mustDo("POST", "/v1/events", map[string]string{
		"author": "acme-dev2", "kind": "blocked", "title": "migration failing",
	}, http.StatusCreated)

	po := h.mustDo("GET", "/v1/po", nil, http.StatusOK)
	got := map[string]string{}
	for _, raw := range list(po["sessions"]) {
		row := raw.(map[string]any)
		got[row["name"].(string)] = row["state"].(string)
	}
	want := map[string]string{
		"acme-dev1": "waiting_on_you",
		"acme-dev2": "blocked",
		"acme-dev3": "working",
		"acme-dev4": "idle",
	}
	for name, state := range want {
		if got[name] != state {
			t.Fatalf("%s = %q, want %q", name, got[name], state)
		}
	}
}

// An info must never make a session look like it is waiting on the PO.
func TestAnInfoDoesNotPutASessionOnThePO(t *testing.T) {
	h := newHarness(t)
	h.mustDo("PUT", "/v1/sessions/acme-dev5", map[string]string{"status": "active"}, http.StatusOK)
	h.mustDo("POST", "/v1/events", map[string]string{
		"author": "acme-dev5", "kind": "info", "audience": "po", "title": "822 tests green",
	}, http.StatusCreated)

	po := h.mustDo("GET", "/v1/po", nil, http.StatusOK)
	row := first(po["sessions"])
	if row["state"] != "working" {
		t.Fatalf("state = %v, want working", row["state"])
	}
}

// A session knows the repository it works in; the service does not, and does
// not guess. So a full URL always links, and a bare number only does when a
// repository has been configured.
func TestAnIssueSentAsAURLNeedsNoConfiguration(t *testing.T) {
	h := newHarness(t)
	h.mustDo("PUT", "/v1/sessions/acme-dev1", map[string]string{
		"status": "active", "issue": "https://github.com/acme/app/issues/142",
	}, http.StatusOK)

	row := first(h.mustDo("GET", "/v1/po", nil, http.StatusOK)["sessions"])
	if row["issue_url"] != "https://github.com/acme/app/issues/142" {
		t.Fatalf("issue_url = %v, want the address the session sent", row["issue_url"])
	}
	if row["issue_label"] != "#142" {
		t.Fatalf("issue_label = %v, want the number pulled out of the address", row["issue_label"])
	}
}

func TestABareNumberLinksOnlyWhenARepositoryIsSet(t *testing.T) {
	h := newHarness(t)
	h.mustDo("PUT", "/v1/sessions/acme-dev1",
		map[string]string{"status": "active", "issue": "142"}, http.StatusOK)

	// Nothing says which repository "142" belongs to.
	row := first(h.mustDo("GET", "/v1/po", nil, http.StatusOK)["sessions"])
	if _, has := row["issue_url"]; has {
		t.Fatal("a bare number was linked with no repository configured")
	}
	if row["issue_label"] != "#142" {
		t.Fatalf("issue_label = %v, want #142 shown as plain text", row["issue_label"])
	}

	cfg := DefaultConfig
	cfg.RepoURL = "https://github.com/acme/app/"
	if got, want := cfg.IssueURL("142"), "https://github.com/acme/app/issues/142"; got != want {
		t.Fatalf("issue url = %q, want %q", got, want)
	}
	if got, want := cfg.IssueURL("#142"), "https://github.com/acme/app/issues/142"; got != want {
		t.Fatalf("a hash-prefixed number gave %q, want %q", got, want)
	}
	// A full URL wins over the configured repository: sessions may span repos.
	other := "https://github.com/acme/other/issues/7"
	if got := cfg.IssueURL(other); got != other {
		t.Fatalf("issue url = %q, want the address as sent", got)
	}
	if cfg.IssueURL("") != "" || cfg.IssueLabel("") != "" {
		t.Fatal("a session with no issue got a link or a label")
	}
}

// The description is where a link lives; it is also where a script would try to.
func TestTheBodyIsSanitizedOnTheWayIn(t *testing.T) {
	h := newHarness(t)
	created := h.mustDo("POST", "/v1/events", map[string]any{
		"author": "acme-dev2", "kind": "question", "audience": "po",
		"title": "Sign-in screen, second pass",
		"body": `<p>Running on <a href="http://localhost:5173/login">localhost</a>.</p>` +
			`<script>alert(1)</script><p onclick="steal()">and this</p>`,
	}, http.StatusCreated)

	body := created["body"].(string)
	if !strings.Contains(body, "localhost:5173/login") {
		t.Fatalf("the link was dropped: %q", body)
	}
	for _, forbidden := range []string{"<script", "onclick"} {
		if strings.Contains(strings.ToLower(body), forbidden) {
			t.Fatalf("%q survived: %q", forbidden, body)
		}
	}
	if !strings.Contains(body, "and this") {
		t.Fatalf("text inside a stripped attribute was lost: %q", body)
	}
}

func TestATitleIsOneShortLine(t *testing.T) {
	h := newHarness(t)
	h.mustDo("POST", "/v1/events", map[string]string{
		"author": "d", "kind": "question", "title": "first line\nsecond line",
	}, http.StatusBadRequest)
	h.mustDo("POST", "/v1/events", map[string]string{
		"author": "d", "kind": "question", "title": strings.Repeat("x", 121),
	}, http.StatusBadRequest)
	h.mustDo("POST", "/v1/events", map[string]string{
		"author": "d", "kind": "question", "title": strings.Repeat("x", 120),
	}, http.StatusCreated)
}

func TestTheRoleTravelsWithTheAsk(t *testing.T) {
	h := newHarness(t)
	created := h.mustDo("POST", "/v1/events", map[string]string{
		"author": "acme-manager", "role": "manager", "kind": "question",
		"audience": "po", "title": "Do we start phase 2 on Thursday?",
	}, http.StatusCreated)
	if created["role"] != "manager" {
		t.Fatalf("role = %v, want manager", created["role"])
	}

	// Left out, an ask is a dev's.
	plain := h.mustDo("POST", "/v1/events", map[string]string{
		"author": "acme-dev1", "kind": "question", "title": "Which label?",
	}, http.StatusCreated)
	if plain["role"] != "dev" {
		t.Fatalf("role = %v, want dev", plain["role"])
	}
	h.mustDo("POST", "/v1/events", map[string]string{
		"author": "d", "role": "ceo", "kind": "question", "title": "x",
	}, http.StatusBadRequest)
}

// stubPage stands in for the embedded page in tests that only care that it is
// mounted at all.
type stubPage struct{}

func (stubPage) Open(name string) (fs.File, error) { return nil, fs.ErrNotExist }

// An ask that answers itself should disappear on its own, not cost the PO a
// decision. Issue #1.
func TestAnOpenAskCanBeWithdrawnByItsAuthor(t *testing.T) {
	h := newHarness(t)
	created := h.mustDo("POST", "/v1/events", map[string]string{
		"author": "acme-dev1", "kind": "question", "audience": "po", "title": "Tab or modal?",
	}, http.StatusCreated)
	id := int64(created["id"].(float64))
	path := "/v1/events/" + itoa(id)

	if got := len(list(h.mustDo("GET", "/v1/po", nil, http.StatusOK)["for_you"])); got != 1 {
		t.Fatalf("for_you = %d, want the ask", got)
	}

	out := h.mustDo("POST", path+"/withdraw", map[string]string{"author": "acme-dev1"}, http.StatusOK)
	if out["state"] != "withdrawn" {
		t.Fatalf("state = %v, want withdrawn", out["state"])
	}
	if out["closed_by"] != "acme-dev1" {
		t.Fatalf("closed_by = %v, want who withdrew it", out["closed_by"])
	}

	// Off the page, and out of the tab count the page derives from it.
	po := h.mustDo("GET", "/v1/po", nil, http.StatusOK)
	if got := len(list(po["for_you"])); got != 0 {
		t.Fatalf("for_you = %d, want the withdrawn ask gone", got)
	}
}

// A session blocked on its own question must tell a withdrawal from a verdict.
func TestAwaitReturnsAWithdrawalAsSuch(t *testing.T) {
	h := newHarness(t)
	created := h.mustDo("POST", "/v1/events", map[string]string{
		"author": "acme-dev3", "kind": "question", "title": "Which date format?",
	}, http.StatusCreated)
	path := "/v1/events/" + itoa(int64(created["id"].(float64)))

	done := make(chan map[string]any, 1)
	go func() { done <- h.mustDo("GET", path+"/reply?wait=10", nil, http.StatusOK) }()
	time.Sleep(150 * time.Millisecond)
	h.mustDo("POST", path+"/withdraw", map[string]string{"author": "acme-dev3"}, http.StatusOK)

	select {
	case e := <-done:
		if e["state"] != "withdrawn" {
			t.Fatalf("state = %v, want withdrawn", e["state"])
		}
		if _, hasReply := e["reply"]; hasReply {
			t.Fatal("a withdrawal came back carrying a reply")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("await did not return on a withdrawal — it would have waited out its timeout")
	}
}

func TestWithdrawalIsRefusedWhereItShouldBe(t *testing.T) {
	h := newHarness(t)
	created := h.mustDo("POST", "/v1/events", map[string]string{
		"author": "acme-dev1", "kind": "question", "audience": "po", "title": "Which label?",
	}, http.StatusCreated)
	path := "/v1/events/" + itoa(int64(created["id"].(float64)))

	// Not the author, not the manager.
	h.mustDo("POST", path+"/withdraw", map[string]string{"author": "acme-dev2"}, http.StatusForbidden)
	// Nobody named at all.
	h.mustDo("POST", path+"/withdraw", map[string]string{}, http.StatusBadRequest)
	h.mustDo("POST", "/v1/events/999/withdraw", map[string]string{"author": "acme-dev1"}, http.StatusNotFound)

	// Answered: that is undo's job, and the two must not overlap.
	h.mustDo("POST", path+"/replies", map[string]string{"author": "po", "text": "Save"}, http.StatusCreated)
	h.mustDo("POST", path+"/withdraw", map[string]string{"author": "acme-dev1"}, http.StatusConflict)
	if got := h.mustDo("GET", path, nil, http.StatusOK); got["state"] != "answered" {
		t.Fatalf("state = %v, want the answer untouched", got["state"])
	}
}

// The manager dispatches, so they can withdraw anybody's ask.
func TestTheManagerCanWithdrawAnybodysAsk(t *testing.T) {
	h := newHarness(t)
	created := h.mustDo("POST", "/v1/events", map[string]string{
		"author": "acme-dev4", "kind": "blocked", "title": "Migration failing",
	}, http.StatusCreated)
	path := "/v1/events/" + itoa(int64(created["id"].(float64)))

	h.mustDo("POST", path+"/withdraw", map[string]string{"author": "manager"}, http.StatusOK)
	if got := h.mustDo("GET", "/v1/state", nil, http.StatusOK); got["pending"].(float64) != 0 {
		t.Fatalf("pending = %v, want the withdrawn ask to stop counting", got["pending"])
	}
}
