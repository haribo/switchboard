package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"
)

// Unreachable is a failure to reach the service at all — a refused connection,
// a dropped one — as opposed to an answer the service gave. A restart looks like
// this, and so does an outage; only time separates them.
type Unreachable struct {
	Addr string
	Err  error
}

func (e Unreachable) Error() string {
	return fmt.Sprintf("switchboard unreachable at %s: %v", e.Addr, e.Err)
}

func (e Unreachable) Unwrap() error { return e.Err }

// DefaultServer is where the service listens unless told otherwise.
const DefaultServer = "http://127.0.0.1:8787"

type client struct {
	base string
	http *http.Client
}

func newClient(base string) *client {
	if base == "" {
		if env := os.Getenv("SWITCHBOARD_URL"); env != "" {
			base = env
		} else {
			base = DefaultServer
		}
	}
	// No overall timeout: the long-polls are the point. The server bounds them.
	return &client{base: base, http: &http.Client{}}
}

// call sends a request and decodes the answer into out. It reports whether the
// server had anything to say (a 204 decodes into nothing).
func (c *client) call(method, path string, body, out any) (bool, error) {
	var reader *bytes.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return false, err
		}
		reader = bytes.NewReader(buf)
	} else {
		reader = bytes.NewReader(nil)
	}
	req, err := http.NewRequest(method, c.base+path, reader)
	if err != nil {
		return false, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.http.Do(req)
	if err != nil {
		// Told apart from an answer the service gave: a caller that can wait
		// out a restart must not wait out a 404.
		return false, Unreachable{Addr: c.base, Err: err}
	}
	defer res.Body.Close()

	if res.StatusCode == http.StatusNoContent {
		return false, nil
	}
	if res.StatusCode >= 400 {
		var e struct {
			Error string `json:"error"`
		}
		json.NewDecoder(res.Body).Decode(&e)
		if e.Error == "" {
			e.Error = res.Status
		}
		return false, fmt.Errorf("%s", e.Error)
	}
	if out != nil {
		if err := json.NewDecoder(res.Body).Decode(out); err != nil {
			return false, err
		}
	}
	return true, nil
}

func secs(d time.Duration) int { return int(d / time.Second) }
