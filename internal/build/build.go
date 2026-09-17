// Package build carries what this binary is, so a running service can say which
// one it is without anybody guessing from a file date.
package build

import "fmt"

// Set at build time with -ldflags; see the justfile.
var (
	Version = "dev"
	Commit  = "unknown"
	Date    = "unknown"
)

// Line is the one-line identity, for `switchboard version` and logs.
func Line() string {
	return fmt.Sprintf("switchboard %s (%s, built %s)", Version, Commit, Date)
}
