// Package cli is the command line every session drives switchboard with. The
// same binary serves and calls.
package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"switchboard/internal/api"
	"switchboard/internal/build"
	"switchboard/internal/store"
	"switchboard/internal/web"
)

const usage = `switchboard — coordination for parallel sessions

  serve    run the service: the API and the PO's page
  state    declare what a session is doing right now
  event    publish a typed event without interrupting anybody
  await    wait for the answer to an event
  board    the whole picture, in one call
  reply    answer an event
  ask      put a question to the PO
  ack      mark as read what only had to be read
  undo     take back an answer just given
  withdraw close an open ask that turned out not to need an answer
  explain  say the same thing in plain words, when the PO asks for it
  retire   remove a session's row from the table
  watch    the grouped signal: one line when a batch needs handling
  db       look after the database: status, backup, migrate
  version  what this binary is

Run "switchboard <command> -h" for the options.
`

// Run executes one command and returns the process exit code.
func Run(args []string) int {
	if len(args) < 1 {
		fmt.Fprint(os.Stderr, usage)
		return 2
	}
	var err error
	switch args[0] {
	case "serve":
		err = serve(args[1:])
	case "state":
		err = state(args[1:])
	case "event":
		err = event(args[1:])
	case "await":
		err = await(args[1:])
	case "board":
		err = board(args[1:])
	case "reply":
		err = reply(args[1:])
	case "ask":
		err = ask(args[1:])
	case "ack":
		err = ack(args[1:])
	case "undo":
		err = undo(args[1:])
	case "withdraw":
		err = withdraw(args[1:])
	case "explain":
		err = explain(args[1:])
	case "retire":
		err = retire(args[1:])
	case "watch":
		err = watch(args[1:])
	case "db":
		err = database(args[1:])
	case "version":
		fmt.Println(build.Line())
		return 0
	case "-h", "--help", "help":
		fmt.Print(usage)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n%s", args[0], usage)
		return 2
	}
	if err != nil {
		var x explainWantedError
		if errors.As(err, &x) {
			// Not a failure either: the PO cannot act on the wording. Say the
			// same thing differently, then wait again.
			fmt.Println(x.Error())
			return ExitExplainWanted
		}
		var w withdrawnError
		if errors.As(err, &w) {
			// Not a failure: the ask was closed without an answer. A distinct
			// code lets a caller branch without parsing the message.
			fmt.Println(w.Error())
			return ExitWithdrawn
		}
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	return 0
}

// ExitWithdrawn is the exit code of `await` when the ask was withdrawn instead
// of answered. A session that blocked on its own question has to tell the two
// apart, or it resumes as if it had a verdict.
const ExitWithdrawn = 3

// withdrawnError reports an ask that was closed without an answer.
type withdrawnError struct {
	id    int64
	title string
	by    string
}

// ExitExplainWanted is the exit code of `await` when the PO asked for the
// question to be put in plain words. It is not an answer and not a refusal:
// reword it with `switchboard explain`, then wait again.
const ExitExplainWanted = 4

// explainWantedError reports that the wording, not the question, is the obstacle.
type explainWantedError struct {
	id    int64
	title string
}

func (e explainWantedError) Error() string {
	return fmt.Sprintf("explain: the PO cannot act on %q as worded (event %d) — "+
		"say the same thing in plain words with `switchboard explain %d --as <you> --body \"…\"`, then await again",
		e.title, e.id, e.id)
}

func (e withdrawnError) Error() string {
	return fmt.Sprintf("withdrawn: %q (event %d, by %s) — no answer is coming, carry on", e.title, e.id, e.by)
}

// repeated collects a flag given more than once (--option A --option B).
type repeated []string

func (r *repeated) String() string     { return strings.Join(*r, ", ") }
func (r *repeated) Set(v string) error { *r = append(*r, v); return nil }

// parse reads flags whatever order they come in. Go's flag package stops at the
// first positional argument, which would reject the natural
// "switchboard await 12 --timeout 10m".
func parse(fs *flag.FlagSet, args []string) []string {
	var positional []string
	for {
		fs.Parse(args)
		rest := fs.Args()
		if len(rest) == 0 {
			return positional
		}
		positional = append(positional, rest[0])
		args = rest[1:]
	}
}

// parseNoArgs is parse for a command that takes no positional argument.
func parseNoArgs(fs *flag.FlagSet, args []string) error {
	if extra := parse(fs, args); len(extra) > 0 {
		return fmt.Errorf("unexpected argument: %s", extra[0])
	}
	return nil
}

// env reads a setting from the environment, so the systemd unit can stay fixed
// and one config file carries the settings.
func env(key, fallback string) string {
	if v := os.Getenv("SWITCHBOARD_" + key); v != "" {
		return v
	}
	return fallback
}

// envDuration is env for a duration; an unreadable value falls back rather than
// stopping the service over a typo in a config file.
func envDuration(key string, fallback time.Duration) time.Duration {
	raw := os.Getenv("SWITCHBOARD_" + key)
	if raw == "" {
		return fallback
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		fmt.Fprintf(os.Stderr, "switchboard: SWITCHBOARD_%s is not a duration (%q), using %s\n", key, raw, fallback)
		return fallback
	}
	return d
}

func flags(name string) (*flag.FlagSet, *string) {
	fs := flag.NewFlagSet(name, flag.ExitOnError)
	server := fs.String("server", "", "service address (default "+DefaultServer+" or $SWITCHBOARD_URL)")
	return fs, server
}

// --- serve ------------------------------------------------------------------

func serve(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	addr := fs.String("addr", env("ADDR", "127.0.0.1:8787"), "listen address")
	db := fs.String("db", env("DB", store.DefaultPath()), "SQLite file")
	cfg := api.DefaultConfig
	fs.DurationVar(&cfg.Wake.Debounce, "debounce", envDuration("DEBOUNCE", cfg.Wake.Debounce),
		"how long a batch gathers before a signal")
	fs.DurationVar(&cfg.Wake.MinInterval, "min-interval", envDuration("MIN_INTERVAL", cfg.Wake.MinInterval),
		"floor between two signals")
	fs.DurationVar(&cfg.Wake.Urgent, "urgent", envDuration("URGENT", cfg.Wake.Urgent),
		"batching delay when a dev is blocked")
	fs.DurationVar(&cfg.UndoWindow, "undo-window", envDuration("UNDO_WINDOW", cfg.UndoWindow),
		"how long an answer can be taken back")
	if err := parseNoArgs(fs, args); err != nil {
		return err
	}

	st, err := store.Open(*db, nil)
	if err != nil {
		return err
	}
	defer st.Close()

	srv := &http.Server{Addr: *addr, Handler: api.New(st, cfg, web.Page())}
	fmt.Println(build.Line())
	fmt.Printf("listening on http://%s — database %s (schema %d)\n", *addr, *db, store.SchemaTarget())
	fmt.Printf("batching %s, floor %s, blocked %s, undo %s\n",
		cfg.Wake.Debounce, cfg.Wake.MinInterval, cfg.Wake.Urgent, cfg.UndoWindow)
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// issueRule is what --issue has to be. The service enforces it too — this is
// here so the message names the flag the caller actually typed.
const issueRule = "--issue takes the issue's full URL — a bare number has no repository"

// checkIssue refuses anything but a full URL. The service holds no repository to
// resolve a bare number against, and will not be given one: a number resolved
// against a repository the session was not working in links to somebody else's
// issue.
func checkIssue(issue string) error {
	if issue == "" || strings.HasPrefix(issue, "http://") || strings.HasPrefix(issue, "https://") {
		return nil
	}
	return errors.New(issueRule)
}

// --- a dev's commands -------------------------------------------------------

func state(args []string) error {
	fs, server := flags("state")
	session := fs.String("session", "", "session name (required)")
	status := fs.String("status", store.StatusActive, "active, waiting or idle")
	issue := fs.String("issue", "", "the issue being worked on, as its full URL")
	detail := fs.String("detail", "", "one short line: what is happening")
	if err := parseNoArgs(fs, args); err != nil {
		return err
	}
	if *session == "" {
		return errors.New("--session is required")
	}
	if err := checkIssue(*issue); err != nil {
		return err
	}
	var out store.Session
	if _, err := newClient(*server).call(http.MethodPut, "/v1/sessions/"+*session,
		map[string]string{"status": *status, "issue": *issue, "detail": *detail}, &out); err != nil {
		return err
	}
	fmt.Printf("%s: %s%s\n", out.Name, out.Status, issueSuffix(out.Issue))
	return nil
}

// retire removes a session's row. Its events stay.
func retire(args []string) error {
	fs, server := flags("retire")
	session := fs.String("session", "", "session to retire (required)")
	rest := parse(fs, args)
	name := *session
	if name == "" && len(rest) == 1 {
		name = rest[0] // switchboard retire acme-dev3
	}
	if name == "" {
		return errors.New("--session is required")
	}
	if _, err := newClient(*server).call(http.MethodDelete, "/v1/sessions/"+name, nil, nil); err != nil {
		return err
	}
	fmt.Printf("%s retired — its events are kept\n", name)
	return nil
}

func event(args []string) error {
	fs, server := flags("event")
	from := fs.String("from", "", "session publishing this (required)")
	role := fs.String("role", store.RoleDev, "dev, manager or po")
	kind := fs.String("kind", "", "info, question, validation or blocked (required)")
	title := fs.String("title", "", "one short line, what this is about (required)")
	body := fs.String("body", "", "the detail; links, bold, code and lists are kept, the rest shows as text")
	link := fs.String("link", "", "address to open (required for a validation)")
	issue := fs.String("issue", "", "the issue this is about, as its full URL")
	audience := fs.String("to", "", "manager or po (default: po for a validation, manager otherwise)")
	wait := fs.Duration("wait", 0, "wait for the answer before returning")
	var options repeated
	fs.Var(&options, "option", "an offered answer (repeatable)")
	if err := parseNoArgs(fs, args); err != nil {
		return err
	}
	if *from == "" || *kind == "" || *title == "" {
		return errors.New("--from, --kind and --title are required")
	}
	if err := checkIssue(*issue); err != nil {
		return err
	}

	c := newClient(*server)
	var e store.Event
	if _, err := c.call(http.MethodPost, "/v1/events", map[string]any{
		"author": *from, "role": *role, "kind": *kind, "title": *title, "body": *body,
		"link": *link, "issue": *issue, "audience": *audience, "options": []string(options),
	}, &e); err != nil {
		return err
	}
	fmt.Printf("%d\n", e.ID)
	if *wait <= 0 {
		return nil
	}
	return waitReply(c, e.ID, *wait)
}

func await(args []string) error {
	fs, server := flags("await")
	timeout := fs.Duration("timeout", time.Hour, "give up after this long")
	id, err := oneID(parse(fs, args))
	if err != nil {
		return err
	}
	return waitReply(newClient(*server), id, *timeout)
}

// waitReply blocks on the server until the event is answered, re-issuing the
// long-poll as it expires. A dev calls this and picks its work back up alone.
func waitReply(c *client, id int64, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		left := time.Until(deadline)
		if left <= 0 {
			return fmt.Errorf("no answer to event %d after %s", id, timeout)
		}
		if left > 5*time.Minute {
			left = 5 * time.Minute
		}
		var e store.Event
		got, err := c.call(http.MethodGet,
			fmt.Sprintf("/v1/events/%d/reply?wait=%d", id, secs(left)), nil, &e)
		if err != nil {
			return err
		}
		if got && e.Reply != nil {
			// Say it has been read: an undo must know it is too late to be
			// invisible.
			c.call(http.MethodPost, fmt.Sprintf("/v1/events/%d/seen", id), nil, nil)
			fmt.Println(answerLine(e))
			return nil
		}
		if got && e.State == store.StateWithdrawn {
			return withdrawnError{id: e.ID, title: e.Title, by: e.ClosedBy}
		}
		if got && e.ExplainPending {
			return explainWantedError{id: e.ID, title: e.Title}
		}
	}
}

// --- the manager's commands -------------------------------------------------

func board(args []string) error {
	fs, server := flags("board")
	asJSON := fs.Bool("json", false, "raw JSON output")
	if err := parseNoArgs(fs, args); err != nil {
		return err
	}

	c := newClient(*server)
	if *asJSON {
		var raw map[string]any
		if _, err := c.call(http.MethodGet, "/v1/state", nil, &raw); err != nil {
			return err
		}
		return writeJSON(os.Stdout, raw)
	}
	var st api.StateResponse
	if _, err := c.call(http.MethodGet, "/v1/state", nil, &st); err != nil {
		return err
	}
	printBoard(os.Stdout, st)
	return nil
}

func printBoard(w io.Writer, st api.StateResponse) {
	fmt.Fprintln(w, "SESSIONS")
	if len(st.Sessions) == 0 {
		fmt.Fprintln(w, "  (none)")
	}
	waiting := map[string]bool{}
	for _, e := range st.WithPO {
		waiting[e.Author] = true
	}
	sorted := append([]store.Session(nil), st.Sessions...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })
	for _, s := range sorted {
		flag := ""
		if waiting[s.Name] {
			flag = "  (with the PO)"
		}
		fmt.Fprintf(w, "  %-14s %-8s %-8s %s%s\n",
			s.Name, s.Status, orDash(issueTag(s.Issue)), age(s.SinceAt), flag)
	}

	section(w, "NEEDS YOU", st.Waiting, func(e store.Event) {
		fmt.Fprintf(w, "  #%d  %-10s %-14s %-6s %s\n",
			e.ID, e.Kind, e.Author, orDash(issueTag(e.Issue)), e.Title)
		if len(e.Options) > 0 {
			fmt.Fprintf(w, "       options: %s\n", strings.Join(e.Options, " | "))
		}
	})
	section(w, "ANSWERS FROM THE PO", st.Answers, func(e store.Event) {
		fmt.Fprintf(w, "  #%d  %s\n", e.ID, answerLine(e))
	})
	section(w, "WITH THE PO", st.WithPO, func(e store.Event) {
		fmt.Fprintf(w, "  #%d  %-10s %-14s %s%s\n", e.ID, e.Kind, e.Author, e.Title, linkSuffix(e.Link))
	})
	section(w, "INFO", st.Infos, func(e store.Event) {
		fmt.Fprintf(w, "  #%d  %-14s %s\n", e.ID, e.Author, e.Title)
	})
}

