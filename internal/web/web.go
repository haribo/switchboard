// Package web carries the single page the PO keeps open.
package web

import (
	"embed"
	"io/fs"
)

//go:embed index.html
var files embed.FS

// Page is the file system served at /.
func Page() fs.FS { return files }
