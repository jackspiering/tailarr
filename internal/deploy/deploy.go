// Package deploy implements service lifecycle against Docker Compose.
package deploy

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
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

// Manager coordinates deploy/apply/stop/restart/remove.
type Manager struct {
	Cfg *config.Config
	Log *logging.Logger
	// UI is optional interactive prompts. When nil, deploy is non-interactive.
	UI prompt.UI
}

// DeployOpts controls optional deploy and apply behavior.
type DeployOpts struct {
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

// managedYAMLLabelRE matches the compose label Tailarr writes in writeOverrideUsing.
var managedYAMLLabelRE = regexp.MustCompile(`(?m)^\s+tailarr\.managed:\s*"true"\s*$`)

// managedMarkerRE matches the comment form Tailarr writes in writeMarkerOnly.
var managedMarkerRE = regexp.MustCompile(`(?m)^# Managed: com\.tailarr\.managed=true\s*$`)

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
	repoLock, err := m.lockRepo()
	if err != nil {
		return err
	}
	defer func() { _ = repoLock.Release() }()

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
		return fmt.Errorf("%w: %s (use Apply)", ErrAlreadyDeployed, service)
	} else if !os.IsNotExist(err) {
		return err
	}

	tplEnv, err := readTemplateEnv(templateDir)
	if err != nil {
		return err
	}
	// The catalog is not read after the template copy, so release its lock
	// before env prompts: other instances need it for unrelated services.
	releaseRepo := func() { _ = repoLock.Release() }
	if started, err := m.finishDeploy(service, templateDir, dest, tplEnv, opts, releaseRepo); err != nil {
		// A failed up can leave some containers running. Take them down before
		// deleting dest; if that fails, keep dest so Remove can clean up later.
		if started {
			args := append(composeProjectArgs(m.Cfg.DeployPath, service), "down", "--remove-orphans")
			if derr := composeCleanup(dest, args...); derr != nil {
				m.log("warning: compose down after failed deploy of %s: %v", service, derr)
				return fmt.Errorf("%w; compose down also failed, kept %s so Remove can clean up: %v", err, dest, derr)
			}
		}
		if rerr := safeRemoveTree(dest, m.Cfg.DeployPath); rerr != nil {
			m.log("warning: could not remove partial deployment %s: %v", dest, rerr)
		}
		return err
	}
	m.log("deployed service %s", service)
	return nil
}

// finishDeploy populates dest and runs compose up. started reports whether
// compose up ran, so a failure may have left containers behind. afterCopy
// runs once the template is copied.
func (m *Manager) finishDeploy(service, templateDir, dest string, tplEnv []byte, opts DeployOpts, afterCopy func()) (started bool, err error) {
	err = copyTemplate(templateDir, dest)
	afterCopy()
	if err != nil {
		return false, err
	}
	if err := m.mergeAndWriteEnv(tplEnv, dest, opts); err != nil {
		return false, err
	}
	if err := writeOverride(service, dest); err != nil {
		return false, err
	}

	proj := composeProjectArgs(m.Cfg.DeployPath, service)
	upArgs := append(append([]string{}, proj...),
		"-f", composeBaseName(dest), "-f", overrideFilename, "up", "-d", "--remove-orphans")
	return true, Compose(dest, upArgs...)
}

