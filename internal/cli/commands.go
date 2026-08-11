package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/jackspiering/tailarr/internal/authkeys"
	"github.com/jackspiering/tailarr/internal/config"
	"github.com/jackspiering/tailarr/internal/deploy"
	"github.com/jackspiering/tailarr/internal/doctor"
	"github.com/jackspiering/tailarr/internal/exitcode"
	"github.com/jackspiering/tailarr/internal/prompt"
	"github.com/jackspiering/tailarr/internal/scaletail"
	"github.com/jackspiering/tailarr/internal/security/names"
)

func newDoctorCmd(rt *Runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check host readiness (commands, paths, Docker)",
		RunE: func(cmd *cobra.Command, args []string) error {
			res := doctor.Run(rt.Cfg)
			doctor.Write(rt.Out, res)
			if !res.Healthy() {
				return &ExitError{Code: exitcode.Health}
			}
			return nil
		},
	}
}

func newListCmd(rt *Runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List available ScaleTail services",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := maybeRefresh(rt); err != nil {
				return err
			}
			svcs, err := scaletail.ListAvailable(rt.Cfg.RepoPath)
			if err != nil {
				return exitf(exitcode.NotFound, "%w", err)
			}
			if len(svcs) == 0 {
				_, _ = fmt.Fprintln(rt.Out, "No valid ScaleTail services found.")
				return nil
			}
			for _, s := range svcs {
				_, _ = fmt.Fprintln(rt.Out, s.Name)
			}
			return nil
		},
	}
}

func newDeployedCmd(rt *Runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "deployed",
		Short: "List local Tailarr deployments",
		RunE: func(cmd *cobra.Command, args []string) error {
			svcs, err := scaletail.ListDeployed(rt.Cfg.DeployPath)
			if err != nil {
				return exitf(exitcode.NotFound, "%w", err)
			}
			if len(svcs) == 0 {
				_, _ = fmt.Fprintln(rt.Out, "No deployed services found.")
				return nil
			}
			for _, s := range svcs {
				tag := "other"
				if deploy.IsManaged(s.Dir) {
					tag = "managed"
				}
				_, _ = fmt.Fprintf(rt.Out, "%s\t%s\n", s.Name, tag)
			}
			return nil
		},
	}
}

func newRunningCmd(rt *Runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "running",
		Short: "List running ScaleTail-style service names (best effort)",
		RunE: func(cmd *cobra.Command, args []string) error {
			if !deploy.DockerOK() {
				return exitf(exitcode.Docker, "docker is not available")
			}
			namesList, err := deploy.RunningServiceNames()
			if err != nil {
				// Fall back to compose project labels.
				c := exec.Command("docker", "ps", "--format", "{{.Label \"com.docker.compose.project\"}}")
				out, err2 := c.Output()
				if err2 != nil {
					return exitf(exitcode.Docker, "docker ps failed: %w", err2)
				}
				seen := map[string]bool{}
				for _, line := range strings.Split(string(out), "\n") {
					line = strings.TrimSpace(line)
					if line == "" || seen[line] {
						continue
					}
					if !names.ValidServiceName(line) {
						continue
					}
					seen[line] = true
					_, _ = fmt.Fprintln(rt.Out, line)
				}
				return nil
			}
			for _, n := range namesList {
				_, _ = fmt.Fprintln(rt.Out, n)
			}
			return nil
		},
	}
}

func mgr(rt *Runtime) *deploy.Manager {
	return &deploy.Manager{
		Cfg: &rt.Cfg,
		Log: rt.Log,
		UI:  prompt.NewStd(rt.Cfg.AssumeYes),
	}
}

func newDeployCmd(rt *Runtime) *cobra.Command {
	var force bool
	var authKeyName string
	cmd := &cobra.Command{
		Use:   "deploy <service>",
		Short: "Deploy a ScaleTail service",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := maybeRefresh(rt); err != nil {
				return err
			}
			opts := deploy.DeployOpts{Force: force, AuthKeyName: authKeyName, SkipConfirm: force}
			// Non-TTY without --yes: skip interactive env prompts (still fail closed on empty TS_AUTHKEY).
			if !isStdinTTY() && !rt.Cfg.AssumeYes {
				opts.SkipInteractive = true
			}
			if err := mgr(rt).DeployWith(args[0], opts); err != nil {
				return mapDeployErr(err)
			}
			_, _ = fmt.Fprintf(rt.Out, "Deployed %s\n", args[0])
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "replace an existing managed deployment")
	// Named key only - never the secret value.
	cmd.Flags().StringVar(&authKeyName, "authkey", "", "name of stored auth key to use when TS_AUTHKEY is empty")
	return cmd
}

