// Package cli wires Cobra commands for Tailarr.
package cli

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/jackspiering/tailarr/internal/config"
	"github.com/jackspiering/tailarr/internal/exitcode"
	"github.com/jackspiering/tailarr/internal/logging"
	"github.com/jackspiering/tailarr/internal/ui"
	"github.com/jackspiering/tailarr/internal/version"
)

// ExitError carries a process exit code.
type ExitError struct {
	Code int
	Err  error
}

func (e *ExitError) Error() string {
	if e.Err == nil {
		return fmt.Sprintf("exit %d", e.Code)
	}
	return e.Err.Error()
}

func (e *ExitError) Unwrap() error { return e.Err }

// exitf returns an ExitError with code and message.
func exitf(code int, format string, args ...any) error {
	return &ExitError{Code: code, Err: fmt.Errorf(format, args...)}
}

// Runtime holds shared state for commands.
type Runtime struct {
	Cfg config.Config
	Log *logging.Logger
	Out io.Writer
	Err io.Writer
}

// NewRoot builds the root command.
func NewRoot() *cobra.Command {
	rt := &Runtime{
		Cfg: config.Default(),
		Out: os.Stdout,
		Err: os.Stderr,
	}

	var (
		flagConfig    string
		flagRepoURL   string
		flagRepoPath  string
		flagDeploy    string
		flagLog       string
		flagAuth      string
		flagRef       string
		flagNoRefresh bool
		flagYes       bool
	)

	root := &cobra.Command{
		Use:     version.Name,
		Short:   "Deploy and manage ScaleTail Docker Compose services",
		Long:    "Tailarr deploys and manages ScaleTail Docker Compose services on a host you control.\nRun with no arguments for the interactive TUI, or use a subcommand for scripting.",
		Version: version.Version,
		// Prefer "tailarr version" output shape for --version as well.
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			cfg := config.Default()
			if flagConfig != "" {
				cfg.ConfigPath = flagConfig
			}
			if err := config.Load(&cfg); err != nil {
				return exitf(exitcode.Usage, "load config: %w", err)
			}
			if flagRepoURL != "" {
				cfg.RepoURL = flagRepoURL
			}
			if flagRepoPath != "" {
				cfg.RepoPath = flagRepoPath
			}
			if flagDeploy != "" {
				cfg.DeployPath = flagDeploy
			}
			if flagLog != "" {
				cfg.LogPath = flagLog
			}
			if flagAuth != "" {
				cfg.AuthkeysPath = flagAuth
			}
			if flagRef != "" {
				cfg.RepoRef = flagRef
			}
			if flagNoRefresh {
				cfg.NoRefresh = true
			}
			if flagYes {
				cfg.AssumeYes = true
			}
			rt.Cfg = cfg
			rt.Log = logging.New(rt.Cfg.LogPath, rt.Cfg.LogMaxBytes)
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDefault(rt)
		},
	}

	pf := root.PersistentFlags()
	pf.StringVar(&flagConfig, "config", "", "config file path")
	pf.StringVar(&flagRepoURL, "repo-url", "", "ScaleTail git URL")
	pf.StringVar(&flagRepoPath, "repo-path", "", "local ScaleTail clone path")
	pf.StringVar(&flagDeploy, "deploy-path", "", "deployment root")
	pf.StringVar(&flagLog, "log-path", "", "log file path")
	pf.StringVar(&flagAuth, "authkeys-path", "", "auth keys file path")
	pf.StringVar(&flagRef, "repo-ref", "", "pin ScaleTail to a branch, tag, or commit")
	pf.BoolVar(&flagNoRefresh, "no-refresh", false, "skip ScaleTail clone/pull for list/deploy/repair")
	pf.BoolVar(&flagYes, "yes", false, "auto-confirm default-yes prompts")
	root.SetVersionTemplate(fmt.Sprintf("%s {{.Version}}\n", version.Name))

	root.AddCommand(newVersionCmd(rt))
	root.AddCommand(newDoctorCmd(rt))
	root.AddCommand(newListCmd(rt))
	root.AddCommand(newDeployedCmd(rt))
	root.AddCommand(newRunningCmd(rt))
	root.AddCommand(newDeployCmd(rt))
	root.AddCommand(newRepairCmd(rt))
	root.AddCommand(newUpdateCmd(rt))
	root.AddCommand(newStopCmd(rt))
	root.AddCommand(newRestartCmd(rt))
	root.AddCommand(newRemoveCmd(rt))
	root.AddCommand(newLogsCmd(rt))
	root.AddCommand(newConfigCmd(rt))
	root.AddCommand(newAuthkeysCmd(rt))

	return root
}

// Execute runs the root command and maps errors to exit codes.
func Execute() int {
	root := NewRoot()
	if err := root.Execute(); err != nil {
		var ee *ExitError
		if errors.As(err, &ee) {
			if ee.Err != nil {
				fmt.Fprintln(os.Stderr, "Error:", ee.Err)
			}
			return ee.Code
		}
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 1
	}
	return 0
}

func runDefault(rt *Runtime) error {
	if ui.IsInteractive() {
		return ui.Run(rt.Cfg, rt.Log)
	}
	// Non-TTY: short help instead of TUI.
	_, _ = fmt.Fprintln(rt.Out, "Tailarr "+version.Version)
	_, _ = fmt.Fprintln(rt.Out, "Interactive TUI requires a TTY. Use a subcommand, for example:")
	_, _ = fmt.Fprintln(rt.Out, "  tailarr list")
	_, _ = fmt.Fprintln(rt.Out, "  tailarr doctor")
	_, _ = fmt.Fprintln(rt.Out, "  tailarr --help")
	return nil
}

func newVersionCmd(rt *Runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version",
		RunE: func(cmd *cobra.Command, args []string) error {
			_, _ = fmt.Fprintf(rt.Out, "%s %s\n", version.Name, version.Version)
			return nil
		},
	}
}