func section(w io.Writer, title string, events []store.Event, line func(store.Event)) {
	if len(events) == 0 {
		return
	}
	fmt.Fprintf(w, "\n%s (%d)\n", title, len(events))
	for _, e := range events {
		line(e)
	}
}

func reply(args []string) error {
	fs, server := flags("reply")
	text := fs.String("text", "", "free-text answer")
	option := fs.String("option", "", "the option taken")
	as := fs.String("as", store.AudienceManager, "who is answering: manager or po")
	id, err := oneID(parse(fs, args))
	if err != nil {
		return err
	}
	if *text == "" && *option == "" {
		return errors.New("--text or --option is required")
	}
	var r store.Reply
	if _, err := newClient(*server).call(http.MethodPost,
		fmt.Sprintf("/v1/events/%d/replies", id),
		map[string]string{"author": *as, "text": *text, "option": *option}, &r); err != nil {
		return err
	}
	fmt.Printf("event %d answered\n", id)
	return nil
}

func ask(args []string) error {
	fs, server := flags("ask")
	title := fs.String("title", "", "the question, one line (required)")
	body := fs.String("body", "", "the detail; links, bold, code and lists are kept")
	link := fs.String("link", "", "address to open — makes it a validation")
	issue := fs.String("issue", "", "the issue this is about, as its full URL")
	from := fs.String("from", store.AudienceManager, "who is asking")
	forward := fs.Int64("forward", 0, "hand this event id to the PO instead of raising a new one")
	var options repeated
	fs.Var(&options, "option", "an offered answer (repeatable)")
	if err := parseNoArgs(fs, args); err != nil {
		return err
	}

	c := newClient(*server)
	if *forward > 0 {
		var e store.Event
		if _, err := c.call(http.MethodPost, fmt.Sprintf("/v1/events/%d/reroute", *forward),
			map[string]string{"audience": store.AudiencePO}, &e); err != nil {
			return err
		}
		fmt.Printf("event %d handed to the PO\n", e.ID)
		return nil
	}
	if *title == "" {
		return errors.New("--title is required")
	}
	if err := checkIssue(*issue); err != nil {
		return err
	}
	kind := store.KindQuestion
	if *link != "" {
		kind = store.KindValidation
	}
	var e store.Event
	if _, err := c.call(http.MethodPost, "/v1/events", map[string]any{
		"author": *from, "role": store.RoleManager, "kind": kind,
		"title": *title, "body": *body, "link": *link, "issue": *issue,
		"audience": store.AudiencePO, "options": []string(options),
	}, &e); err != nil {
		return err
	}
	fmt.Printf("%d\n", e.ID)
	return nil
}

