// Command switchboard runs the coordination service and the client that talks
// to it. See README.md.
package main

import (
	"os"

	"switchboard/internal/cli"
)

func main() { os.Exit(cli.Run(os.Args[1:])) }
