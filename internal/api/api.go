// Package api exposes switchboard over HTTP on the loopback interface. There is
// no authentication: the service runs on the machine, for the sessions on it.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io/fs"
	"net/http"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"switchboard/internal/build"
	"switchboard/internal/richtext"
	"switchboard/internal/store"
	"switchboard/internal/wake"
)

// maxWait bounds a long-poll, so a client that goes away cannot pin a
// connection for ever.
const maxWait = 10 * time.Minute

// Config is what the server runs with.
type Config struct {
	Wake wake.Config
	// UndoWindow is how long the person who answered can take it back. Short on
	// purpose: it catches the wrong button, not a change of mind.
	UndoWindow time.Duration
}

// IssueURL is the address of an issue, or "" when there is none to give.
//
// The service holds no repository of its own to resolve a bare number against,
// and will not be given one: naming another repository is what this repository
// must not do, and a number resolved against a repository the session was not
// working in produces a link to somebody else's issue — worse than no link.
//
// New events are refused a bare number on the way in. Rows written before that
// rule keep theirs, and render as plain text.
func (c Config) IssueURL(issue string) string {
	if isURL(issue) {
		return issue
	}
	return ""
}

// IssueLabel is what the page shows: "#142", whether the session sent the number
// or the whole address.
func (c Config) IssueLabel(issue string) string {
	if issue == "" {
		return ""
	}
	if isURL(issue) {
		if n := path.Base(strings.TrimRight(issue, "/")); n != "" && n != "." && n != "/" {
			return "#" + n
		}
		return issue
	}
	return "#" + strings.TrimPrefix(issue, "#")
}

// issueRule is what an issue has to be, said the way every other rule is.
const issueRule = "an issue takes its full URL — a bare number has no repository"

// validIssue reports whether an issue is one the service can do anything with.
// Empty is fine: not every ask is about an issue.
func validIssue(issue string) bool {
	return issue == "" || isURL(issue)
}

func isURL(s string) bool {
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://")
}

// What a session's row says, worked out by the service rather than declared by
// the session: a status nobody has to keep up to date cannot go stale.
const (
	RowBlocked = "blocked"        // an open `blocked` event
	RowOnYou   = "waiting_on_you" // an open ask sitting with the PO
	RowWorking = "working"
	RowIdle    = "idle"
)

// SessionRow is one line of the sessions table.
type SessionRow struct {
	Name       string    `json:"name"`
	State      string    `json:"state"`
	Issue      string    `json:"issue,omitempty"`
	IssueLabel string    `json:"issue_label,omitempty"`
	IssueURL   string    `json:"issue_url,omitempty"`
	Detail     string    `json:"detail,omitempty"`
	SinceAt    time.Time `json:"since_at"`
}

// DefaultConfig is the shipped behaviour.
var DefaultConfig = Config{Wake: wake.Default, UndoWindow: 10 * time.Second}

// Server routes the HTTP surface.
type Server struct {
	st        *store.Store
	cfg       Config
	page      fs.FS
	mux       *http.ServeMux
	startedAt time.Time
}

// New builds the server. page is served at /, and may be nil.
func New(st *store.Store, cfg Config, page fs.FS) *Server {
	s := &Server{st: st, cfg: cfg, page: page, mux: http.NewServeMux(), startedAt: time.Now()}
	s.routes()
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// A session name is checked here, ahead of routing, because the router
	// answers first otherwise: a name carrying a slash lands on a path with no
	// handler, and the caller gets "405 Method Not Allowed" — which says
	// nothing about what it did wrong, to a caller that is usually a program.
	//
	// Only on the call that creates a row. Retiring one must work whatever it
	// is called: names were unchecked before this rule existed, and a row the
	// current rule rejects would otherwise be stuck on the PO's table for good
	// — the permanent row that retiring exists to remove.
	if r.Method == http.MethodPut {
		if name, ok := strings.CutPrefix(r.URL.Path, sessionsPrefix); ok {
			if err := validSessionName(name); err != nil {
				fail(w, http.StatusBadRequest, err.Error())
				return
			}
		}
	}
	s.mux.ServeHTTP(w, r)
}