func ack(args []string) error {
	fs, server := flags("ack")
	if err := parseNoArgs(fs, args); err != nil {
		return err
	}
	var out map[string]int64
	if _, err := newClient(*server).call(http.MethodPost, "/v1/ack", nil, &out); err != nil {
		return err
	}
	fmt.Printf("%d item(s) marked as read\n", out["acknowledged"])
	return nil
}

// undo takes back an answer given moments ago. The server holds the window and
// says no once it has passed.
func undo(args []string) error {
	fs, server := flags("undo")
	as := fs.String("as", store.AudiencePO, "who wrote the answer: po or manager")
	id, err := oneID(parse(fs, args))
	if err != nil {
		return err
	}
	var res api.UndoResponse
	if _, err := newClient(*server).call(http.MethodPost,
		fmt.Sprintf("/v1/events/%d/undo", id), map[string]string{"author": *as}, &res); err != nil {
		return err
	}
	fmt.Printf("answer taken back, event %d is waiting again\n", id)
	if res.WasRead {
		fmt.Printf("%s had already read it: raised to the manager as #%d\n", res.Event.Author, res.CatchUpID)
	}
	return nil
}

// withdraw closes an open ask that no longer needs an answer.
func withdraw(args []string) error {
	fs, server := flags("withdraw")
	as := fs.String("as", "", "who is withdrawing it: the event's author, or manager (required)")
	id, err := oneID(parse(fs, args))
	if err != nil {
		return err
	}
	if *as == "" {
		return errors.New("--as is required: someone is accountable for the card disappearing")
	}
	var e store.Event
	if _, err := newClient(*server).call(http.MethodPost,
		fmt.Sprintf("/v1/events/%d/withdraw", id), map[string]string{"author": *as}, &e); err != nil {
		return err
	}
	fmt.Printf("event %d withdrawn by %s\n", e.ID, e.ClosedBy)
	return nil
}

