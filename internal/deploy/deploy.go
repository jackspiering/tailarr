// Package deploy implements service lifecycle against Docker Compose.
package deploy

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/jackspiering/tailarr/internal/authkeys"
	"github.com/jackspiering/tailarr/internal/config"
	"github.com/jackspiering/tailarr/internal/logging"
	"github.com/jackspiering/tailarr/internal/scaletail"
	"github.com/jackspiering/tailarr/internal/security/atomic"
	"github.com/jackspiering/tailarr/internal/security/names"
	"github.com/jackspiering/tailarr/internal/security/paths"
)

// Manager coordinates deploy/update/stop/restart/remove/repair.
type Manager struct {
	Cfg *config.Config
	Log *logging.Logger
}

// DeployOpts controls optional deploy behavior.
type DeployOpts struct {
	// Force replaces an existing managed deployment (with backup).
	Force bool
	// AuthKeyName selects a named key from the auth key store when TS_AUTHKEY is empty.
	// The secret itself is never accepted here or via flags.
	AuthKeyName string
}

// TailarrComposeLabel is applied via override so status can detect managed stacks.
const TailarrComposeLabel = "com.tailarr.managed=true"

// overrideFilename is written next to the service compose file.
const overrideFilename = ".tailarr.compose.yaml"

// Deploy copies a template into the deploy path, merges env, and runs compose up.
func (m *Manager) Deploy(service string, force bool) error {
	return m.DeployWith(service, DeployOpts{Force: force})
}

// DeployWith is Deploy with extended options.
func (m *Manager) DeployWith(service string, opts DeployOpts) error {
	if err := names.ValidateServiceName(service); err != nil {
		return err
	}
	lockPath, err := ServiceLockPath(m.Cfg.DeployPath, service)
	if err != nil {
		return err
	}
	lock, err := AcquireLock(lockPath, DefaultLockTimeout)
	if err != nil {
		return err
	}
	defer func() { _ = lock.Release() }()

	if err := paths.EnsureDir(m.Cfg.DeployPath, "deployment directory"); err != nil {
		return err
	}
	if err := paths.RefuseSymlinkAncestry(m.Cfg.DeployPath); err != nil {
		return fmt.Errorf("deployment root: %w", err)
	}

	templateDir := filepath.Join(m.Cfg.RepoPath, "services", service)
	if paths.IsSymlink(templateDir) {
		return fmt.Errorf("%w: template must not be a symlink: %s", ErrSymlink, templateDir)
	}
	if found, err := paths.ContainsSymlinks(templateDir); err != nil {
		return fmt.Errorf("template: %w", err)
	} else if found != "" {
		return fmt.Errorf("%w: template contains unsupported symlink: %s", ErrSymlink, found)
	}
	if !scaletail.HasComposeFile(templateDir) {
		return fmt.Errorf("template has no compose file: %s", service)
	}

	dest, err := paths.JoinUnder(m.Cfg.DeployPath, service)
	if err != nil {
		return err
	}

	var backupPath string
	if st, err := os.Lstat(dest); err == nil {
		if !opts.Force {
			return fmt.Errorf("%w: %s (use --force to replace)", ErrAlreadyDeployed, service)
		}
		if st.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: refusing to operate on symlink deployment: %s", ErrSymlink, service)
		}
		if found, err := paths.ContainsSymlinks(dest); err == nil && found != "" {
			return fmt.Errorf("%w: deployment contains unsupported symlink: %s", ErrSymlink, found)
		}
		// Only force-replace Tailarr-managed deployments (or those with compose + we take over carefully).
		if !IsManaged(dest) {
			return fmt.Errorf("%w: refusing --force on unmanaged path %s (no Tailarr marker)", ErrNotManaged, service)
		}
		// Best-effort compose down before replace; failure is non-fatal if we still backup,
		// but we surface it in logs. Containers may linger until backup path is cleaned.
		proj := composeProjectArgs(m.Cfg.DeployPath, service)
		downArgs := append(append([]string{}, proj...), "down", "--remove-orphans")
		if err := Compose(dest, downArgs...); err != nil {
			m.log("warning: compose down before force replace of %s: %v", service, err)
		}
		backupPath, err = Backup(m.Cfg.DeployPath, service, dest, BackupMove)
		if err != nil {
			return err
		}
		m.log("backup created for %s: %s", service, backupPath)
	}

	// From here, if we moved the old deployment, failures must try to restore it.
	if err := m.finishDeploy(service, templateDir, dest, backupPath, opts); err != nil {
		if backupPath != "" {
			if rerr := restoreDeploymentFromBackup(backupPath, dest); rerr != nil {
				return fmt.Errorf("deploy failed (%v); also failed to restore previous deployment from %s: %w", err, backupPath, rerr)
			}
			m.log("restored previous deployment for %s after failed force replace", service)
			return fmt.Errorf("deploy failed; previous deployment restored: %w", err)
		}
		// Partial fresh deploy: clean up dest if we created it.
		_ = safeRemoveTree(dest, m.Cfg.DeployPath)
		return err
	}
	m.log("deployed service %s", service)
	return nil
}