const sessionsPrefix = "/v1/sessions/"

// maxSessionName bounds a name so it stays readable in the PO's table.
const maxSessionName = 64

// sessionName is what a session may be called: the shape of the names Claude
// Code gives sessions, and nothing that needs escaping in a path.
var sessionName = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// validSessionName reports why a name cannot be used, in the shape of every
// other message this API answers with.
func validSessionName(name string) error {
	switch {
	case name == "":
		return errors.New("a session name is required")
	case len(name) > maxSessionName:
		return fmt.Errorf("a session name is at most %d characters — got %d", maxSessionName, len(name))
	case !sessionName.MatchString(name):
		return fmt.Errorf("a session name holds letters, digits, '-', '_' or '.' — got %q", clip(name, 40))
	}
	return nil
}

// clip keeps a rejected value short in the answer it is quoted back in.
func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// route is one entry of the routing table. The table is the single place a
// route is declared, so the specification can be checked against it.
type route struct {
	method  string
	path    string
	handler http.HandlerFunc
}

// Pattern is what http.ServeMux registers, and what the specification is
// compared to: "GET /v1/state".
func (r route) Pattern() string { return r.method + " " + r.path }

// table lists every route the service answers.
func (s *Server) table() []route {
	return []route{
		// Published by a dev, read by whoever it is addressed to.
		{http.MethodPost, "/v1/events", s.addEvent},
		{http.MethodGet, "/v1/events/{id}", s.getEvent},
		{http.MethodGet, "/v1/events/{id}/reply", s.awaitReply},
		{http.MethodPost, "/v1/events/{id}/seen", s.markSeen},
		{http.MethodPost, "/v1/events/{id}/replies", s.addReply},
		{http.MethodPost, "/v1/events/{id}/undo", s.undo},
		{http.MethodPost, "/v1/events/{id}/withdraw", s.withdraw},
		{http.MethodPost, "/v1/events/{id}/reroute", s.reroute},

		{http.MethodPut, "/v1/sessions/{name}", s.putSession},
		{http.MethodDelete, "/v1/sessions/{name}", s.retireSession},

		// The manager's side.
		{http.MethodGet, "/v1/state", s.state},
		{http.MethodGet, "/v1/wake", s.wake},
		{http.MethodPost, "/v1/ack", s.ack},

		// The PO's page.
		{http.MethodGet, "/v1/po", s.po},

		// What is running, and the contract it answers to.
		{http.MethodGet, "/v1/health", s.health},
		{http.MethodGet, "/v1/openapi.yaml", s.openapiYAML},
		{http.MethodGet, "/v1/openapi.json", s.openapiJSON},
	}
}

func (s *Server) routes() {
	for _, r := range s.table() {
		s.mux.HandleFunc(r.Pattern(), r.handler)
	}
	if s.page != nil {
		s.mux.Handle("GET /", http.FileServer(http.FS(s.page)))
	}
}

// --- devs -------------------------------------------------------------------

type eventRequest struct {
	Author   string   `json:"author"`
	Role     string   `json:"role"`
	Kind     string   `json:"kind"`
	Title    string   `json:"title"`
	Body     string   `json:"body"`
	Link     string   `json:"link"`
	Issue    string   `json:"issue"`
	Audience string   `json:"audience"`
	Options  []string `json:"options"`
}

// maxTitle keeps a title skimmable. Past this it is a body, not a title.
const maxTitle = 120