func newRepairCmd(rt *Runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "repair <service>",
		Short: "Refresh templates and keep local secrets when possible",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := maybeRefresh(rt); err != nil {
				return err
			}
			if err := mgr(rt).Repair(args[0]); err != nil {
				return mapDeployErr(err)
			}
			_, _ = fmt.Fprintf(rt.Out, "Repaired %s\n", args[0])
			return nil
		},
	}
}

func newUpdateCmd(rt *Runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "update <service>",
		Short: "Pull images and recreate containers",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := mgr(rt).Update(args[0]); err != nil {
				return mapDeployErr(err)
			}
			_, _ = fmt.Fprintf(rt.Out, "Updated %s\n", args[0])
			return nil
		},
	}
}

func newStopCmd(rt *Runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "stop <service>",
		Short: "Stop a deployed service",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := mgr(rt).Stop(args[0]); err != nil {
				return mapDeployErr(err)
			}
			_, _ = fmt.Fprintf(rt.Out, "Stopped %s\n", args[0])
			return nil
		},
	}
}

func newRestartCmd(rt *Runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "restart <service>",
		Short: "Restart a deployed service",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := mgr(rt).Restart(args[0]); err != nil {
				return mapDeployErr(err)
			}
			_, _ = fmt.Fprintf(rt.Out, "Restarted %s\n", args[0])
			return nil
		},
	}
}

func newRemoveCmd(rt *Runtime) *cobra.Command {
	var volumes bool
	cmd := &cobra.Command{
		Use:   "remove <service>",
		Short: "Remove a managed deployment (fails closed if compose down fails)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !isStdinTTY() && !rt.Cfg.AssumeYes {
				return exitf(exitcode.Usage, "stdin is not a terminal; pass --yes to confirm remove")
			}
			opts := deploy.DeployOpts{Volumes: volumes, SkipConfirm: rt.Cfg.AssumeYes}
			if err := mgr(rt).RemoveWith(args[0], opts); err != nil {
				return mapDeployErr(err)
			}
			_, _ = fmt.Fprintf(rt.Out, "Removed %s\n", args[0])
			return nil
		},
	}
	cmd.Flags().BoolVar(&volumes, "volumes", false, "also remove Compose volumes")
	return cmd
}

func isStdinTTY() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

func newLogsCmd(rt *Runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "logs",
		Short: "Print the Tailarr log file path",
		RunE: func(cmd *cobra.Command, args []string) error {
			_, _ = fmt.Fprintln(rt.Out, rt.Cfg.LogPath)
			return nil
		},
	}
}

func newConfigCmd(rt *Runtime) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Show or edit configuration",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "show",
		Short: "Print effective configuration",
		RunE: func(cmd *cobra.Command, args []string) error {
			_, _ = fmt.Fprint(rt.Out, rt.Cfg.String())
			return nil
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "write",
		Short: "Write current effective configuration to the config file",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := config.Save(rt.Cfg); err != nil {
				return exitf(exitcode.Perm, "%w", err)
			}
			_, _ = fmt.Fprintf(rt.Out, "Wrote %s\n", rt.Cfg.ConfigPath)
			return nil
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "edit",
		Short: "Interactively edit and save configuration",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := editConfigInteractive(rt); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(rt.Out, "Saved %s\n", rt.Cfg.ConfigPath)
			return nil
		},
	})
	// Default: interactive edit on TTY, otherwise show.
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if isStdinTTY() {
			if err := editConfigInteractive(rt); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(rt.Out, "Saved %s\n", rt.Cfg.ConfigPath)
			return nil
		}
		_, _ = fmt.Fprint(rt.Out, rt.Cfg.String())
		return nil
	}
	return cmd
}

func editConfigInteractive(rt *Runtime) error {
	ui := prompt.NewStd(rt.Cfg.AssumeYes)
	var err error
	if rt.Cfg.RepoURL, err = ui.Line("TAILARR_REPO_URL", rt.Cfg.RepoURL); err != nil {
		return err
	}
	if rt.Cfg.RepoPath, err = ui.Line("TAILARR_REPO_PATH", rt.Cfg.RepoPath); err != nil {
		return err
	}
	if rt.Cfg.DeployPath, err = ui.Line("TAILARR_DEPLOY_PATH", rt.Cfg.DeployPath); err != nil {
		return err
	}
	if rt.Cfg.LogPath, err = ui.Line("TAILARR_LOG_PATH", rt.Cfg.LogPath); err != nil {
		return err
	}
	if rt.Cfg.AuthkeysPath, err = ui.Line("TAILARR_AUTHKEYS_PATH", rt.Cfg.AuthkeysPath); err != nil {
		return err
	}
	if rt.Cfg.RepoRef, err = ui.Line("TAILARR_REPO_REF", rt.Cfg.RepoRef); err != nil {
		return err
	}
	return config.Save(rt.Cfg)
}

