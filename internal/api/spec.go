package api

import (
	_ "embed"
	"encoding/json"
	"net/http"
	"sync"

	"sigs.k8s.io/yaml"
)

// The contract, served by the service itself so it can only ever describe the
// version that is answering.
//
//go:embed openapi.yaml
var specYAML []byte

var (
	specOnce sync.Once
	specJSON []byte
	specErr  error
)

// SpecYAML is the specification as written.
func SpecYAML() []byte { return specYAML }

// SpecJSON is the same document, for callers that would rather not parse YAML.
func SpecJSON() ([]byte, error) {
	specOnce.Do(func() { specJSON, specErr = yaml.YAMLToJSON(specYAML) })
	return specJSON, specErr
}

func (s *Server) openapiYAML(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	w.Write(specYAML)
}

func (s *Server) openapiJSON(w http.ResponseWriter, r *http.Request) {
	doc, err := SpecJSON()
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Write(doc)
}

// SpecPaths lists the method-and-path pairs the specification describes, in the
// same shape the router uses ("GET /v1/state"). The consistency test compares it
// to the routing table, in both directions.
func SpecPaths() ([]string, error) {
	doc, err := SpecJSON()
	if err != nil {
		return nil, err
	}
	var parsed struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(doc, &parsed); err != nil {
		return nil, err
	}
	methods := map[string]string{
		"get": http.MethodGet, "put": http.MethodPut, "post": http.MethodPost,
		"delete": http.MethodDelete, "patch": http.MethodPatch,
	}
	var out []string
	for path, ops := range parsed.Paths {
		for op := range ops {
			if m, ok := methods[op]; ok {
				out = append(out, m+" "+path)
			}
		}
	}
	return out, nil
}