func (m *Manager) finishDeploy(service, templateDir, dest, backupPath string, opts DeployOpts) error {
	if err := copyTemplate(templateDir, dest); err != nil {
		return err
	}
	if backupPath != "" {
		if err := RestorePersistentData(backupPath, dest); err != nil {
			return fmt.Errorf("restore persistent data: %w", err)
		}
	}

	if err := m.mergeAndWriteEnv(service, templateDir, dest, backupPath, opts); err != nil {
		return err
	}
	if err := writeOverride(dest); err != nil {
		return err
	}

	proj := composeProjectArgs(m.Cfg.DeployPath, service)
	upArgs := append(append([]string{}, proj...),
		"-f", composeBaseName(dest), "-f", overrideFilename, "up", "-d", "--remove-orphans")
	if err := Compose(dest, upArgs...); err != nil {
		return err
	}
	return nil
}

func composeBaseName(dir string) string {
	if p, ok := scaletail.ComposeFileIn(dir); ok {
		return filepath.Base(p)
	}
	return "compose.yaml"
}

func (m *Manager) mergeAndWriteEnv(service, templateDir, dest, backupPath string, opts DeployOpts) error {
	tplEnv := filepath.Join(templateDir, ".env")
	localEnv := filepath.Join(dest, ".env")
	templateMap, err := scaletail.ParseEnvFile(tplEnv)
	if err != nil {
		return err
	}
	keys, err := scaletail.ReadEnvKeys(tplEnv)
	if err != nil {
		return err
	}
	// Start with dest .env (template copy), then layer backup secrets, then store key.
	localMap, err := scaletail.ParseEnvFile(localEnv)
	if err != nil {
		return err
	}
	if backupPath != "" {
		backupEnv := filepath.Join(backupPath, ".env")
		backupMap, err := scaletail.ParseEnvFile(backupEnv)
		if err != nil {
			return fmt.Errorf("read backup .env: %w", err)
		}
		// Non-empty backup values win over empty template values.
		for k, v := range backupMap {
			if strings.TrimSpace(v) != "" {
				localMap[k] = v
			}
		}
	} else {
		// Also try latest historical backup if local TS_AUTHKEY is empty.
		if strings.TrimSpace(localMap["TS_AUTHKEY"]) == "" {
			if latest, err := LatestBackup(m.Cfg.DeployPath, service); err == nil && latest != "" {
				if bm, err := scaletail.ParseEnvFile(filepath.Join(latest, ".env")); err == nil {
					if v := strings.TrimSpace(bm["TS_AUTHKEY"]); v != "" {
						localMap["TS_AUTHKEY"] = v
					}
				}
			}
		}
	}

	merged := scaletail.MergeEnv(templateMap, localMap, keys)

	// Resolve empty TS_AUTHKEY from named store entry when requested or only one key.
	if strings.TrimSpace(merged["TS_AUTHKEY"]) == "" {
		if err := m.applyAuthKeyFromStore(merged, opts.AuthKeyName); err != nil {
			return err
		}
	}

	if err := scaletail.ValidateMergedTSAuthkey(merged); err != nil {
		return err
	}
	// Fail closed when template declares TS_AUTHKEY but it is still empty.
	if _, declared := templateMap["TS_AUTHKEY"]; declared {
		if strings.TrimSpace(merged["TS_AUTHKEY"]) == "" {
			return ErrEmptyAuthkey
		}
	}
	return scaletail.WriteEnvFile(localEnv, merged, keys)
}

