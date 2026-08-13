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
	"github.com/jackspiering/tailarr/internal/prompt"
	"github.com/jackspiering/tailarr/internal/scaletail"
	"github.com/jackspiering/tailarr/internal/security/atomic"
	"github.com/jackspiering/tailarr/internal/security/names"
	"github.com/jackspiering/tailarr/internal/security/paths"
	"github.com/jackspiering/tailarr/internal/security/redact"
	"github.com/jackspiering/tailarr/internal/version"
)

// Manager coordinates deploy/update/stop/restart/remove/repair.
type Manager struct {
	Cfg *config.Config
	Log *logging.Logger
	// UI is optional interactive prompts. When nil, deploy is non-interactive.
	UI prompt.UI
}

// DeployOpts controls optional deploy behavior.
type DeployOpts struct {
	// Force replaces an existing managed deployment (with backup).
	// When false and UI is set, the operator is asked before replace.
	Force bool
	// Interactive prompts for empty/placeholder env values when UI is set.
	// Default true when UI is non-nil unless set false via SkipInteractive.
	SkipInteractive bool
	// ReusableAuthKey is an already-resolved TS_AUTHKEY for batch deploys.
	ReusableAuthKey string
}

// TailarrComposeLabel is applied via override so status can detect managed stacks.
const TailarrComposeLabel = "com.tailarr.managed=true"

// overrideFilename is written next to the service compose file.
const overrideFilename = ".tailarr.compose.yaml"

