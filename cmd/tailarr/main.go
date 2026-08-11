// Command tailarr is the Tailarr CLI and TUI entrypoint.
package main

import (
	"os"

	"github.com/jackspiering/tailarr/internal/cli"
)

func main() {
	os.Exit(cli.Execute())
}
