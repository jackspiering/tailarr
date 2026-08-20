// Command tailarr is the Tailarr interactive TUI entrypoint.
package main

import (
	"fmt"
	"os"

	"github.com/jackspiering/tailarr/internal/config"
	"github.com/jackspiering/tailarr/internal/logging"
	"github.com/jackspiering/tailarr/internal/ui"
)

func main() {
	cfg := config.Default()
	if err := config.Load(&cfg); err != nil {
		fmt.Fprintln(os.Stderr, "Error: load config:", err)
		os.Exit(1)
	}

	if !ui.IsInteractive() {
		fmt.Fprintln(os.Stderr, "Tailarr is interactive; run inside a terminal.")
		os.Exit(1)
	}

	log := logging.New(cfg.LogPath, cfg.LogMaxBytes)
	if err := log.Validate(); err != nil {
		fmt.Fprintln(os.Stderr, "Warning: log path:", err)
	}

	if err := ui.FirstRunSetup(&cfg); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
	// FirstRunSetup may have changed LogPath via interactive edit; recreate logger if needed.
	if cfg.LogPath != log.Path() {
		log = logging.New(cfg.LogPath, cfg.LogMaxBytes)
	}
	if err := ui.Run(cfg, log); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}