func newAuthkeysCmd(rt *Runtime) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "authkeys",
		Short: "Manage stored Tailscale auth keys (secrets never on flags)",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List stored key names (values redacted)",
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := authkeys.Load(rt.Cfg.AuthkeysPath)
			if err != nil {
				return exitf(exitcode.Perm, "%w", err)
			}
			lines := s.RedactedList()
			if len(lines) == 0 {
				_, _ = fmt.Fprintln(rt.Out, "No stored auth keys.")
				return nil
			}
			for _, line := range lines {
				_, _ = fmt.Fprintln(rt.Out, line)
			}
			return nil
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "add <name>",
		Short: "Add or replace a key (reads TS_AUTHKEY from stdin or prompt)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if err := names.ValidateAuthkeyName(name); err != nil {
				return exitf(exitcode.Usage, "%w", err)
			}
			value, err := readSecret("TS_AUTHKEY")
			if err != nil {
				return exitf(exitcode.Canceled, "%w", err)
			}
			// Serialize read-modify-write with a lock next to the store.
			lockPath := rt.Cfg.AuthkeysPath + ".lock"
			lock, err := deploy.AcquireLock(lockPath, deploy.DefaultLockTimeout)
			if err != nil {
				return exitf(exitcode.Perm, "authkeys lock: %w", err)
			}
			defer func() { _ = lock.Release() }()

			s, err := authkeys.Load(rt.Cfg.AuthkeysPath)
			if err != nil {
				return exitf(exitcode.Perm, "%w", err)
			}
			if err := s.Put(name, value); err != nil {
				return exitf(exitcode.Usage, "%w", err)
			}
			if err := s.Save(); err != nil {
				return exitf(exitcode.Perm, "%w", err)
			}
			if rt.Log != nil {
				rt.Log.Event("stored auth key updated: " + name + "=TS_AUTHKEY=[redacted]")
			}
			_, _ = fmt.Fprintf(rt.Out, "Stored auth key %s\n", name)
			return nil
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "remove <name>",
		Short: "Remove a stored key",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			lockPath := rt.Cfg.AuthkeysPath + ".lock"
			lock, err := deploy.AcquireLock(lockPath, deploy.DefaultLockTimeout)
			if err != nil {
				return exitf(exitcode.Perm, "authkeys lock: %w", err)
			}
			defer func() { _ = lock.Release() }()

			s, err := authkeys.Load(rt.Cfg.AuthkeysPath)
			if err != nil {
				return exitf(exitcode.Perm, "%w", err)
			}
			if err := s.Remove(args[0]); err != nil {
				return exitf(exitcode.NotFound, "%w", err)
			}
			if err := s.Save(); err != nil {
				return exitf(exitcode.Perm, "%w", err)
			}
			_, _ = fmt.Fprintf(rt.Out, "Removed auth key %s\n", args[0])
			return nil
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "rename <old> <new>",
		Short: "Rename a stored key",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			lockPath := rt.Cfg.AuthkeysPath + ".lock"
			lock, err := deploy.AcquireLock(lockPath, deploy.DefaultLockTimeout)
			if err != nil {
				return exitf(exitcode.Perm, "authkeys lock: %w", err)
			}
			defer func() { _ = lock.Release() }()
			s, err := authkeys.Load(rt.Cfg.AuthkeysPath)
			if err != nil {
				return exitf(exitcode.Perm, "%w", err)
			}
			if err := s.Rename(args[0], args[1]); err != nil {
				return exitf(exitcode.Usage, "%w", err)
			}
			if err := s.Save(); err != nil {
				return exitf(exitcode.Perm, "%w", err)
			}
			_, _ = fmt.Fprintf(rt.Out, "Renamed auth key %s -> %s\n", args[0], args[1])
			return nil
		},
	})
	return cmd
}

// maxSecretBytes caps non-interactive secret reads to prevent huge pipes.
const maxSecretBytes = 4096

