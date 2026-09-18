package api

import (
	"bytes"
	"encoding/json"
	"fmt"
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
		map[string]string{"status": "active", "issue": issueURL(142)}, http.StatusOK)

	created := h.mustDo("POST", "/v1/events", map[string]any{
		"author": "acme-dev3", "kind": "question", "issue": issueURL(142),
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
		"author": "acme-dev3", "kind": "question", "audience": "po", "issue": issueURL(142),
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
	if catchUp["issue"] != issueURL(142) {
		t.Fatalf("issue = %v, want the one at stake", catchUp["issue"])
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
			map[string]string{"status": "active", "issue": issueURL(14) + name[len(name)-1:]}, http.StatusOK)
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

// The service holds no repository to resolve a bare number against, and will
// not be given one: a number resolved against a repository the session was not
// working in links to somebody else's issue. Issue #14.
func TestABareIssueNumberIsRefused(t *testing.T) {
	h := newHarness(t)

	for _, path := range []string{"/v1/sessions/acme-dev1"} {
		res, out := h.do("PUT", path, map[string]string{"status": "active", "issue": "142"})
		if res.StatusCode != http.StatusBadRequest {
			t.Fatalf("PUT %s with a bare number = %d, want 400", path, res.StatusCode)
		}
		if msg, _ := out["error"].(string); !strings.Contains(msg, "full URL") {
			t.Fatalf("message %q does not name the rule", msg)
		}
	}
	res, out := h.do("POST", "/v1/events", map[string]string{
		"author": "acme-dev1", "kind": "question", "title": "Which label?", "issue": "142",
	})
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("POST /v1/events with a bare number = %d, want 400", res.StatusCode)
	}
	if msg, _ := out["error"].(string); !strings.Contains(msg, "full URL") {
		t.Fatalf("message %q does not name the rule", msg)
	}

	// No issue at all stays fine: not every ask is about one.
	h.mustDo("PUT", "/v1/sessions/acme-dev2", map[string]string{"status": "active"}, http.StatusOK)

	cfg := DefaultConfig
	if got := cfg.IssueURL("142"); got != "" {
		t.Fatalf("a bare number resolved to %q; nothing can resolve it", got)
	}
	if got, want := cfg.IssueURL("https://github.com/acme/app/issues/142"), "https://github.com/acme/app/issues/142"; got != want {
		t.Fatalf("issue url = %q, want %q", got, want)
	}
}

// A row written before the rule keeps its bare number and still renders — as
// plain text, since nothing can turn it into an address. Issue #14.
func TestARowWrittenBeforeTheRuleStillRenders(t *testing.T) {
	h := newHarness(t)
	if _, err := h.st.SaveSession("acme-dev1", "active", "142", "CSV export"); err != nil {
		t.Fatalf("seed: %v", err)
	}

	row := first(h.mustDo("GET", "/v1/po", nil, http.StatusOK)["sessions"])
	if row["issue"] != "142" {
		t.Fatalf("issue = %v, want it kept as written", row["issue"])
	}
	if row["issue_label"] != "#142" {
		t.Fatalf("issue_label = %v, want it still shown", row["issue_label"])
	}
	if _, linked := row["issue_url"]; linked {
		t.Fatal("a bare number was turned into a link")
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

// A retired session leaves the table; what it published stays readable. Issue #2.
func TestARetiredSessionLeavesThePageButItsEventsRemain(t *testing.T) {
	h := newHarness(t)
	h.mustDo("PUT", "/v1/sessions/acme-dev3",
		map[string]string{"status": "active", "issue": issueURL(150)}, http.StatusOK)
	created := h.mustDo("POST", "/v1/events", map[string]string{
		"author": "acme-dev3", "kind": "info", "title": "e2e gate finished",
	}, http.StatusCreated)
	id := itoa(int64(created["id"].(float64)))

	h.mustDo("DELETE", "/v1/sessions/acme-dev3", nil, http.StatusNoContent)

	po := h.mustDo("GET", "/v1/po", nil, http.StatusOK)
	if got := len(list(po["sessions"])); got != 0 {
		t.Fatalf("sessions = %d, want the row gone", got)
	}
	// The event is still readable, and still carries the name that raised it —
	// which is what the page renders, so it does not need the row.
	got := h.mustDo("GET", "/v1/events/"+id, nil, http.StatusOK)
	if got["author"] != "acme-dev3" {
		t.Fatalf("author = %v, want it kept", got["author"])
	}
	if infos := list(po["infos"]); len(infos) != 1 {
		t.Fatalf("infos = %d, want the event still shown", len(infos))
	}
}

func TestRetiringIsRefusedWhileAsksAreOpen(t *testing.T) {
	h := newHarness(t)
	h.mustDo("PUT", "/v1/sessions/acme-dev3", map[string]string{"status": "active"}, http.StatusOK)
	created := h.mustDo("POST", "/v1/events", map[string]string{
		"author": "acme-dev3", "kind": "question", "audience": "po", "title": "Tab or modal?",
	}, http.StatusCreated)
	path := "/v1/events/" + itoa(int64(created["id"].(float64)))

	h.mustDo("DELETE", "/v1/sessions/acme-dev3", nil, http.StatusConflict)
	if got := len(list(h.mustDo("GET", "/v1/po", nil, http.StatusOK)["sessions"])); got != 1 {
		t.Fatal("the row went despite the refusal")
	}

	// Withdrawing clears the way.
	h.mustDo("POST", path+"/withdraw", map[string]string{"author": "acme-dev3"}, http.StatusOK)
	h.mustDo("DELETE", "/v1/sessions/acme-dev3", nil, http.StatusNoContent)
}

func TestRetiringAnUnknownSessionIsNotFound(t *testing.T) {
	h := newHarness(t)
	h.mustDo("DELETE", "/v1/sessions/acme-dev9", nil, http.StatusNotFound)
}

// A name that is not path-safe used to reach the router before any validation
// and come back as "405 Method Not Allowed", which tells an automated caller
// nothing about what it did wrong. Issue #3.
func TestAnInvalidSessionNameSaysWhatIsWrong(t *testing.T) {
	h := newHarness(t)
	body := map[string]string{"status": "active"}

	for _, c := range []struct{ name, path, why string }{
		{"a slash", "/v1/sessions/a/b", "lands on a path with no handler"},
		{"a traversal", "/v1/sessions/../../etc/passwd", "the reported case"},
		{"empty", "/v1/sessions/", "no name at all"},
		{"a space", "/v1/sessions/mon nom", "accepted silently before"},
	} {
		res, out := h.do("PUT", c.path, body)
		if res.StatusCode != http.StatusBadRequest {
			t.Errorf("%s (%s): status = %d, want 400", c.name, c.why, res.StatusCode)
			continue
		}
		msg, _ := out["error"].(string)
		if !strings.Contains(msg, "session name") {
			t.Errorf("%s: message %q does not name the rule", c.name, msg)
		}
	}
}

func TestAValidSessionNameIsUnaffected(t *testing.T) {
	h := newHarness(t)
	for _, name := range []string{"acme-dev1", "acme_dev1", "dev.1", "D3V", "a"} {
		h.mustDo("PUT", "/v1/sessions/"+name, map[string]string{"status": "active"}, http.StatusOK)
	}
	h.mustDo("DELETE", "/v1/sessions/acme-dev1", nil, http.StatusNoContent)
	// Retiring is deliberately not name-checked — a row that exists must always
	// be removable. A name with a slash could never have been created, so there
	// is simply nothing there.
	res, _ := h.do("DELETE", "/v1/sessions/a/b", nil)
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("DELETE with a name nothing could have been created under = %d, want 404", res.StatusCode)
	}
}

// The name is the caller's to give; only its shape is checked.
func TestATooLongSessionNameIsRefused(t *testing.T) {
	h := newHarness(t)
	h.mustDo("PUT", "/v1/sessions/"+strings.Repeat("a", 64),
		map[string]string{"status": "active"}, http.StatusOK)
	res, out := h.do("PUT", "/v1/sessions/"+strings.Repeat("a", 65), map[string]string{"status": "active"})
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("65 characters = %d, want 400", res.StatusCode)
	}
	if msg, _ := out["error"].(string); !strings.Contains(msg, "64") {
		t.Fatalf("message %q does not give the limit", msg)
	}
}

// Names were only validated from #3 onwards, so a database can hold a row the
// current rule would reject. Refusing to retire it would make exactly the
// permanent row #2 exists to remove — and would leave it on the PO's table with
// no way out.
func TestASessionWhoseNameIsNowInvalidCanStillBeRetired(t *testing.T) {
	h := newHarness(t)
	// Straight into the store, the way a pre-#3 PUT would have landed.
	if _, err := h.st.SaveSession("mon nom", "active", "", ""); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if got := len(list(h.mustDo("GET", "/v1/po", nil, http.StatusOK)["sessions"])); got != 1 {
		t.Fatal("the seeded row is not on the page")
	}

	h.mustDo("DELETE", "/v1/sessions/mon nom", nil, http.StatusNoContent)

	if got := len(list(h.mustDo("GET", "/v1/po", nil, http.StatusOK)["sessions"])); got != 0 {
		t.Fatal("the row is still there — it cannot be cleaned up")
	}
	// Creating it again is still refused: the rule applies to new names.
	h.mustDo("PUT", "/v1/sessions/mon nom", map[string]string{"status": "active"}, http.StatusBadRequest)
}

// issueURL is what a session sends: the whole address of the issue it is
// working on, since the service holds no repository to resolve a number.
func issueURL(n int) string {
	return fmt.Sprintf("https://github.com/acme/app/issues/%d", n)
}

// An ask written in the dev's vocabulary can reach the PO as something only the
// dev holds the terms for. Issue #12.
func TestThePOCanAskForAnAskToBePutInPlainWords(t *testing.T) {
	h := newHarness(t)
	created := h.mustDo("POST", "/v1/events", map[string]any{
		"author": "acme-dev3", "kind": "question", "audience": "po",
		"title": "CSV export pages past 10,000 rows — keyset or offset?",
	}, http.StatusCreated)
	id := int64(created["id"].(float64))
	path := "/v1/events/" + itoa(id)

	asked := h.mustDo("POST", path+"/explain", nil, http.StatusOK)
	if asked["explain_pending"] != true {
		t.Fatalf("explain_pending = %v, want true", asked["explain_pending"])
	}
	// The ask is still open: it waits for an answer, just not in those terms.
	if asked["state"] != "open" {
		t.Fatalf("state = %v, want it still open", asked["state"])
	}
	if got := len(list(h.mustDo("GET", "/v1/po", nil, http.StatusOK)["for_you"])); got != 1 {
		t.Fatal("the ask left the PO's page")
	}

	// Clicking twice raises no second request.
	again := h.mustDo("POST", path+"/explain", nil, http.StatusOK)
	if again["explain_pending"] != true {
		t.Fatal("a second ask cleared the first")
	}

	explained := h.mustDo("POST", path+"/explanation", map[string]string{
		"author": "acme-dev3",
		"body":   `<p>Paging by <a href="https://example.com/keyset">keyset</a> is faster; offset is simpler.</p>`,
	}, http.StatusOK)
	if explained["explain_pending"] == true {
		t.Fatal("publishing did not clear the request")
	}
	if !strings.Contains(explained["explanation"].(string), "keyset") {
		t.Fatalf("explanation = %v", explained["explanation"])
	}
	// The original wording is kept: the PO may want it back.
	if !strings.Contains(explained["title"].(string), "keyset or offset") {
		t.Fatal("the original wording was replaced")
	}

	// And it may be asked again if the rewording did not help.
	h.mustDo("POST", path+"/explain", nil, http.StatusOK)
}

// Three ways to stop waiting, and a caller must tell them apart.
func TestAwaitReturnsWhenAReWordingIsAsked(t *testing.T) {
	h := newHarness(t)
	created := h.mustDo("POST", "/v1/events", map[string]string{
		"author": "acme-dev3", "kind": "question", "title": "keyset or offset?",
	}, http.StatusCreated)
	path := "/v1/events/" + itoa(int64(created["id"].(float64)))

	done := make(chan map[string]any, 1)
	go func() { done <- h.mustDo("GET", path+"/reply?wait=10", nil, http.StatusOK) }()
	time.Sleep(150 * time.Millisecond)
	h.mustDo("POST", path+"/explain", nil, http.StatusOK)

	select {
	case e := <-done:
		if e["explain_pending"] != true {
			t.Fatalf("await came back with explain_pending = %v", e["explain_pending"])
		}
		if _, hasReply := e["reply"]; hasReply {
			t.Fatal("a rewording request came back carrying a reply")
		}
		if e["state"] != "open" {
			t.Fatalf("state = %v — this is not a verdict, the ask still stands", e["state"])
		}
	case <-time.After(5 * time.Second):
		t.Fatal("await did not return — it would have waited out its timeout")
	}
}

func TestExplainingIsRefusedWhereItShouldBe(t *testing.T) {
	h := newHarness(t)
	info := h.mustDo("POST", "/v1/events", map[string]string{
		"author": "acme-dev5", "kind": "info", "title": "822 tests green",
	}, http.StatusCreated)
	infoPath := "/v1/events/" + itoa(int64(info["id"].(float64)))
	// An info asks nothing, so there is nothing to reword.
	h.mustDo("POST", infoPath+"/explain", nil, http.StatusConflict)

	created := h.mustDo("POST", "/v1/events", map[string]string{
		"author": "acme-dev3", "kind": "question", "audience": "po", "title": "keyset or offset?",
	}, http.StatusCreated)
	path := "/v1/events/" + itoa(int64(created["id"].(float64)))
	h.mustDo("POST", path+"/explain", nil, http.StatusOK)

	// Somebody else's ask is not theirs to reword.
	h.mustDo("POST", path+"/explanation",
		map[string]string{"author": "acme-dev9", "body": "x"}, http.StatusForbidden)
	// An explanation with nothing in it is not one.
	h.mustDo("POST", path+"/explanation",
		map[string]string{"author": "acme-dev3", "body": "  "}, http.StatusBadRequest)
	// The manager may do it for a session that is gone.
	h.mustDo("POST", path+"/explanation",
		map[string]string{"author": "manager", "body": "<p>Which paging strategy.</p>"}, http.StatusOK)

	// Once answered, the wording is no longer the question.
	h.mustDo("POST", path+"/replies", map[string]string{"author": "po", "text": "keyset"}, http.StatusCreated)
	h.mustDo("POST", path+"/explain", nil, http.StatusConflict)
}

// The explanation is a body like any other: it is sanitized on the way in.
func TestAnExplanationIsSanitized(t *testing.T) {
	h := newHarness(t)
	created := h.mustDo("POST", "/v1/events", map[string]string{
		"author": "acme-dev3", "kind": "question", "audience": "po", "title": "keyset or offset?",
	}, http.StatusCreated)
	path := "/v1/events/" + itoa(int64(created["id"].(float64)))

	out := h.mustDo("POST", path+"/explanation", map[string]string{
		"author": "acme-dev3",
		"body":   `<p>Read <a href="https://example.com">this</a>.</p><script>alert(1)</script>`,
	}, http.StatusOK)
	body := out["explanation"].(string)
	if strings.Contains(strings.ToLower(body), "<script") {
		t.Fatalf("a script survived: %q", body)
	}
	if !strings.Contains(body, "example.com") {
		t.Fatalf("the link was dropped: %q", body)
	}
}

// The table answered "how long in this status" and nothing else, so a session
// declaring every minute and one that died an hour ago rendered identically.
// Issue #17.
func TestARowCarriesBothHowLongAndWhetherAnythingIsArriving(t *testing.T) {
	h := newHarness(t)

	// Two sessions enter their status at the same instant.
	h.mustDo("PUT", "/v1/sessions/acme-dev1", map[string]string{"status": "active"}, http.StatusOK)
	h.mustDo("PUT", "/v1/sessions/acme-dev2", map[string]string{"status": "active"}, http.StatusOK)

	// An hour passes. One keeps declaring the same thing; the other says nothing.
	*h.now = base.Add(time.Hour)
	h.mustDo("PUT", "/v1/sessions/acme-dev1", map[string]string{"status": "active"}, http.StatusOK)

	rows := map[string]map[string]any{}
	for _, raw := range list(h.mustDo("GET", "/v1/po", nil, http.StatusOK)["sessions"]) {
		row := raw.(map[string]any)
		rows[row["name"].(string)] = row
	}

	// since_at keeps its documented behaviour for both: a repeated declaration
	// does not reset the duration in status.
	if rows["acme-dev1"]["since_at"] != rows["acme-dev2"]["since_at"] {
		t.Fatalf("since_at differs: %v vs %v — a heartbeat reset it",
			rows["acme-dev1"]["since_at"], rows["acme-dev2"]["since_at"])
	}
	// But the rows are now distinguishable, which is the whole point.
	if rows["acme-dev1"]["quiet"] == true {
		t.Fatal("a session that just declared is marked quiet")
	}
	if rows["acme-dev2"]["quiet"] != true {
		t.Fatal("a session silent for an hour is not marked")
	}
	if rows["acme-dev1"]["updated_at"] == rows["acme-dev2"]["updated_at"] {
		t.Fatal("updated_at does not separate them either")
	}
}

// The board reads the same rows as the page, so the two cannot disagree.
func TestTheBoardCarriesTheSameFreshness(t *testing.T) {
	h := newHarness(t)
	h.mustDo("PUT", "/v1/sessions/acme-dev2", map[string]string{"status": "active"}, http.StatusOK)
	*h.now = base.Add(time.Hour)

	row := first(h.mustDo("GET", "/v1/state", nil, http.StatusOK)["sessions"])
	if row["quiet"] != true {
		t.Fatalf("the board's row does not carry it: %v", row)
	}
	if row["state"] != "working" {
		t.Fatalf("state = %v — the board reads a derived row, like the page", row["state"])
	}
}

func TestTheQuietThresholdIsTheServices(t *testing.T) {
	h := newHarness(t)
	h.mustDo("PUT", "/v1/sessions/acme-dev2", map[string]string{"status": "active"}, http.StatusOK)

	// Just under the threshold: nothing is said.
	*h.now = base.Add(DefaultConfig.QuietAfter - time.Minute)
	if first(h.mustDo("GET", "/v1/po", nil, http.StatusOK)["sessions"])["quiet"] == true {
		t.Fatal("marked before the threshold")
	}
	*h.now = base.Add(DefaultConfig.QuietAfter + time.Minute)
	if first(h.mustDo("GET", "/v1/po", nil, http.StatusOK)["sessions"])["quiet"] != true {
		t.Fatal("not marked past the threshold")
	}
}

// A session stopped pending another session's work had no state for it: the
// table said "working" while the free-text detail said otherwise, in capitals.
// Issue #26.
func TestASessionPausedOnAnotherIsShownAsSuch(t *testing.T) {
	h := newHarness(t)
	h.mustDo("PUT", "/v1/sessions/acme-dev2", map[string]string{
		"status": "active", "issue": issueURL(1889),
	}, http.StatusOK)
	h.mustDo("PUT", "/v1/sessions/acme-dev4", map[string]string{
		"status": "active", "issue": issueURL(1826),
		"waiting_on": "acme-dev2", "waiting_for": issueURL(1889),
	}, http.StatusOK)

	rows := rowsByName(h)
	if rows["acme-dev4"]["state"] != "waiting_on_peer" {
		t.Fatalf("state = %v, want waiting_on_peer", rows["acme-dev4"]["state"])
	}
	if rows["acme-dev4"]["waiting_on"] != "acme-dev2" {
		t.Fatalf("waiting_on = %v", rows["acme-dev4"]["waiting_on"])
	}
	if rows["acme-dev4"]["waiting_for_label"] != "#1889" {
		t.Fatalf("waiting_for_label = %v", rows["acme-dev4"]["waiting_for_label"])
	}
	// The session it waits on is working, not waiting.
	if rows["acme-dev2"]["state"] != "working" {
		t.Fatalf("acme-dev2 = %v, want working", rows["acme-dev2"]["state"])
	}
}

// It clears itself: nobody has to remember to lift it.
func TestThePauseLiftsWhenTheOtherSessionMovesOn(t *testing.T) {
	h := newHarness(t)
	h.mustDo("PUT", "/v1/sessions/acme-dev2", map[string]string{
		"status": "active", "issue": issueURL(1889),
	}, http.StatusOK)
	h.mustDo("PUT", "/v1/sessions/acme-dev4", map[string]string{
		"status": "active", "issue": issueURL(1826),
		"waiting_on": "acme-dev2", "waiting_for": issueURL(1889),
	}, http.StatusOK)

	// acme-dev2 moves to something else. acme-dev4 said nothing.
	h.mustDo("PUT", "/v1/sessions/acme-dev2", map[string]string{
		"status": "active", "issue": issueURL(1890),
	}, http.StatusOK)

	rows := rowsByName(h)
	if rows["acme-dev4"]["state"] != "working" {
		t.Fatalf("state = %v, want working — the pause did not lift", rows["acme-dev4"]["state"])
	}
	// And the dependency is not shown, so nobody reads a pause that has lifted.
	if _, shown := rows["acme-dev4"]["waiting_on"]; shown {
		t.Fatal("a lifted dependency is still on the row")
	}
}

func TestThePauseLiftsWhenTheOtherSessionRetires(t *testing.T) {
	h := newHarness(t)
	h.mustDo("PUT", "/v1/sessions/acme-dev2", map[string]string{
		"status": "active", "issue": issueURL(1889),
	}, http.StatusOK)
	h.mustDo("PUT", "/v1/sessions/acme-dev4", map[string]string{
		"status": "active", "issue": issueURL(1826),
		"waiting_on": "acme-dev2", "waiting_for": issueURL(1889),
	}, http.StatusOK)
	h.mustDo("DELETE", "/v1/sessions/acme-dev2", nil, http.StatusNoContent)

	if rowsByName(h)["acme-dev4"]["state"] != "working" {
		t.Fatal("the pause survived the session it pointed at")
	}
}

// A session cannot mark itself stopped with nothing to point at — that would be
// the freely-declared flag this design refuses, and it could never lift.
func TestAPauseNeedsSomethingToPointAt(t *testing.T) {
	h := newHarness(t)
	for _, body := range []map[string]string{
		{"status": "active", "waiting_on": "acme-dev2"},
		{"status": "active", "waiting_for": issueURL(1889)},
		{"status": "active", "waiting_on": "acme-dev4", "waiting_for": issueURL(1889)}, // itself
		{"status": "active", "waiting_on": "acme-dev2", "waiting_for": "1889"},         // bare number
	} {
		h.mustDo("PUT", "/v1/sessions/acme-dev4", body, http.StatusBadRequest)
	}
}

// Accepting a status, echoing it back, and displaying another was the worst of
// the three options. It is refused now, and the message says what to use.
func TestDeclaringWaitingIsRefused(t *testing.T) {
	h := newHarness(t)
	_, out := h.do("PUT", "/v1/sessions/acme-dev4", map[string]string{"status": "waiting"})
	msg, _ := out["error"].(string)
	if !strings.Contains(msg, "active or idle") {
		t.Fatalf("error = %q, want it to name what is accepted", msg)
	}
}

func rowsByName(h *harness) map[string]map[string]any {
	out := map[string]map[string]any{}
	for _, raw := range list(h.mustDo("GET", "/v1/po", nil, http.StatusOK)["sessions"]) {
		row := raw.(map[string]any)
		out[row["name"].(string)] = row
	}
	return out
}