func (s *Server) addEvent(w http.ResponseWriter, r *http.Request) {
	var req eventRequest
	if !decode(w, r, &req) {
		return
	}
	req.Author, req.Title = strings.TrimSpace(req.Author), strings.TrimSpace(req.Title)
	switch {
	case req.Author == "":
		fail(w, http.StatusBadRequest, "author is required")
		return
	case !store.ValidKind(req.Kind):
		fail(w, http.StatusBadRequest, "kind must be info, question, validation or blocked")
		return
	case req.Title == "":
		fail(w, http.StatusBadRequest, "title is required")
		return
	case strings.ContainsAny(req.Title, "\n\r"):
		// The title is what the PO skims; a paragraph there defeats the point.
		fail(w, http.StatusBadRequest, "a title is one line — put the rest in the body")
		return
	case len([]rune(req.Title)) > maxTitle:
		fail(w, http.StatusBadRequest, fmt.Sprintf("a title is at most %d characters — put the rest in the body", maxTitle))
		return
	case req.Role != "" && !store.ValidRole(req.Role):
		fail(w, http.StatusBadRequest, "role must be dev, manager or po")
		return
	case req.Audience != "" && !store.ValidAudience(req.Audience):
		fail(w, http.StatusBadRequest, "audience must be manager or po")
		return
	case !validIssue(req.Issue):
		fail(w, http.StatusBadRequest, issueRule)
		return
	case req.Kind == store.KindValidation && req.Link == "":
		// A validation without a link is a validation nobody can give.
		fail(w, http.StatusBadRequest, "a validation must carry a link")
		return
	}

	e, err := s.st.AddEvent(store.Event{
		Author: req.Author, Role: req.Role, Kind: req.Kind,
		Title: req.Title, Body: richtext.Clean(req.Body), Link: req.Link,
		Issue: req.Issue, Audience: req.Audience, Options: req.Options,
	})
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	write(w, http.StatusCreated, e)
}

func (s *Server) getEvent(w http.ResponseWriter, r *http.Request) {
	e, err := s.event(w, r)
	if err != nil {
		return
	}
	write(w, http.StatusOK, e)
}

// awaitReply blocks until the event has an answer, so a dev can go back to work
// on its own. It answers 204 if the wait runs out first.
func (s *Server) awaitReply(w http.ResponseWriter, r *http.Request) {
	id, ok := eventID(w, r)
	if !ok {
		return
	}
	deadline := time.Now().Add(waitFor(r))
	for {
		changed := s.st.Changed()
		e, err := s.st.Event(id)
		if err != nil {
			failStore(w, err)
			return
		}
		// An answer is one way to stop waiting; a withdrawal is another. A
		// caller blocked on its own question has to tell them apart, or it
		// resumes as if it had a verdict.
		if e.Reply != nil || e.State != store.StateOpen {
			write(w, http.StatusOK, e)
			return
		}
		if !sleep(r.Context(), changed, deadline, time.Time{}) {
			w.WriteHeader(http.StatusNoContent)
			return
		}
	}
}

type sessionRequest struct {
	Status string `json:"status"`
	Issue  string `json:"issue"`
	Detail string `json:"detail"`
}

func (s *Server) putSession(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := validSessionName(name); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	var req sessionRequest
	if !decode(w, r, &req) {
		return
	}
	if !store.ValidStatus(req.Status) {
		fail(w, http.StatusBadRequest, "status must be active, waiting or idle")
		return
	}
	if !validIssue(req.Issue) {
		fail(w, http.StatusBadRequest, issueRule)
		return
	}
	sess, err := s.st.SaveSession(name, req.Status, req.Issue, req.Detail)
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	write(w, http.StatusOK, sess)
}

