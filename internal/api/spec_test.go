package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
)

// The specification and the router must describe the same service. Checked in
// both directions: a route added without a spec entry, and a spec entry for a
// route that no longer exists, are both defects — the second is worse, because
// it lies with authority.
func TestTheSpecificationAndTheRouterAgree(t *testing.T) {
	h := newHarness(t)
	srv := New(h.st, DefaultConfig, nil)

	var routed []string
	for _, r := range srv.table() {
		routed = append(routed, r.Pattern())
	}
	spec, err := SpecPaths()
	if err != nil {
		t.Fatalf("read the specification: %v", err)
	}

	missing := difference(routed, spec)
	if len(missing) > 0 {
		t.Errorf("routes the service answers but the specification does not describe: %v", missing)
	}
	extra := difference(spec, routed)
	if len(extra) > 0 {
		t.Errorf("routes the specification describes but the service does not answer: %v", extra)
	}
}

func difference(a, b []string) []string {
	have := map[string]bool{}
	for _, v := range b {
		have[v] = true
	}
	var out []string
	for _, v := range a {
		if !have[v] {
			out = append(out, v)
		}
	}
	sort.Strings(out)
	return out
}

func TestTheSpecificationIsServedByTheServiceItself(t *testing.T) {
	h := newHarness(t)

	res, err := http.Get(h.URL + "/v1/openapi.yaml")
	if err != nil {
		t.Fatalf("fetch yaml: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("yaml = %d, want 200", res.StatusCode)
	}
	if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/yaml") {
		t.Fatalf("content type = %q", ct)
	}

	doc := h.mustDo("GET", "/v1/openapi.json", nil, http.StatusOK)
	if doc["openapi"] != "3.1.0" {
		t.Fatalf("openapi = %v, want 3.1.0", doc["openapi"])
	}
	info := doc["info"].(map[string]any)
	if info["title"] != "switchboard" {
		t.Fatalf("title = %v", info["title"])
	}
}

// Every enum in the specification must match the values the code accepts;
// otherwise a caller follows the contract and gets a 400.
func TestTheSpecificationEnumsMatchWhatIsAccepted(t *testing.T) {
	doc, err := SpecJSON()
	if err != nil {
		t.Fatalf("spec: %v", err)
	}
	var parsed struct {
		Components struct {
			Schemas map[string]struct {
				Enum []string `json:"enum"`
			} `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(doc, &parsed); err != nil {
		t.Fatalf("parse: %v", err)
	}

	for name, want := range map[string][]string{
		"Kind":          {"info", "question", "validation", "blocked"},
		"Audience":      {"manager", "po"},
		"Role":          {"dev", "manager", "po"},
		"EventState":    {"open", "answered", "done", "withdrawn"},
		"SessionStatus": {"active", "waiting", "idle"},
		"TableState":    {RowBlocked, RowOnYou, RowWorking, RowIdle},
	} {
		got := parsed.Components.Schemas[name].Enum
		if len(got) != len(want) {
			t.Errorf("%s: spec lists %v, code accepts %v", name, got, want)
			continue
		}
		set := map[string]bool{}
		for _, v := range got {
			set[v] = true
		}
		for _, v := range want {
			if !set[v] {
				t.Errorf("%s: %q is accepted by the code but missing from the spec (%v)", name, v, got)
			}
		}
	}
}

// The declared title cap is what the service actually enforces.
func TestTheDeclaredTitleLimitIsTheEnforcedOne(t *testing.T) {
	doc, err := SpecJSON()
	if err != nil {
		t.Fatalf("spec: %v", err)
	}
	var parsed struct {
		Components struct {
			Schemas map[string]struct {
				Properties map[string]struct {
					MaxLength int `json:"maxLength"`
				} `json:"properties"`
			} `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(doc, &parsed); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := parsed.Components.Schemas["EventRequest"].Properties["title"].MaxLength; got != maxTitle {
		t.Fatalf("spec says a title is at most %d, the service enforces %d", got, maxTitle)
	}
}

// A page is served at /, so the spec routes must not be shadowed by it.
func TestTheSpecIsReachableAlongsideThePage(t *testing.T) {
	h := newHarness(t)
	srv := New(h.st, DefaultConfig, stubPage{})
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/openapi.json", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("with a page mounted, /v1/openapi.json = %d", rec.Code)
	}
}

// The shape the specification promises for a session name is the shape the
// service enforces. A contract that describes a different rule than the one
// applied sends callers to a 400 they followed the document to reach.
func TestTheDeclaredSessionNameShapeIsTheEnforcedOne(t *testing.T) {
	doc, err := SpecJSON()
	if err != nil {
		t.Fatalf("spec: %v", err)
	}
	var parsed struct {
		Components struct {
			Parameters map[string]struct {
				Schema struct {
					Pattern   string `json:"pattern"`
					MaxLength int    `json:"maxLength"`
				} `json:"schema"`
			} `json:"parameters"`
		} `json:"components"`
	}
	if err := json.Unmarshal(doc, &parsed); err != nil {
		t.Fatalf("parse: %v", err)
	}

	declared := parsed.Components.Parameters["SessionName"].Schema
	if declared.Pattern != sessionName.String() {
		t.Errorf("spec pattern %q, service enforces %q", declared.Pattern, sessionName.String())
	}
	if declared.MaxLength != maxSessionName {
		t.Errorf("spec maxLength %d, service enforces %d", declared.MaxLength, maxSessionName)
	}

	// And the rule really is the one applied, either way round.
	for _, ok := range []string{"acme-dev3", "a", "D3V.1_x"} {
		if err := validSessionName(ok); err != nil {
			t.Errorf("%q matches the declared pattern but was refused: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "a/b", "a b", "a?b", strings.Repeat("a", maxSessionName+1)} {
		if err := validSessionName(bad); err == nil {
			t.Errorf("%q does not match the declared pattern but was accepted", bad)
		}
	}
}