// DeployWith deploys a service: copies the template into the deploy path,
// merges env, and runs compose up.
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
		if st.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: refusing to operate on symlink deployment: %s", ErrSymlink, service)
		}
		if found, err := paths.ContainsSymlinks(dest); err != nil {
			return fmt.Errorf("deployment: %w", err)
		} else if found != "" {
			return fmt.Errorf("%w: deployment contains unsupported symlink: %s", ErrSymlink, found)
		}
		if !IsManaged(dest) {
			return fmt.Errorf("%w: refusing to replace unmanaged path %s (no Tailarr marker)", ErrNotManaged, service)
		}
		if !opts.Force {
			if m.UI == nil {
				return fmt.Errorf("%w: %s (replace requires confirmation)", ErrAlreadyDeployed, service)
			}
			ok, cerr := m.UI.Confirm(fmt.Sprintf("Deployment already exists for %s. Replace it?", service), false)
			if cerr != nil {
				return cerr
			}
			if !ok {
				return fmt.Errorf("%w: deployment canceled", prompt.ErrCanceled)
			}
		}
		// Back up the current deployment before any teardown so a failure below
		// can always restore it. The backup is the source of truth until the
		// new deployment is fully in place.
		backupPath, err = Backup(m.Cfg.DeployPath, service, dest, BackupMove)
		if err != nil {
			return err
		}
		m.log("backup created for %s: %s", service, backupPath)
		// Best-effort compose down after backup: composeProjectArgs derives the
		// project name from deployPath+service so containers are still found,
		// while cmd.Dir=backupPath makes docker compose read the moved file.
		proj := composeProjectArgs(m.Cfg.DeployPath, service)
		downArgs := append(append([]string{}, proj...), "down", "--remove-orphans")
		if err := Compose(backupPath, downArgs...); err != nil {
			m.log("warning: compose down before force replace of %s: %v", service, err)
		}
	}

	// From here, if we moved the old deployment, failures must try to restore it.
	if err := m.finishDeploy(service, templateDir, dest, backupPath, opts); err != nil {
		if backupPath != "" {
			if rerr := restoreDeploymentFromBackup(m.Cfg.DeployPath, service, backupPath, dest); rerr != nil {
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
	if err := writeOverride(service, dest); err != nil {
		return err
	}

	proj := composeProjectArgs(m.Cfg.DeployPath, service)
	upArgs := append(append([]string{}, proj...),
		"-f", composeBaseName(dest), "-f", overrideFilename, "up", "-d", "--remove-orphans")
	if err := Compose(dest, upArgs...); err != nil {
		return err
	}
	if backupPath != "" {
		m.log("backup retained for rollback: %s", backupPath)
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

	if opts.ReusableAuthKey != "" && scaletail.IsPlaceholder(merged["TS_AUTHKEY"]) {
		merged["TS_AUTHKEY"] = opts.ReusableAuthKey
	}

	// Interactive fill for remaining placeholders when a UI is available.
	if m.UI != nil && !opts.SkipInteractive {
		if err := m.promptMissingEnv(merged, keys); err != nil {
			return err
		}
	}

	if err := scaletail.ValidateMergedTSAuthkey(merged); err != nil {
		return err
	}
	// Fail closed when template declares TS_AUTHKEY but it is still empty.
	if _, declared := templateMap["TS_AUTHKEY"]; declared {
		if scaletail.IsPlaceholder(merged["TS_AUTHKEY"]) {
			return ErrEmptyAuthkey
		}
	}
	return scaletail.WriteEnvFile(localEnv, merged, keys)
}

func (m *Manager) promptMissingEnv(merged scaletail.EnvMap, keys []string) error {
	for _, key := range scaletail.PlaceholderKeys(merged, keys) {
		if key == "TS_AUTHKEY" {
			if !scaletail.IsPlaceholder(merged[key]) {
				continue
			}
			// Offer stored keys first when present.
			if store, err := authkeys.Load(m.Cfg.AuthkeysPath); err == nil && len(store.Order) > 0 {
				m.UI.Printf("Stored auth keys: %s\n", strings.Join(store.Order, ", "))
				name, err := m.UI.Line("Auth key name (empty to paste a new key)", "")
				if err != nil {
					return err
				}
				if name != "" {
					val, ok := store.Keys[name]
					if !ok {
						return fmt.Errorf("auth key %q not found in store", name)
					}
					merged[key] = val
					continue
				}
			}
			val, err := m.UI.Secret("TS_AUTHKEY")
			if err != nil {
				return err
			}
			if !names.ValidTSAuthkey(val) {
				return fmt.Errorf("TS_AUTHKEY must start with tskey-auth-")
			}
			merged[key] = val
			if ok, _ := m.UI.Confirm("Store this key for future use?", true); ok {
				storeName, err := m.UI.Line("Stored key name", "default")
				if err != nil {
					return err
				}
				if storeName != "" {
					if err := storeAuthkey(m.Cfg.AuthkeysPath, storeName, val); err != nil {
						return err
					}
					m.UI.Printf("Stored auth key %s\n", storeName)
				}
			}
			continue
		}

		def, _ := scaletail.DefaultForKey(key)
		var val string
		var err error
		if redact.LooksSecret(key) {
			val, err = m.UI.Secret(key)
			if err != nil {
				return err
			}
			if val == "" {
				val = def
			}
		} else {
			val, err = m.UI.Line(key, def)
			if err != nil {
				return err
			}
		}
		merged[key] = val
	}
	return nil
}

func writeOverride(service, dest string) error {
	services, err := ComposeServiceNames(dest)
	if err != nil {
		// Fall back to a marker-only file so IsManaged still works.
		body := "# Generated by Tailarr - do not edit by hand\n# Managed: " + TailarrComposeLabel + "\n"
		return atomic.WriteFileString(filepath.Join(dest, overrideFilename), body, 0o644)
	}
	var b strings.Builder
	b.WriteString("# Generated by Tailarr. Do not edit by hand.\n")
	b.WriteString("services:\n")
	if len(services) == 0 {
		body := "# Generated by Tailarr - do not edit by hand\n# Managed: " + TailarrComposeLabel + "\n"
		return atomic.WriteFileString(filepath.Join(dest, overrideFilename), body, 0o644)
	}
	for _, svc := range services {
		fmt.Fprintf(&b, "  %s:\n", svc)
		b.WriteString("    labels:\n")
		b.WriteString("      tailarr.managed: \"true\"\n")
		fmt.Fprintf(&b, "      tailarr.service: %q\n", service)
		fmt.Fprintf(&b, "      tailarr.version: %q\n", version.Version)
	}
	return atomic.WriteFileString(filepath.Join(dest, overrideFilename), b.String(), 0o644)
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
	if _, err = io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Sync(); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// storeAuthkey writes a named key using the same lock as the Authkeys menu.
func storeAuthkey(path, name, value string) error {
	lock, err := AcquireLock(AuthkeysLockPath(path), DefaultLockTimeout)
	if err != nil {
		return fmt.Errorf("authkeys lock: %w", err)
	}
	defer func() { _ = lock.Release() }()
	s, err := authkeys.Load(path)
	if err != nil {
		return err
	}
	if err := s.Put(name, value); err != nil {
		return err
	}
	return s.Save()
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

	backupPath, err := Backup(m.Cfg.DeployPath, service, dest, BackupCopy)
	if err != nil {
		return err
	}

	// Preserve .env
	envPath := filepath.Join(dest, ".env")
	var envBackup []byte
	if data, err := os.ReadFile(envPath); err == nil {
		envBackup = data
	}

	templateDir := filepath.Join(m.Cfg.RepoPath, "services", service)
	// Remove stale compose candidates that the template no longer ships. An
	// old compose.yaml left in dest would otherwise shadow a newer
	// compose.yml (ComposeFileIn prefers compose.yaml) and `up` would keep
	// running the old stack. The backup taken above restores these files if
	// the repair later fails.
	for _, name := range scaletail.ComposeCandidates {
		if _, err := os.Stat(filepath.Join(templateDir, name)); err == nil {
			continue
		}
		stale := filepath.Join(dest, name)
		if st, err := os.Lstat(stale); err == nil && !st.IsDir() && !paths.IsSymlink(stale) {
			if err := os.Remove(stale); err != nil {
				return fmt.Errorf("remove stale compose file %s: %w", name, err)
			}
		}
	}
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
	if err := writeOverride(service, dest); err != nil {
		return err
	}
	proj := composeProjectArgs(m.Cfg.DeployPath, service)
	upArgs := append(append([]string{}, proj...),
		"-f", composeBaseName(dest), "-f", overrideFilename, "up", "-d", "--remove-orphans")
	if err := Compose(dest, upArgs...); err != nil {
		if backupPath != "" {
			if rerr := restoreRepairFromBackup(backupPath, dest); rerr != nil {
				return fmt.Errorf("repair failed (%v); also failed to restore compose files from %s: %w", err, backupPath, rerr)
			}
			m.log("restored previous compose files for %s after failed repair", service)
			return fmt.Errorf("repair failed; previous compose files restored: %w", err)
		}
		return err
	}
	m.log("repaired service %s", service)
	return nil
}

func restoreRepairFromBackup(backupPath, dest string) error {
	for _, name := range scaletail.ComposeCandidates {
		src := filepath.Join(backupPath, name)
		if st, err := os.Stat(src); err != nil || st.IsDir() || paths.IsSymlink(src) {
			continue
		}
		if err := copyFileMode(src, filepath.Join(dest, name), 0o644); err != nil {
			return err
		}
	}
	ov := filepath.Join(backupPath, overrideFilename)
	if st, err := os.Stat(ov); err == nil && !st.IsDir() && !paths.IsSymlink(ov) {
		if err := copyFileMode(ov, filepath.Join(dest, overrideFilename), 0o644); err != nil {
			return err
		}
	}
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

// RemoveWith tears down a deployment. Fails closed: directory is only deleted
// after compose down succeeds.
func (m *Manager) RemoveWith(service string, opts DeployOpts) error {
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

	if m.UI != nil {
		ok, cerr := m.UI.Confirm(fmt.Sprintf("Remove %s and delete %s?", service, dest), false)
		if cerr != nil {
			return cerr
		}
		if !ok {
			return fmt.Errorf("%w: remove canceled", prompt.ErrCanceled)
		}
	}

	if _, err := Backup(m.Cfg.DeployPath, service, dest, BackupCopy); err != nil {
		return err
	}
	proj := composeProjectArgs(m.Cfg.DeployPath, service)
	args := append(append([]string{}, proj...), "down", "--remove-orphans")
	if err := Compose(dest, args...); err != nil {
		return fmt.Errorf("compose down failed; deployment directory left intact: %w", err)
	}
	if err := safeRemoveTree(dest, m.Cfg.DeployPath); err != nil {
		return err
	}

	// Offer to delete retained backups (they may contain .env secrets).
	if m.UI != nil && !opts.SkipInteractive {
		if backups, _ := listServiceBackups(m.Cfg.DeployPath, service); len(backups) > 0 {
			m.UI.Printf("%d backup(s) for %s remain under .tailarr_backups and may contain secrets.\n", len(backups), service)
			if ok, _ := m.UI.Confirm("Delete these backups as well?", false); ok {
				root := filepath.Join(m.Cfg.DeployPath, config.BackupDirName)
				for _, b := range backups {
					_ = safeRemoveTree(b, root)
				}
				m.log("removed %d backups for %s", len(backups), service)
			}
		}
	}

	m.log("removed service %s", service)
	return nil
}

func listServiceBackups(deployPath, service string) ([]string, error) {
	root := filepath.Join(deployPath, config.BackupDirName)
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() || !isServiceBackupName(service, e.Name()) {
			continue
		}
		p := filepath.Join(root, e.Name())
		if paths.IsSymlink(p) {
			continue
		}
		out = append(out, p)
	}
	return out, nil
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
	s := string(data)
	return strings.Contains(s, "com.tailarr.managed") || strings.Contains(s, "tailarr.managed")
}
