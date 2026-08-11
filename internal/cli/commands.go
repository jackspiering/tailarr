package cli

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"

	"github.com/jackspiering/tailarr/internal/authkeys"
	"github.com/jackspiering/tailarr/internal/config"
	"github.com/jackspiering/tailarr/internal/deploy"
	"github.com/jackspiering/tailarr/internal/doctor"
	"github.com/jackspiering/tailarr/internal/exitcode"
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
				fmt.Fprintln(rt.Out, "No valid ScaleTail services found.")
				return nil
			}
			for _, s := range svcs {
				fmt.Fprintln(rt.Out, s.Name)
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
				fmt.Fprintln(rt.Out, "No deployed services found.")
				return nil
			}
			for _, s := range svcs {
				tag := "other"
				if deploy.IsManaged(s.Dir) {
					tag = "managed"
				}
				fmt.Fprintf(rt.Out, "%s\t%s\n", s.Name, tag)
			}
			return nil
		},
	}
}

func newRunningCmd(rt *Runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "running",
		Short: "List running Compose project names (best effort)",
		RunE: func(cmd *cobra.Command, args []string) error {
			if !deploy.DockerOK() {
				return exitf(exitcode.Docker, "docker is not available")
			}
			// Best-effort: docker ps project label.
			c := exec.Command("docker", "ps", "--format", "{{.Label \"com.docker.compose.project\"}}")
			out, err := c.Output()
			if err != nil {
				return exitf(exitcode.Docker, "docker ps failed: %w", err)
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
				fmt.Fprintln(rt.Out, line)
			}
			return nil
		},
	}
}

func mgr(rt *Runtime) *deploy.Manager {
	return &deploy.Manager{Cfg: &rt.Cfg, Log: rt.Log}
}

func newDeployCmd(rt *Runtime) *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "deploy <service>",
		Short: "Deploy a ScaleTail service",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := maybeRefresh(rt); err != nil {
				return err
			}
			if err := mgr(rt).Deploy(args[0], force); err != nil {
				return mapDeployErr(err)
			}
			fmt.Fprintf(rt.Out, "Deployed %s\n", args[0])
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "replace an existing deployment")
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
			fmt.Fprintf(rt.Out, "Repaired %s\n", args[0])
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
			fmt.Fprintf(rt.Out, "Updated %s\n", args[0])
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
			fmt.Fprintf(rt.Out, "Stopped %s\n", args[0])
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
			fmt.Fprintf(rt.Out, "Restarted %s\n", args[0])
			return nil
		},
	}
}

func newRemoveCmd(rt *Runtime) *cobra.Command {
	var volumes bool
	cmd := &cobra.Command{
		Use:   "remove <service>",
		Short: "Remove a deployed service",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := mgr(rt).Remove(args[0], volumes); err != nil {
				return mapDeployErr(err)
			}
			fmt.Fprintf(rt.Out, "Removed %s\n", args[0])
			return nil
		},
	}
	cmd.Flags().BoolVar(&volumes, "volumes", false, "also remove Compose volumes")
	return cmd
}

func newLogsCmd(rt *Runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "logs",
		Short: "Print the Tailarr log file path",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintln(rt.Out, rt.Cfg.LogPath)
			return nil
		},
	}
}

func newConfigCmd(rt *Runtime) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Show or write configuration",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "show",
		Short: "Print effective configuration",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprint(rt.Out, rt.Cfg.String())
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
			fmt.Fprintf(rt.Out, "Wrote %s\n", rt.Cfg.ConfigPath)
			return nil
		},
	})
	// Default: show
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		fmt.Fprint(rt.Out, rt.Cfg.String())
		return nil
	}
	return cmd
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
				fmt.Fprintln(rt.Out, "No stored auth keys.")
				return nil
			}
			for _, line := range lines {
				fmt.Fprintln(rt.Out, line)
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
				return exitf(exitcode.Cancelled, "%w", err)
			}
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
			fmt.Fprintf(rt.Out, "Stored auth key %s\n", name)
			return nil
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "remove <name>",
		Short: "Remove a stored key",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
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
			fmt.Fprintf(rt.Out, "Removed auth key %s\n", args[0])
			return nil
		},
	})
	return cmd
}

func readSecret(label string) (string, error) {
	// Prefer full stdin when not a terminal (scripted file redirect).
	stat, _ := os.Stdin.Stat()
	if stat != nil && (stat.Mode()&os.ModeCharDevice) == 0 {
		data, err := os.ReadFile("/dev/stdin")
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(data)), nil
	}
	fmt.Fprintf(os.Stderr, "%s: ", label)
	var line string
	_, err := fmt.Fscanln(os.Stdin, &line)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

func maybeRefresh(rt *Runtime) error {
	lockPath := deploy.RepoLockPath(rt.Cfg.RepoPath)
	if !rt.Cfg.NoRefresh {
		lock, err := deploy.AcquireLock(lockPath, deploy.DefaultLockTimeout)
		if err != nil {
			// If lock parent not writable, still try refresh without lock in doctor-less path
			// Prefer fail safe.
			return exitf(exitcode.Perm, "repo lock: %w", err)
		}
		defer lock.Release()
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
	msg := err.Error()
	switch {
	case strings.Contains(msg, "invalid service name"), strings.Contains(msg, "service name is required"):
		return &ExitError{Code: exitcode.Usage, Err: err}
	case strings.Contains(msg, "not found"), strings.Contains(msg, "not deployed"), strings.Contains(msg, "not a git"):
		return &ExitError{Code: exitcode.NotFound, Err: err}
	case strings.Contains(msg, "symlink"), strings.Contains(msg, "escaped"), strings.Contains(msg, "Unsafe"):
		return &ExitError{Code: exitcode.Unsafe, Err: err}
	case strings.Contains(msg, "docker"):
		return &ExitError{Code: exitcode.Docker, Err: err}
	case strings.Contains(msg, "lock"), strings.Contains(msg, "permission"), strings.Contains(msg, "not writable"):
		return &ExitError{Code: exitcode.Perm, Err: err}
	default:
		return err
	}
}