// explain publishes the plain-words version of an ask the PO could not act on.
func explain(args []string) error {
	fs, server := flags("explain")
	as := fs.String("as", "", "the ask's author, or manager (required)")
	body := fs.String("body", "", "the same thing said differently (required)")
	id, err := oneID(parse(fs, args))
	if err != nil {
		return err
	}
	if *as == "" || *body == "" {
		return errors.New("--as and --body are required")
	}
	var e store.Event
	if _, err := newClient(*server).call(http.MethodPost,
		fmt.Sprintf("/v1/events/%d/explanation", id),
		map[string]string{"author": *as, "body": *body}, &e); err != nil {
		return err
	}
	fmt.Printf("event %d explained — the original wording is kept\n", e.ID)
	return nil
}

// --- the grouped signal -----------------------------------------------------

func watch(args []string) error {
	fs, server := flags("watch")
	party := fs.String("for", store.AudienceManager, "manager or po")
	once := fs.Bool("once", false, "stop after the first batch")
	poll := fs.Duration("poll", 5*time.Minute, "length of one long wait")
	downAfter := fs.Duration("down-alert", 2*time.Minute, "report an unreachable service after this long")
	if err := parseNoArgs(fs, args); err != nil {
		return err
	}

	c := newClient(*server)
	path := fmt.Sprintf("/v1/wake?audience=%s&wait=%d", *party, secs(*poll))
	var down outage
	for {
		var batch api.WakeResponse
		got, err := c.call(http.MethodGet, path, nil, &batch)
		if err != nil {
			// The watch must outlive a restart of the service: a subscription
			// that dies stops waking anybody, silently.
			fmt.Fprintln(os.Stderr, "switchboard:", err)
			if line := down.report(time.Now(), *downAfter); line != "" {
				// Standard output, deliberately: a service that is down must
				// reach the manager, not sit in a log nobody reads. Silence
				// and "nothing to do" must never look alike.
				fmt.Println(line)
			}
			time.Sleep(5 * time.Second)
			continue
		}
		down.clear()
		if !got {
			continue // nothing ripe; the long-poll simply expired
		}
		// One line, one notification. Counts only, never the content.
		fmt.Printf("%s — `switchboard board`\n", batch.Line)
		if *once {
			return nil
		}
	}
}