// Apply syncs catalog template files onto an existing managed deployment, then
// pulls images and runs compose up. Dest-only paths and dest .env are kept.
// Create is Deploy only. Before changing anything, Apply saves the files it
// may write. A failure puts those files back in place and, when compose up
// had started, runs up again so the containers match the restored files.
// Container data is never copied or moved.
func (m *Manager) Apply(service string, opts DeployOpts) (retErr error) {
	if err := names.ValidateServiceName(service); err != nil {
		return err
	}
	// Ask before taking locks so a slow answer does not block other
	// Tailarr instances. Everything is checked again under the locks.
	if m.UI != nil {
		dest, err := paths.JoinUnder(m.Cfg.DeployPath, service)
		if err != nil {
			return err
		}
		if err := requireApplyTarget(dest, service); err != nil {
			return err
		}
		ok, cerr := m.UI.Confirm(fmt.Sprintf("Apply catalog to %s? This overwrites template files and pulls images.", service), false)
		if cerr != nil {
			return cerr
		}
		if !ok {
			return fmt.Errorf("%w: apply canceled", prompt.ErrCanceled)
		}
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
	repoLock, err := m.lockRepo()
	if err != nil {
		return err
	}
	defer func() { _ = repoLock.Release() }()

	if err := paths.RefuseSymlinkAncestry(m.Cfg.DeployPath); err != nil {
		return fmt.Errorf("deployment root: %w", err)
	}

	dest, err := paths.JoinUnder(m.Cfg.DeployPath, service)
	if err != nil {
		return err
	}
	if err := requireApplyTarget(dest, service); err != nil {
		return err
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
	tplEnv, err := readTemplateEnv(templateDir)
	if err != nil {
		return err
	}

	snap, err := snapshotManaged(m.Cfg.DeployPath, service, templateDir, dest)
	if err != nil {
		return err
	}
	m.log("backup created for %s: %s", service, snap.dir)

	restore := true
	upStarted := false
	defer func() {
		if !restore {
			return
		}
		if rerr := snap.restore(); rerr != nil {
			retErr = fmt.Errorf("apply failed (%v); also failed to restore previous files from %s: %w", retErr, snap.dir, rerr)
			return
		}
		m.log("restored previous files for %s after failed apply", service)
		switch {
		case !upStarted:
			retErr = fmt.Errorf("apply failed; previous deployment restored: %w", retErr)
		case errors.Is(retErr, ErrInterrupted):
			retErr = fmt.Errorf("apply interrupted; previous files restored, but containers may not match them (run Restart): %w", retErr)
		default:
			upArgs := append(composeProjectArgs(m.Cfg.DeployPath, service),
				"-f", composeBaseName(dest), "-f", overrideFilename, "up", "-d", "--remove-orphans")
			if uerr := Compose(dest, upArgs...); uerr != nil {
				m.log("warning: compose up with restored files failed for %s: %v", service, uerr)
				retErr = fmt.Errorf("apply failed; previous files restored, but starting them also failed (%v): %w", uerr, retErr)
				return
			}
			retErr = fmt.Errorf("apply failed; previous deployment restored and started: %w", retErr)
		}
	}()

	if err := syncTemplateFiles(templateDir, dest); err != nil {
		return err
	}
	composeFile := composeBaseName(templateDir)
	// The catalog is not read again; free it before env prompts and compose.
	_ = repoLock.Release()
	if err := m.mergeAndWriteEnv(tplEnv, dest, opts); err != nil {
		return err
	}
	if err := writeOverrideUsing(service, dest, composeFile); err != nil {
		return err
	}

	proj := composeProjectArgs(m.Cfg.DeployPath, service)
	pullArgs := append(append([]string{}, proj...), "-f", composeFile, "pull")
	if err := Compose(dest, pullArgs...); err != nil {
		return err
	}
	upStarted = true
	upArgs := append(append([]string{}, proj...),
		"-f", composeFile, "-f", overrideFilename, "up", "-d", "--remove-orphans")
	if err := Compose(dest, upArgs...); err != nil {
		return err
	}
	restore = false
	m.log("applied catalog to service %s", service)
	return nil
}

// requireApplyTarget checks that dest is an existing managed deployment
// that Apply may update.
func requireApplyTarget(dest, service string) error {
	st, err := os.Lstat(dest)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%w: %s (use Deploy)", ErrNotDeployed, service)
		}
		return err
	}
	if st.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%w: refusing to operate on symlink deployment: %s", ErrSymlink, service)
	}
	return requireManagedFiles(dest, service)
}

// readTemplateEnv returns the template .env contents, or nil when the
// template has none.
func readTemplateEnv(templateDir string) ([]byte, error) {
	f, err := paths.OpenFileNoFollow(filepath.Join(templateDir, ".env"), os.O_RDONLY, 0)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("template .env: %w", err)
	}
	defer func() { _ = f.Close() }()
	return io.ReadAll(io.LimitReader(f, 1<<20))
}

