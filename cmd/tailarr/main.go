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
	log, err := reopenLogger(log, cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Warning: log path:", err)
	}
	if err := ui.Run(cfg, log); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}

// reopenLogger rebuilds the logger when first-run changes the log path and
// validates the replacement. Event stays silent on path errors, so a missing
// Validate here would hide a symlink parent chosen during first-run.
func reopenLogger(log *logging.Logger, cfg config.Config) (*logging.Logger, error) {
	if log != nil && cfg.LogPath == log.Path() {
		return log, nil
	}
	next := logging.New(cfg.LogPath, cfg.LogMaxBytes)
	return next, next.Validate()
}