// outage tracks a service that stopped answering, so the watch says so once —
// and only once — rather than every five seconds or never.
type outage struct {
	since   time.Time
	alerted bool
}

// report returns the line to print, or "" when there is nothing new to say.
func (o *outage) report(now time.Time, after time.Duration) string {
	if o.since.IsZero() {
		o.since = now
	}
	if o.alerted || now.Sub(o.since) < after {
		return ""
	}
	o.alerted = true
	return fmt.Sprintf("switchboard unreachable for %s — nothing is getting through", roundDuration(now.Sub(o.since)))
}

// clear forgets an outage that is over, so the next one is announced again.
func (o *outage) clear() { *o = outage{} }

func roundDuration(d time.Duration) time.Duration {
	if d < time.Minute {
		return d.Round(time.Second)
	}
	return d.Round(time.Minute)
}

// --- shared -----------------------------------------------------------------

func oneID(args []string) (int64, error) {
	if len(args) != 1 {
		return 0, errors.New("one event id is expected")
	}
	id, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("not an event id: %s", args[0])
	}
	return id, nil
}

func answerLine(e store.Event) string {
	if e.Reply == nil {
		return e.Title
	}
	answer := e.Reply.Option
	if answer == "" {
		answer = e.Reply.Text
	} else if e.Reply.Text != "" {
		answer += " — " + e.Reply.Text
	}
	return fmt.Sprintf("%q → %s (%s)", e.Title, answer, e.Reply.Author)
}

func issueSuffix(issue string) string {
	if issue == "" {
		return ""
	}
	return " #" + issue
}

func issueTag(issue string) string {
	if issue == "" {
		return ""
	}
	return "#" + issue
}

func linkSuffix(l string) string {
	if l == "" {
		return ""
	}
	return "  " + l
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func age(t time.Time) string {
	mins := int(time.Since(t).Minutes())
	switch {
	case mins < 1:
		return "just now"
	case mins < 60:
		return fmt.Sprintf("%d min", mins)
	default:
		return fmt.Sprintf("%dh%02d", mins/60, mins%60)
	}
}

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