func readSecret(label string) (string, error) {
	stat, err := os.Stdin.Stat()
	if err == nil && (stat.Mode()&os.ModeCharDevice) == 0 {
		// Non-interactive: single line, bounded size.
		r := io.LimitReader(os.Stdin, maxSecretBytes+1)
		data, err := io.ReadAll(r)
		if err != nil {
			return "", err
		}
		if len(data) > maxSecretBytes {
			return "", fmt.Errorf("auth key input exceeds %d bytes", maxSecretBytes)
		}
		// First line only; reject if more content after first newline (injection).
		line, rest, found := strings.Cut(string(data), "\n")
		line = strings.TrimRight(line, "\r")
		line = strings.TrimSpace(line)
		if found {
			if strings.TrimSpace(rest) != "" {
				return "", fmt.Errorf("auth key must be a single line")
			}
		}
		if line == "" {
			return "", fmt.Errorf("empty auth key")
		}
		if strings.ContainsAny(line, "\r\n") {
			return "", fmt.Errorf("auth key must be a single line")
		}
		return line, nil
	}

	// Interactive: disable echo.
	_, _ = fmt.Fprintf(os.Stderr, "%s: ", label)
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		// Fallback without echo control: still only one line, bounded.
		sc := bufio.NewScanner(io.LimitReader(os.Stdin, maxSecretBytes))
		if !sc.Scan() {
			if err := sc.Err(); err != nil {
				return "", err
			}
			return "", fmt.Errorf("empty auth key")
		}
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			return "", fmt.Errorf("empty auth key")
		}
		return line, nil
	}
	raw, err := term.ReadPassword(fd)
	_, _ = fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", err
	}
	line := strings.TrimSpace(string(raw))
	if line == "" {
		return "", fmt.Errorf("empty auth key")
	}
	if strings.ContainsAny(line, "\r\n") {
		return "", fmt.Errorf("auth key must be a single line")
	}
	return line, nil
}

func maybeRefresh(rt *Runtime) error {
	lockPath := deploy.RepoLockPath(rt.Cfg.RepoPath)
	if !rt.Cfg.NoRefresh {
		lock, err := deploy.AcquireLock(lockPath, deploy.DefaultLockTimeout)
		if err != nil {
			return exitf(exitcode.Perm, "repo lock: %w", err)
		}
		defer func() { _ = lock.Release() }()
	}
	if err := scaletail.Refresh(rt.Cfg.RepoURL, rt.Cfg.RepoPath, rt.Cfg.RepoRef, rt.Cfg.NoRefresh); err != nil {
		return exitf(exitcode.NotFound, "%w", err)
	}
	return nil
}

func mapDeployErr(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, prompt.Canceled):
		return &ExitError{Code: exitcode.Canceled, Err: err}
	case errors.Is(err, deploy.ErrAlreadyDeployed):
		return &ExitError{Code: exitcode.Usage, Err: err}
	case errors.Is(err, deploy.ErrNotDeployed), errors.Is(err, deploy.ErrNoCompose):
		return &ExitError{Code: exitcode.NotFound, Err: err}
	case errors.Is(err, deploy.ErrNotManaged):
		return &ExitError{Code: exitcode.Unsafe, Err: err}
	case errors.Is(err, deploy.ErrSymlink):
		return &ExitError{Code: exitcode.Unsafe, Err: err}
	case errors.Is(err, deploy.ErrComposeFailed):
		return &ExitError{Code: exitcode.Docker, Err: err}
	case errors.Is(err, deploy.ErrEmptyAuthkey):
		return &ExitError{Code: exitcode.Usage, Err: err}
	}

	msg := err.Error()
	switch {
	case strings.Contains(msg, "invalid service name"), strings.Contains(msg, "service name is required"):
		return &ExitError{Code: exitcode.Usage, Err: err}
	case strings.Contains(msg, "not found"), strings.Contains(msg, "not deployed"), strings.Contains(msg, "not a git"):
		return &ExitError{Code: exitcode.NotFound, Err: err}
	case strings.Contains(msg, "symlink"), strings.Contains(msg, "escaped"), strings.Contains(msg, "Unsafe"), strings.Contains(msg, "unmanaged"):
		return &ExitError{Code: exitcode.Unsafe, Err: err}
	case strings.Contains(msg, "docker"):
		return &ExitError{Code: exitcode.Docker, Err: err}
	case strings.Contains(msg, "lock"), strings.Contains(msg, "permission"), strings.Contains(msg, "not writable"):
		return &ExitError{Code: exitcode.Perm, Err: err}
	default:
		return err
	}
}
