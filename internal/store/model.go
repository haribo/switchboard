package store

import "time"

// Session states. A session is `waiting` when it has asked a human something and
// cannot move until the answer comes back.
const (
	StatusActive  = "active"
	StatusWaiting = "waiting"
	StatusIdle    = "idle"
)

// Event kinds, as named in the brief.
const (
	KindInfo       = "info"       // progress, nobody has to act on it
	KindQuestion   = "question"   // a decision the dev cannot take alone
	KindValidation = "validation" // the PO must look at a link and give a verdict
	KindBlocked    = "blocked"    // the dev is actually stopped
)

// Who an event is addressed to.
const (
	AudienceManager = "manager"
	AudiencePO      = "po"
)

// Who is asking. An ask from the manager is not decided like an ask from a dev:
// one is a choice of direction, the other a detail to unblock. The names alone
// look too alike to tell apart at a glance, so the role is carried explicitly.
const (
	RoleDev     = "dev"
	RoleManager = "manager"
	RolePO      = "po"
)

// Event lifecycle.
const (
	StateOpen     = "open"
	StateAnswered = "answered"
	StateDone     = "done"
)

// Session is the living state of one Claude session. GitHub owns the work; this
// owns where the session currently is.
type Session struct {
	Name      string    `json:"name"`
	Status    string    `json:"status"`
	Issue     string    `json:"issue,omitempty"`
	Detail    string    `json:"detail,omitempty"`
	SinceAt   time.Time `json:"since_at"`   // when it entered this status/issue
	UpdatedAt time.Time `json:"updated_at"` // last sign of life
}

// Event is one typed message published without interrupting anybody.
//
// Title and Body split two readings: the title is what the PO skims to decide
// whether to take this one now, the body is what they read once they have.
type Event struct {
	ID     int64  `json:"id"`
	Author string `json:"author"`
	Role   string `json:"role"`
	Kind   string `json:"kind"`
	Title  string `json:"title"`
	// Body is sanitized HTML — see package richtext. It is never the raw text
	// a session sent.
	Body     string `json:"body,omitempty"`
	Link     string `json:"link,omitempty"`
	Issue    string `json:"issue,omitempty"`
	Audience string `json:"audience"`
	// IssueLabel and IssueURL are filled in on the way out by the API: what to
	// show, and where it points. Neither is stored — a session may send a bare
	// number or a whole address, and how that resolves is a display decision.
	IssueLabel string     `json:"issue_label,omitempty"`
	IssueURL   string     `json:"issue_url,omitempty"`
	Options    []string   `json:"options,omitempty"`
	State      string     `json:"state"`
	CreatedAt  time.Time  `json:"created_at"`
	ClosedAt   *time.Time `json:"closed_at,omitempty"`
	Reply      *Reply     `json:"reply,omitempty"`
}

// Reply is an answer to an event, kept for good.
type Reply struct {
	ID        int64      `json:"id"`
	EventID   int64      `json:"event_id"`
	Author    string     `json:"author"`
	Text      string     `json:"text,omitempty"`
	Option    string     `json:"option,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	SeenAt    *time.Time `json:"seen_at,omitempty"`
}

// NeedsHuman reports whether an event is waiting on somebody. `info` never is:
// it is published so that nobody has to read it now.
func (e *Event) NeedsHuman() bool {
	return e.State == StateOpen && e.Kind != KindInfo
}

// ValidKind reports whether k is one of the four kinds.
func ValidKind(k string) bool {
	switch k {
	case KindInfo, KindQuestion, KindValidation, KindBlocked:
		return true
	}
	return false
}

// ValidStatus reports whether s is one of the three session states.
func ValidStatus(s string) bool {
	switch s {
	case StatusActive, StatusWaiting, StatusIdle:
		return true
	}
	return false
}

// ValidAudience reports whether a is manager or po.
func ValidAudience(a string) bool {
	return a == AudienceManager || a == AudiencePO
}

// ValidRole reports whether r is one of the three roles.
func ValidRole(r string) bool {
	switch r {
	case RoleDev, RoleManager, RolePO:
		return true
	}
	return false
}

// DefaultAudience routes an event on creation: a visual validation is the PO's
// call, everything else reaches the manager, who dispatches.
func DefaultAudience(kind string) string {
	if kind == KindValidation {
		return AudiencePO
	}
	return AudienceManager
}