// retireSession removes a session's row. Its events stay — they are the trace of
// decisions, not the session's property.
func (s *Server) retireSession(w http.ResponseWriter, r *http.Request) {
	// Deliberately not validSessionName: see ServeHTTP. A row that exists can
	// always be removed, whatever it is called.
	name := r.PathValue("name")
	if name == "" {
		fail(w, http.StatusBadRequest, "a session name is required")
		return
	}
	err := s.st.RetireSession(name)
	switch {
	case errors.Is(err, store.ErrNotFound):
		fail(w, http.StatusNotFound, "no such session")
		return
	case errors.Is(err, store.ErrStillWaiting):
		fail(w, http.StatusConflict, err.Error())
		return
	case err != nil:
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- answers ----------------------------------------------------------------

type replyRequest struct {
	Author string `json:"author"`
	Text   string `json:"text"`
	Option string `json:"option"`
}

func (s *Server) addReply(w http.ResponseWriter, r *http.Request) {
	id, ok := eventID(w, r)
	if !ok {
		return
	}
	var req replyRequest
	if !decode(w, r, &req) {
		return
	}
	req.Author = strings.TrimSpace(req.Author)
	if req.Author == "" {
		fail(w, http.StatusBadRequest, "author is required")
		return
	}
	if strings.TrimSpace(req.Text) == "" && strings.TrimSpace(req.Option) == "" {
		fail(w, http.StatusBadRequest, "an answer needs a text or an option")
		return
	}
	reply, err := s.st.AddReply(id, req.Author, req.Text, req.Option)
	if errors.Is(err, store.ErrNotFound) {
		fail(w, http.StatusNotFound, "no such event")
		return
	}
	if err != nil {
		// Answering an event that already has an answer is a mistake, not an edit.
		fail(w, http.StatusConflict, err.Error())
		return
	}
	write(w, http.StatusCreated, reply)
}

// markSeen records that the asker has read the answer. The CLI calls it right
// after `await` comes back, which is what lets an undo know it is too late to
// be invisible.
func (s *Server) markSeen(w http.ResponseWriter, r *http.Request) {
	id, ok := eventID(w, r)
	if !ok {
		return
	}
	if err := s.st.MarkReplySeen(id); err != nil {
		failStore(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type undoRequest struct {
	Author string `json:"author"`
}

// undo takes an answer back inside the window and reopens the event.
//
// If the asker had already read it, erasing it silently would leave them
// working on a decision that no longer holds. So the correction is raised to
// the manager, whose job is to reach the dev — the tool never talks to a
// session itself.
func (s *Server) undo(w http.ResponseWriter, r *http.Request) {
	id, ok := eventID(w, r)
	if !ok {
		return
	}
	var req undoRequest
	if !decode(w, r, &req) {
		return
	}
	if req.Author == "" {
		req.Author = store.AudiencePO
	}

	e, wasRead, err := s.st.UndoReply(id, req.Author, s.cfg.UndoWindow)
	if errors.Is(err, store.ErrNotFound) {
		fail(w, http.StatusNotFound, "this event has no answer to take back")
		return
	}
	if err != nil {
		fail(w, http.StatusConflict, err.Error())
		return
	}

	res := UndoResponse{Event: e, WasRead: wasRead}
	if wasRead {
		catchUp, err := s.st.AddEvent(store.Event{
			Author:   req.Author,
			Role:     store.RolePO,
			Kind:     store.KindQuestion,
			Audience: store.AudienceManager,
			Issue:    e.Issue,
			Title:    fmt.Sprintf("Answer withdrawn on #%d — %s has already moved on", e.ID, e.Author),
			Body: richtext.Clean(fmt.Sprintf(
				"<p>%s asked: <strong>%s</strong>. The answer was taken back after they had read it, "+
					"so they are working on a decision that no longer holds. They need to hear it from you.</p>",
				htmlEscape(e.Author), htmlEscape(e.Title))),
		})
		if err != nil {
			fail(w, http.StatusInternalServerError, err.Error())
			return
		}
		res.CatchUpID = catchUp.ID
	}
	write(w, http.StatusOK, res)
}

// UndoResponse says what an undo did.
type UndoResponse struct {
	Event store.Event `json:"event"`
	// WasRead is true when the asker had already read the answer.
	WasRead bool `json:"was_read"`
	// CatchUpID is the event raised to the manager so they can reach that dev.
	CatchUpID int64 `json:"catch_up_id,omitempty"`
}

type withdrawRequest struct {
	Author string `json:"author"`
}

// withdraw closes an open ask that turned out not to need an answer — the dev
// found it themselves, or the manager read the issue and saw the decision was
// already written.
//
// It is refused on an answered event: taking an answer back is undo, and the
// two must not overlap.
func (s *Server) withdraw(w http.ResponseWriter, r *http.Request) {
	id, ok := eventID(w, r)
	if !ok {
		return
	}
	var req withdrawRequest
	if !decode(w, r, &req) {
		return
	}
	req.Author = strings.TrimSpace(req.Author)
	if req.Author == "" {
		fail(w, http.StatusBadRequest, "author is required: someone is accountable for the card disappearing")
		return
	}

	e, err := s.st.Withdraw(id, req.Author)
	switch {
	case errors.Is(err, store.ErrNotFound):
		fail(w, http.StatusNotFound, "no such event")
		return
	case errors.Is(err, store.ErrNotOpen):
		fail(w, http.StatusConflict, err.Error())
		return
	case errors.Is(err, store.ErrNotAllowed):
		fail(w, http.StatusForbidden, err.Error())
		return
	case err != nil:
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	write(w, http.StatusOK, s.withIssueURLs([]store.Event{e})[0])
}

type rerouteRequest struct {
	Audience string `json:"audience"`
}

func (s *Server) reroute(w http.ResponseWriter, r *http.Request) {
	id, ok := eventID(w, r)
	if !ok {
		return
	}
	var req rerouteRequest
	if !decode(w, r, &req) {
		return
	}
	if !store.ValidAudience(req.Audience) {
		fail(w, http.StatusBadRequest, "audience must be manager or po")
		return
	}
	if err := s.st.Reroute(id, req.Audience); err != nil {
		failStore(w, err)
		return
	}
	e, err := s.st.Event(id)
	if err != nil {
		failStore(w, err)
		return
	}
	write(w, http.StatusOK, e)
}

// --- the manager ------------------------------------------------------------

// StateResponse is everything the manager needs, in one call.
type StateResponse struct {
	Sessions []store.Session `json:"sessions"`
	Waiting  []store.Event   `json:"waiting"` // open, addressed to the manager, needs a decision
	Infos    []store.Event   `json:"infos"`   // open, addressed to the manager, needs nothing
	WithPO   []store.Event   `json:"with_po"` // open, sitting with the PO
	Answers  []store.Event   `json:"answers"` // answered, not read yet — the return trip
	Pending  int             `json:"pending"` // what a wake signal would count
}

// state is the one call that replaces being interrupted: everything, at once.
func (s *Server) state(w http.ResponseWriter, r *http.Request) {
	sessions, err := s.st.Sessions()
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	mine, err := s.st.OpenEvents(store.AudienceManager)
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	withPO, err := s.st.OpenEvents(store.AudiencePO)
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	answers, err := s.st.AnsweredFor(store.AudienceManager)
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	items, err := s.st.PendingFor(store.AudienceManager)
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}

	res := StateResponse{
		Sessions: sessions, Waiting: []store.Event{}, Infos: []store.Event{},
		WithPO:  s.withIssueURLs(withPO),
		Answers: s.withIssueURLs(answers),
		Pending: len(items),
	}
	mine = s.withIssueURLs(mine)
	for _, e := range mine {
		if e.Kind == store.KindInfo {
			res.Infos = append(res.Infos, e)
		} else {
			res.Waiting = append(res.Waiting, e)
		}
	}
	write(w, http.StatusOK, res)
}

// wake is the long-poll the manager's monitor sits on. It returns a count and
// never the content: the point is to be told once that there is something, not
// to be told what.
func (s *Server) wake(w http.ResponseWriter, r *http.Request) {
	party := r.URL.Query().Get("audience")
	if party == "" {
		party = store.AudienceManager
	}
	if !store.ValidAudience(party) {
		fail(w, http.StatusBadRequest, "audience must be manager or po")
		return
	}
	deadline := time.Now().Add(waitFor(r))
	for {
		changed := s.st.Changed()
		items, err := s.st.PendingFor(party)
		if err != nil {
			fail(w, http.StatusInternalServerError, err.Error())
			return
		}
		cur, err := s.st.Cursor(party)
		if err != nil {
			fail(w, http.StatusInternalServerError, err.Error())
			return
		}
		now := s.st.Now()
		batch, due, next := wake.Due(items, cur, s.cfg.Wake, now)
		if due {
			if err := s.st.SetCursor(party, now, batch.Through); err != nil {
				fail(w, http.StatusInternalServerError, err.Error())
				return
			}
			write(w, http.StatusOK, WakeResponse{Batch: batch, Line: batch.Line()})
			return
		}
		if !sleep(r.Context(), changed, deadline, next) {
			w.WriteHeader(http.StatusNoContent)
			return
		}
	}
}

func (s *Server) ack(w http.ResponseWriter, r *http.Request) {
	n, err := s.st.Ack(store.AudienceManager, store.AudienceManager)
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	write(w, http.StatusOK, map[string]int64{"acknowledged": n})
}

// HealthResponse says what is running and whether it is sound.
type HealthResponse struct {
	OK           bool      `json:"ok"`
	Version      string    `json:"version"`
	Commit       string    `json:"commit"`
	Schema       int       `json:"schema"`
	SchemaTarget int       `json:"schema_target"`
	StartedAt    time.Time `json:"started_at"`
	Database     string    `json:"database"`
}

// health is what `just deploy` checks before calling an upgrade done. It is
// deliberately cheap and needs no arguments.
func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	schema, err := store.SchemaVersion(s.st.DB())
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	res := HealthResponse{
		OK:           schema == store.SchemaTarget(),
		Version:      build.Version,
		Commit:       build.Commit,
		Schema:       schema,
		SchemaTarget: store.SchemaTarget(),
		StartedAt:    s.startedAt,
		Database:     s.st.Path(),
	}
	code := http.StatusOK
	if !res.OK {
		code = http.StatusServiceUnavailable
	}
	write(w, code, res)
}

// --- the PO -----------------------------------------------------------------

// POResponse is what the page reads.
type POResponse struct {
	Sessions []SessionRow  `json:"sessions"`
	ForYou   []store.Event `json:"for_you"` // everything addressed to the PO, and nothing else
	Infos    []store.Event `json:"infos"`   // the folded-away zone; never in the flow
	// Recent is what the PO just answered and can still take back. It is sent
	// so that a page reload does not lose the chance to undo.
	Recent []store.Event `json:"recent"`
	// UndoWindow, in seconds, is the server's truth about how long that lasts.
	UndoWindow int `json:"undo_window"`
}

// po is what the page reads. An `info` never reaches ForYou: a text scrolling
// while the PO reads is the defect this whole tool exists to remove.
func (s *Server) po(w http.ResponseWriter, r *http.Request) {
	sessions, err := s.st.Sessions()
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	open, err := s.st.OpenEvents(store.AudiencePO)
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	infos, err := s.st.OpenEvents(store.AudienceManager)
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}

	recent, err := s.st.AnsweredWithin(store.AudiencePO, s.cfg.UndoWindow)
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}

	open, infos = s.withIssueURLs(open), s.withIssueURLs(infos)
	res := POResponse{
		Sessions:   s.rows(sessions, open, infos),
		ForYou:     []store.Event{},
		Infos:      []store.Event{},
		Recent:     s.withIssueURLs(recent),
		UndoWindow: int(s.cfg.UndoWindow / time.Second),
	}
	for _, e := range open {
		if e.Kind == store.KindInfo {
			res.Infos = append(res.Infos, e)
			continue
		}
		res.ForYou = append(res.ForYou, e)
	}
	for _, e := range infos {
		if e.Kind == store.KindInfo {
			res.Infos = append(res.Infos, e)
		}
	}
	write(w, http.StatusOK, res)
}

// WakeResponse is one grouped signal: how many, never what.
type WakeResponse struct {
	wake.Batch
	Line string `json:"line"`
}

// htmlEscape keeps content out of the markup the catch-up builds around it.
func htmlEscape(s string) string { return html.EscapeString(s) }

// withIssueURLs fills in the address of each event's issue, so a page can link
// it without knowing the repository.
func (s *Server) withIssueURLs(events []store.Event) []store.Event {
	for i := range events {
		events[i].IssueLabel = s.cfg.IssueLabel(events[i].Issue)
		events[i].IssueURL = s.cfg.IssueURL(events[i].Issue)
	}
	return events
}

// rows turns the declared sessions into the table the PO reads. The two states
// that matter most — blocked, and waiting on you — are worked out here from the
// open events, so no session has to declare and refresh them.
func (s *Server) rows(sessions []store.Session, open ...[]store.Event) []SessionRow {
	blocked := map[string]bool{}
	onPO := map[string]bool{}
	for _, list := range open {
		for _, e := range list {
			if e.State != store.StateOpen {
				continue
			}
			if e.Kind == store.KindBlocked {
				blocked[e.Author] = true
			}
			if e.Audience == store.AudiencePO && e.Kind != store.KindInfo {
				onPO[e.Author] = true
			}
		}
	}

	out := make([]SessionRow, 0, len(sessions))
	for _, sess := range sessions {
		row := SessionRow{
			Name: sess.Name, Issue: sess.Issue, Detail: sess.Detail,
			SinceAt:    sess.SinceAt,
			IssueLabel: s.cfg.IssueLabel(sess.Issue),
			IssueURL:   s.cfg.IssueURL(sess.Issue),
		}
		switch {
		case blocked[sess.Name]:
			row.State = RowBlocked
		case onPO[sess.Name]:
			row.State = RowOnYou
		case sess.Status == store.StatusIdle:
			row.State = RowIdle
		default:
			row.State = RowWorking
		}
		out = append(out, row)
	}
	return out
}

// --- plumbing ---------------------------------------------------------------

// sleep waits for the next thing that could change the answer. It reports false
// when the caller should give up: the wait ran out, or the client went away.
func sleep(ctx context.Context, changed <-chan struct{}, deadline, next time.Time) bool {
	now := time.Now()
	if !now.Before(deadline) {
		return false
	}
	wait := deadline.Sub(now)
	// A batch that ripens on its own clock wakes us at that moment, not later.
	if !next.IsZero() {
		if d := next.Sub(now); d > 0 && d < wait {
			wait = d
		}
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-changed:
		return true
	case <-timer.C:
		return time.Now().Before(deadline)
	}
}

func waitFor(r *http.Request) time.Duration {
	raw := r.URL.Query().Get("wait")
	if raw == "" {
		return 0
	}
	secs, err := strconv.Atoi(raw)
	if err != nil || secs < 0 {
		return 0
	}
	d := time.Duration(secs) * time.Second
	if d > maxWait {
		return maxWait
	}
	return d
}

func (s *Server) event(w http.ResponseWriter, r *http.Request) (store.Event, error) {
	id, ok := eventID(w, r)
	if !ok {
		return store.Event{}, errors.New("bad id")
	}
	e, err := s.st.Event(id)
	if err != nil {
		failStore(w, err)
		return store.Event{}, err
	}
	return e, nil
}

func eventID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		fail(w, http.StatusBadRequest, "event id must be a number")
		return 0, false
	}
	return id, true
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(v); err != nil {
		fail(w, http.StatusBadRequest, fmt.Sprintf("invalid body: %v", err))
		return false
	}
	return true
}

func write(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, code int, msg string) {
	write(w, code, map[string]string{"error": msg})
}

func failStore(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrNotFound) {
		fail(w, http.StatusNotFound, "no such event")
		return
	}
	fail(w, http.StatusInternalServerError, err.Error())
}