// syncTemplateFiles copies template files onto dest without deleting dest-only
// paths and without overwriting dest .env. A file/directory type mismatch fails closed.
func syncTemplateFiles(templateDir, dest string) error {
	destClean := filepath.Clean(dest)
	return filepath.Walk(templateDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(templateDir, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if rel == ".env" {
			return nil
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: refusing to copy symlink: %s", ErrSymlink, path)
		}
		target := filepath.Join(dest, rel)
		targetClean := filepath.Clean(target)
		if targetClean != destClean && !strings.HasPrefix(targetClean, destClean+string(os.PathSeparator)) {
			return fmt.Errorf("sync path escaped destination: %s", rel)
		}
		// Refuse if any existing ancestor of target is a symlink (TOCTOU window is bounded by per-service lock).
		if err := paths.RefuseSymlinkAncestry(filepath.Dir(target)); err != nil {
			return fmt.Errorf("%w: %w", ErrSymlink, err)
		}

		destInfo, err := os.Lstat(target)
		if os.IsNotExist(err) {
			if info.IsDir() {
				return os.MkdirAll(target, 0o755)
			}
			return copyFile(path, target, info.Mode().Perm())
		}
		if err != nil {
			return err
		}
		if destInfo.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: dest path is a symlink: %s", ErrSymlink, target)
		}
		if info.IsDir() {
			if !destInfo.IsDir() {
				return fmt.Errorf("apply type mismatch: template %s is a directory, dest is a file", rel)
			}
			return nil
		}
		if destInfo.IsDir() {
			return fmt.Errorf("apply type mismatch: template %s is a file, dest is a directory", rel)
		}
		return copyFile(path, target, info.Mode().Perm())
	})
}

func composeBaseName(dir string) string {
	if p, ok := scaletail.ComposeFileIn(dir); ok {
		return filepath.Base(p)
	}
	return "compose.yaml"
}

func (m *Manager) mergeAndWriteEnv(tplEnv []byte, dest string, opts DeployOpts) error {
	localEnv := filepath.Join(dest, ".env")
	templateMap, keys, err := scaletail.ParseEnv(bytes.NewReader(tplEnv))
	if err != nil {
		return fmt.Errorf("template .env: %w", err)
	}
	// Apply never overwrites dest .env, so it already holds the deployed values.
	localMap, err := scaletail.ParseEnvFile(localEnv)
	if err != nil {
		return err
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
		quoted, err := scaletail.QuoteEnvValue(val)
		if err != nil {
			return fmt.Errorf("%s: %w", key, err)
		}
		merged[key] = quoted
	}
	return nil
}