func (m *Manager) applyAuthKeyFromStore(merged scaletail.EnvMap, authKeyName string) error {
	if m.Cfg.AuthkeysPath == "" {
		return nil
	}
	store, err := authkeys.Load(m.Cfg.AuthkeysPath)
	if err != nil {
		return err
	}
	if authKeyName != "" {
		val, ok := store.Keys[authKeyName]
		if !ok {
			return fmt.Errorf("auth key %q not found in store", authKeyName)
		}
		merged["TS_AUTHKEY"] = val
		return nil
	}
	// Non-interactive default: do not auto-pick when multiple keys exist.
	return nil
}

func writeOverride(dest string) error {
	body := `# Generated by Tailarr - do not edit by hand
# Managed: ` + TailarrComposeLabel + `
`
	return atomic.WriteFileString(filepath.Join(dest, overrideFilename), body, 0o644)
}

func copyTemplate(src, dst string) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		target := filepath.Join(dst, rel)
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: refusing to copy symlink: %s", ErrSymlink, path)
		}
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		return copyFileMode(path, target, info.Mode().Perm())
	})
}

func copyFileMode(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()
	_, err = io.Copy(out, in)
	return err
}

// Repair refreshes template files while preserving local .env secrets.
func (m *Manager) Repair(service string) error {
	if err := names.ValidateServiceName(service); err != nil {
		return err
	}
	lockPath, err := ServiceLockPath(m.Cfg.DeployPath, service)
	if err != nil {
		return err
	}
	lock, err := AcquireLock(lockPath, DefaultLockTimeout)
	if err != nil {
		return err
	}
	defer func() { _ = lock.Release() }()

	dest, err := paths.JoinUnder(m.Cfg.DeployPath, service)
	if err != nil {
		return err
	}
	if err := requireManagedDeploy(dest, service); err != nil {
		return err
	}

	if _, err := Backup(m.Cfg.DeployPath, service, dest, BackupCopy); err != nil {
		return err
	}

	// Preserve .env
	envPath := filepath.Join(dest, ".env")
	var envBackup []byte
	if data, err := os.ReadFile(envPath); err == nil {
		envBackup = data
	}

	templateDir := filepath.Join(m.Cfg.RepoPath, "services", service)
	for _, name := range scaletail.ComposeCandidates {
		src := filepath.Join(templateDir, name)
		if st, err := os.Stat(src); err == nil && !st.IsDir() && !paths.IsSymlink(src) {
			if err := copyFileMode(src, filepath.Join(dest, name), 0o644); err != nil {
				return err
			}
		}
	}
	if len(envBackup) > 0 {
		if err := atomic.WriteFile(envPath, envBackup, 0o600); err != nil {
			return err
		}
	}
	if err := writeOverride(dest); err != nil {
		return err
	}
	proj := composeProjectArgs(m.Cfg.DeployPath, service)
	upArgs := append(append([]string{}, proj...),
		"-f", composeBaseName(dest), "-f", overrideFilename, "up", "-d", "--remove-orphans")
	if err := Compose(dest, upArgs...); err != nil {
		return err
	}
	m.log("repaired service %s", service)
	return nil
}

// Update pulls images and recreates containers.
func (m *Manager) Update(service string) error {
	return m.withManagedServiceDir(service, func(dir string) error {
		proj := composeProjectArgs(m.Cfg.DeployPath, service)
		pullArgs := append(append([]string{}, proj...), "-f", composeBaseName(dir), "pull")
		if err := Compose(dir, pullArgs...); err != nil {
			return err
		}
		upArgs := append(append([]string{}, proj...),
			"-f", composeBaseName(dir), "-f", overrideFilename, "up", "-d", "--remove-orphans")
		if err := Compose(dir, upArgs...); err != nil {
			return err
		}
		m.log("updated service %s", service)
		return nil
	})
}

// Stop stops a deployment.
func (m *Manager) Stop(service string) error {
	return m.withManagedServiceDir(service, func(dir string) error {
		proj := composeProjectArgs(m.Cfg.DeployPath, service)
		args := append(append([]string{}, proj...), "stop")
		if err := Compose(dir, args...); err != nil {
			return err
		}
		m.log("stopped service %s", service)
		return nil
	})
}

// Restart restarts a deployment.
func (m *Manager) Restart(service string) error {
	return m.withManagedServiceDir(service, func(dir string) error {
		proj := composeProjectArgs(m.Cfg.DeployPath, service)
		args := append(append([]string{}, proj...), "restart")
		if err := Compose(dir, args...); err != nil {
			return err
		}
		m.log("restarted service %s", service)
		return nil
	})
}

