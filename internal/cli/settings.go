package cli

import (
	"fmt"
	"os"
	"sort"
	"strings"
)

// The settings this binary reads. A name absent from here is either a typo or a
// setting the product has since dropped — and the operator needs to be told
// which, because one is deleted and the other is hunted for.
var knownSettings = map[string]bool{
	"SWITCHBOARD_ADDR":         true,
	"SWITCHBOARD_DB":           true,
	"SWITCHBOARD_DEBOUNCE":     true,
	"SWITCHBOARD_MIN_INTERVAL": true,
	"SWITCHBOARD_URGENT":       true,
	"SWITCHBOARD_UNDO_WINDOW":  true,
	// Read by the client, not the server, but it legitimately sits in the same
	// environment — naming it as unknown would send the operator after nothing.
	"SWITCHBOARD_URL": true,
}

// retiredSettings are names the product used to read. They are named apart from
// unknown ones: a stale line is deleted, a misspelled one is corrected, and
// telling the operator which they are looking at is the whole point.
var retiredSettings = map[string]string{
	"SWITCHBOARD_REPO": "issues now carry their full URL",
}

// straySettings returns the unknown and the retired names present in env, each
// sorted, so a startup report is stable from one run to the next.
func straySettings(env []string) (unknown []string, retired []string) {
	for _, entry := range env {
		name, _, ok := strings.Cut(entry, "=")
		if !ok || !strings.HasPrefix(name, "SWITCHBOARD_") || knownSettings[name] {
			continue
		}
		if why, gone := retiredSettings[name]; gone {
			retired = append(retired, fmt.Sprintf("%s (%s)", name, why))
			continue
		}
		unknown = append(unknown, name)
	}
	sort.Strings(unknown)
	sort.Strings(retired)
	return unknown, retired
}

// reportStraySettings names what the service is not reading.
//
// A warning, never a refusal: a stale line must not stop a service that would
// otherwise run correctly, and a config the operator cannot edit before the next
// restart would be a worse failure than the one this catches.
func reportStraySettings(env []string, out *os.File) {
	unknown, retired := straySettings(env)
	if len(retired) > 0 {
		fmt.Fprintf(out, "no longer used, safe to delete: %s\n", strings.Join(retired, ", "))
	}
	if len(unknown) > 0 {
		fmt.Fprintf(out, "not a setting, ignored (misspelled?): %s\n", strings.Join(unknown, ", "))
	}
}