// composeServiceNameRE matches valid Compose service names. The YAML scan
// fallback in composeServiceNames can misread nested keys; never emit a name
// that would produce invalid YAML in the generated override.
var composeServiceNameRE = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`)

func writeOverride(service, dest string) error {
	return writeOverrideUsing(service, dest, composeBaseName(dest))
}

func writeOverrideUsing(service, dest, composeFile string) error {
	services, err := composeServiceNames(dest, composeFile)
	if err != nil {
		// Fall back to a marker-only file so IsManaged still works.
		return writeMarkerOnly(dest)
	}
	valid := services[:0]
	for _, svc := range services {
		if !composeServiceNameRE.MatchString(svc) {
			continue
		}
		valid = append(valid, svc)
	}
	if len(valid) == 0 {
		return writeMarkerOnly(dest)
	}
	var b strings.Builder
	b.WriteString("# Generated by Tailarr. Do not edit by hand.\n")
	b.WriteString("services:\n")
	for _, svc := range valid {
		fmt.Fprintf(&b, "  %s:\n", svc)
		b.WriteString("    labels:\n")
		b.WriteString("      tailarr.managed: \"true\"\n")
		fmt.Fprintf(&b, "      tailarr.service: %q\n", service)
		fmt.Fprintf(&b, "      tailarr.version: %q\n", version.Version)
	}
	return atomic.WriteFileString(filepath.Join(dest, overrideFilename), b.String(), 0o644)
}

func writeMarkerOnly(dest string) error {
	body := "# Generated by Tailarr - do not edit by hand\n# Managed: " + TailarrComposeLabel + "\n"
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
		return copyFile(path, target, info.Mode().Perm())
	})
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

// Restart stops a deployment, then starts it with compose up. compose restart
// restarts every container at once, so an app with network_mode: service:
// joins the namespace of a sidecar that is still stopping and exits. up
// starts the sidecar first and waits for its depends_on condition.
func (m *Manager) Restart(service string) error {
	return m.withManagedServiceDir(service, func(dir string) error {
		proj := composeProjectArgs(m.Cfg.DeployPath, service)
		stopArgs := append(append([]string{}, proj...), "stop")
		if err := Compose(dir, stopArgs...); err != nil {
			return err
		}
		upArgs := append(append([]string{}, proj...),
			"-f", composeBaseName(dir), "-f", overrideFilename, "up", "-d", "--remove-orphans")
		if err := Compose(dir, upArgs...); err != nil {
			return fmt.Errorf("restart stopped %s but could not start it again; it is stopped now (fix the cause, then Restart or Apply): %w", service, err)
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
		question := fmt.Sprintf("Remove %s and delete %s?", service, dest)
		if size, err := treeSize(dest); err == nil {
			question = fmt.Sprintf("Remove %s and delete %s? A backup copy (%s) is made first.", service, dest, formatBytes(size))
		}
		ok, cerr := m.UI.Confirm(question, false)
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
				removed := 0
				for _, b := range backups {
					if err := safeRemoveTree(b, root); err != nil {
						m.UI.Printf("Could not delete %s; it may contain secrets: %v\n", b, err)
						m.log("warning: could not delete backup %s: %v", b, err)
						continue
					}
					removed++
				}
				m.log("removed %d of %d backups for %s", removed, len(backups), service)
			}
		}
	}

	m.log("removed service %s", service)
	return nil
}

// treeSize returns the total size of the regular files under root.
func treeSize(root string) (int64, error) {
	var total int64
	err := filepath.WalkDir(root, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type().IsRegular() {
			info, err := d.Info()
			if err != nil {
				return err
			}
			total += info.Size()
		}
		return nil
	})
	return total, err
}

// formatBytes renders n with a binary unit, for example "12.3 MiB".
func formatBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
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
	if err := requireManagedFiles(dest, service); err != nil {
		return err
	}
	return fn(dest)
}

// requireManagedDeploy ensures dest is a managed deployment with no symlink
// anywhere in the tree. Apply and Remove copy and delete the whole tree, so
// they need it. Containers often write root-owned data, so a non-root
// operator gets a permission error that says to run as root.
func requireManagedDeploy(dest, service string) error {
	if err := requireManagedFiles(dest, service); err != nil {
		return err
	}
	if found, err := paths.ContainsSymlinks(dest); err != nil {
		if errors.Is(err, fs.ErrPermission) {
			return fmt.Errorf("cannot read container data; run Tailarr as root to back up and delete %s: %w", service, err)
		}
		return err
	} else if found != "" {
		return fmt.Errorf("%w: deployment contains unsupported symlink: %s", ErrSymlink, found)
	}
	return nil
}

// requireManagedFiles ensures dest is a managed deployment whose compose
// inputs are not symlinks. Stop and Restart only run compose, so they do not
// walk container data, which may be unreadable or hold symlinks.
func requireManagedFiles(dest, service string) error {
	st, err := os.Lstat(dest)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%w: %s", ErrNotDeployed, service)
		}
		return err
	}
	if st.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%w: refusing to operate on symlink deployment: %s", ErrSymlink, service)
	}
	inputs := append([]string{overrideFilename, ".env"}, scaletail.ComposeCandidates...)
	for _, name := range inputs {
		if p := filepath.Join(dest, name); paths.IsSymlink(p) {
			return fmt.Errorf("%w: deployment contains unsupported symlink: %s", ErrSymlink, p)
		}
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
// It requires the structured forms Tailarr writes (YAML label or marker
// comment), not a substring match of "tailarr.managed".
func IsManaged(dir string) bool {
	p := filepath.Join(dir, overrideFilename)
	data, err := os.ReadFile(p)
	if err != nil {
		return false
	}
	s := string(data)
	return managedYAMLLabelRE.MatchString(s) || managedMarkerRE.MatchString(s)
}