// Remove tears down a deployment. When volumes is true, passes -v.
// Fails closed: directory is only deleted after compose down succeeds.
func (m *Manager) Remove(service string, volumes bool) error {
	if err := names.ValidateServiceName(service); err != nil {
		return err
	}
	lockPath, err := ServiceLockPath(m.Cfg.DeployPath, service)
	if err != nil {
		return err
	}
	lock, err := AcquireLock(lockPath, DefaultLockTimeout)
	if err != nil {
		return err
	}
	defer func() { _ = lock.Release() }()

	dest, err := paths.JoinUnder(m.Cfg.DeployPath, service)
	if err != nil {
		return err
	}
	if err := requireManagedDeploy(dest, service); err != nil {
		return err
	}

	if _, err := Backup(m.Cfg.DeployPath, service, dest, BackupCopy); err != nil {
		return err
	}
	proj := composeProjectArgs(m.Cfg.DeployPath, service)
	args := append(append([]string{}, proj...), "down", "--remove-orphans")
	if volumes {
		args = append(args, "--volumes")
	}
	if err := Compose(dest, args...); err != nil {
		return fmt.Errorf("compose down failed; deployment directory left intact: %w", err)
	}
	if err := safeRemoveTree(dest, m.Cfg.DeployPath); err != nil {
		return err
	}
	m.log("removed service %s", service)
	return nil
}

func (m *Manager) withManagedServiceDir(service string, fn func(dir string) error) error {
	if err := names.ValidateServiceName(service); err != nil {
		return err
	}
	lockPath, err := ServiceLockPath(m.Cfg.DeployPath, service)
	if err != nil {
		return err
	}
	lock, err := AcquireLock(lockPath, DefaultLockTimeout)
	if err != nil {
		return err
	}
	defer func() { _ = lock.Release() }()

	dest, err := paths.JoinUnder(m.Cfg.DeployPath, service)
	if err != nil {
		return err
	}
	if err := requireManagedDeploy(dest, service); err != nil {
		return err
	}
	return fn(dest)
}

// requireManagedDeploy ensures dest exists, has a compose file, and Tailarr marker.
func requireManagedDeploy(dest, service string) error {
	if _, err := os.Stat(dest); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%w: %s", ErrNotDeployed, service)
		}
		return err
	}
	if found, err := paths.ContainsSymlinks(dest); err != nil {
		return err
	} else if found != "" {
		return fmt.Errorf("%w: deployment contains unsupported symlink: %s", ErrSymlink, found)
	}
	if !scaletail.HasComposeFile(dest) {
		return fmt.Errorf("%w: %s", ErrNoCompose, service)
	}
	if !IsManaged(dest) {
		return fmt.Errorf("%w: %s (missing %s marker)", ErrNotManaged, service, overrideFilename)
	}
	return nil
}

func safeRemoveTree(path, root string) error {
	if paths.IsSymlink(path) {
		return fmt.Errorf("%w: refusing to remove symlink: %s", ErrSymlink, path)
	}
	ok, err := paths.Within(path, root)
	if err != nil || !ok {
		rootAbs, err2 := paths.AbsExistingDir(root)
		if err2 != nil {
			return fmt.Errorf("unsafe remove path: %s", path)
		}
		pathAbs, err2 := filepath.Abs(path)
		if err2 != nil {
			return err2
		}
		if !strings.HasPrefix(pathAbs, rootAbs+string(os.PathSeparator)) {
			return fmt.Errorf("path not within deploy root: %s", path)
		}
	}
	if found, err := paths.ContainsSymlinks(path); err != nil {
		return err
	} else if found != "" {
		return fmt.Errorf("%w: refusing to remove tree with symlink: %s", ErrSymlink, found)
	}
	return os.RemoveAll(path)
}

func (m *Manager) log(format string, args ...any) {
	if m.Log != nil {
		m.Log.Event(fmt.Sprintf(format, args...))
	}
}

// IsManaged reports whether a deploy dir has a Tailarr override marker.
func IsManaged(dir string) bool {
	p := filepath.Join(dir, overrideFilename)
	data, err := os.ReadFile(p)
	if err != nil {
		return false
	}
	return strings.Contains(string(data), "com.tailarr.managed")
}
