package ui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/jackspiering/tailarr/internal/authkeys"
	"github.com/jackspiering/tailarr/internal/config"
	"github.com/jackspiering/tailarr/internal/deploy"
	"github.com/jackspiering/tailarr/internal/interrupt"
	"github.com/jackspiering/tailarr/internal/logging"
	"github.com/jackspiering/tailarr/internal/prompt"
	"github.com/jackspiering/tailarr/internal/scaletail"
	"github.com/jackspiering/tailarr/internal/security/names"
	"github.com/jackspiering/tailarr/internal/security/redact"
	"github.com/jackspiering/tailarr/internal/upgrade"
	"github.com/jackspiering/tailarr/internal/version"
)

type multiMode int

const (
	multiNone multiMode = iota
	multiDeploy
	multiRemove
	multiApply
	multiStop
	multiRestart
)

func multiTitle(mode multiMode) string {
	switch mode {
	case multiDeploy:
		return "Deploy"
	case multiRemove:
		return "Remove"
	case multiApply:
		return "Apply"
	case multiStop:
		return "Stop"
	case multiRestart:
		return "Restart"
	}
	return "Select"
}

func multiDone(mode multiMode) string {
	switch mode {
	case multiDeploy:
		return "Deployed"
	case multiRemove:
		return "Removed"
	case multiApply:
		return "Applied catalog to"
	case multiStop:
		return "Stopped"
	case multiRestart:
		return "Restarted"
	}
	return "Done:"
}

// batchResult is the outcome of runBatchWith. text lists every service with
// its result; the counts drive the one-line TUI summary.
type batchResult struct {
	text                string
	ok, failed, skipped int
	failedNames         []string
	canceled            bool
	interrupted         bool
}

// summary returns one line for the output panel.
func (r batchResult) summary(mode multiMode, services []string) string {
	if r.canceled {
		return "Canceled."
	}
	if r.failed == 0 && r.skipped == 0 {
		what := fmt.Sprintf("%d services", len(services))
		if len(services) == 1 {
			what = services[0]
		}
		return "✔ " + multiDone(mode) + " " + what
	}
	line := fmt.Sprintf("✖ %s interrupted", multiTitle(mode))
	if r.failed > 0 && !r.interrupted {
		line = fmt.Sprintf("✖ %s failed for %s", multiTitle(mode), summarizeNames(r.failedNames, 3))
	}
	line += fmt.Sprintf(" · %d of %d ok", r.ok, len(services))
	if r.skipped > 0 {
		line += fmt.Sprintf(", %d skipped", r.skipped)
	}
	return line
}

// runBatchWith runs mode on each service. It stops at the first interrupt,
// logs every failure, and asks once before a Deploy, Stop, or Restart batch.
// Apply and Remove confirm per service. Progress goes to ui.Printf.
func runBatchWith(cfg config.Config, log *logging.Logger, ui prompt.UI, mode multiMode, services []string) batchResult {
	verb := multiTitle(mode)
	if mode == multiDeploy || mode == multiStop || mode == multiRestart {
		ok, err := ui.Confirm(fmt.Sprintf("%s %d service(s): %s?", verb, len(services), summarizeNames(services, 8)), false)
		if err != nil || !ok {
			return batchResult{text: "Canceled.", canceled: true}
		}
	}
	mgr := &deploy.Manager{Cfg: &cfg, Log: log, UI: ui}
	var sharedKey string
	if mode == multiDeploy && len(services) > 1 {
		key, err := sharedAuthkey(cfg, ui)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, prompt.ErrCanceled) {
				return batchResult{text: "Canceled.", canceled: true}
			}
			return batchResult{text: "Error: " + redact.Text(err.Error()), failed: len(services)}
		}
		sharedKey = key
	}
	var r batchResult
	var b strings.Builder
	skip := func(rest []string) {
		for _, name := range rest {
			fmt.Fprintf(&b, "==> %s\n  skipped: interrupted\n", name)
			ui.Printf("==> %s\n  skipped: interrupted\n", name)
			r.skipped++
		}
	}
	for i, svc := range services {
		if interrupt.Context().Err() != nil {
			skip(services[i:])
			break
		}
		fmt.Fprintf(&b, "==> %s\n", svc)
		ui.Printf("==> %s\n", svc)
		var err error
		switch mode {
		case multiDeploy:
			err = mgr.DeployWith(svc, deploy.DeployOpts{ReusableAuthKey: sharedKey})
		case multiApply:
			err = mgr.Apply(svc, deploy.DeployOpts{ReusableAuthKey: sharedKey})
		case multiRemove:
			err = mgr.RemoveWith(svc, deploy.DeployOpts{})
		case multiStop:
			err = mgr.Stop(svc)
		case multiRestart:
			err = mgr.Restart(svc)
		}
		if err == nil {
			b.WriteString("  ok\n")
			ui.Printf("  ✔ ok\n")
			r.ok++
			continue
		}
		msg := redact.Text(err.Error())
		fmt.Fprintf(&b, "  error: %s\n", msg)
		ui.Printf("  error: %s\n", msg)
		r.failed++
		r.failedNames = append(r.failedNames, svc)
		if log != nil {
			log.Event(fmt.Sprintf("%s %s failed: %v", strings.ToLower(verb), svc, err))
		}
		if errors.Is(err, deploy.ErrInterrupted) || errors.Is(err, context.Canceled) {
			r.interrupted = true
			skip(services[i+1:])
			break
		}
	}
	r.text = b.String()
	return r
}

// sharedAuthkey asks for one auth key for a multi-service deploy. It returns
// "" when the operator wants per-service prompts.
func sharedAuthkey(cfg config.Config, ui prompt.UI) (string, error) {
	ok, err := ui.Confirm("Use one reusable Tailscale auth key for all selected services?", true)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", nil
	}
	s, err := authkeys.Load(cfg.AuthkeysPath)
	if err != nil {
		return "", err
	}
	if len(s.Order) > 0 {
		ui.Printf("Stored keys: %s\n", strings.Join(s.Order, ", "))
		name, err := ui.Line("Auth key name (empty to paste)", "")
		if err != nil {
			return "", err
		}
		if name != "" {
			key, ok := s.Keys[name]
			if !ok {
				return "", fmt.Errorf("auth key %q not found in store", name)
			}
			return key, nil
		}
	}
	val, err := ui.Secret("TS_AUTHKEY for all services (empty to ask per service)")
	if err != nil {
		return "", err
	}
	if val == "" {
		return "", nil
	}
	if !names.ValidTSAuthkey(val) {
		return "", fmt.Errorf("TS_AUTHKEY must start with tskey-auth-")
	}
	return val, nil
}

// summarizeNames joins up to max names and counts the rest.
func summarizeNames(list []string, max int) string {
	if len(list) <= max {
		return strings.Join(list, ", ")
	}
	return fmt.Sprintf("%s and %d more", strings.Join(list[:max], ", "), len(list)-max)
}

// runAuthkeyAction runs one auth key store action. name is the key the
// operator picked in the list; when empty, the action asks for it.
func runAuthkeyAction(cfg config.Config, ui prompt.UI, action, name string) string {
	// Serialize read-modify-write with a lock next to the store.
	lock, err := deploy.AcquireLock(deploy.AuthkeysLockPath(cfg.AuthkeysPath), deploy.DefaultLockTimeout)
	if err != nil {
		return "Error: authkeys lock: " + redact.Text(err.Error())
	}
	defer func() { _ = lock.Release() }()

	s, err := authkeys.Load(cfg.AuthkeysPath)
	if err != nil {
		return "Error: " + redact.Text(err.Error())
	}
	pick := func(label string) (string, bool) {
		if name != "" {
			return name, true
		}
		if len(s.Order) == 0 {
			return "", false
		}
		ui.Printf("Keys: %s\n", strings.Join(s.Order, ", "))
		n, err := ui.Line(label, "")
		return n, err == nil && n != ""
	}
	save := func(done string) string {
		if err := s.Save(); err != nil {
			return "Error: " + redact.Text(err.Error())
		}
		return done
	}
	switch action {
	case "add":
		n, err := ui.Line("New key name", "")
		if err != nil || n == "" {
			return "Canceled."
		}
		val, err := ui.Secret("TS_AUTHKEY for " + n)
		if err != nil {
			return "Canceled."
		}
		if err := s.Put(n, val); err != nil {
			return "Error: " + redact.Text(err.Error())
		}
		return save("✔ Stored auth key " + n)
	case "rename":
		if len(s.Order) == 0 {
			return "No stored keys."
		}
		old, ok := pick("Key to rename")
		if !ok {
			return "Canceled."
		}
		nw, err := ui.Line("New name for "+old, "")
		if err != nil || nw == "" {
			return "Canceled."
		}
		if err := s.Rename(old, nw); err != nil {
			return "Error: " + redact.Text(err.Error())
		}
		return save("✔ Renamed " + old + " to " + nw)
	case "replace":
		if len(s.Order) == 0 {
			return "No stored keys."
		}
		n, ok := pick("Key to replace")
		if !ok {
			return "Canceled."
		}
		if _, exists := s.Keys[n]; !exists {
			return "Error: auth key not found: " + n
		}
		val, err := ui.Secret("New TS_AUTHKEY for " + n)
		if err != nil {
			return "Canceled."
		}
		if err := s.Put(n, val); err != nil {
			return "Error: " + redact.Text(err.Error())
		}
		return save("✔ Updated auth key " + n)
	case "remove":
		if len(s.Order) == 0 {
			return "No stored keys."
		}
		n, ok := pick("Key to remove")
		if !ok {
			return "Canceled."
		}
		yes, err := ui.Confirm("Remove stored auth key "+n+"?", false)
		if err != nil || !yes {
			return "Canceled."
		}
		if err := s.Remove(n); err != nil {
			return "Error: " + redact.Text(err.Error())
		}
		return save("✔ Removed auth key " + n)
	}
	return ""
}

// editConfigInteractive edits the values saved in the config file. TAILARR_*
// environment overrides still apply to the running session but are not saved.
func editConfigInteractive(cfg *config.Config, ui prompt.UI) (string, bool) {
	next, err := config.LoadFile(cfg.ConfigPath)
	if err != nil {
		return "Error: " + redact.Text(err.Error()), false
	}
	if keys := config.EnvOverrides(); len(keys) > 0 {
		ui.Printf("Note: %s set in the environment. The environment wins at runtime; these prompts edit the saved values.\n",
			strings.Join(keys, ", "))
	}
	var raw string
	if raw, err = ui.Line("TAILARR_REPO_URL", next.RepoURL); err != nil {
		return redact.Text(err.Error()), false
	}
	raw = strings.TrimSpace(raw)
	if raw != "" {
		if err := names.ValidateRepoURL(raw); err != nil {
			return "Error saving: " + redact.Text(err.Error()), false
		}
	}
	next.RepoURL = raw
	if next.RepoPath, err = ui.Line("TAILARR_REPO_PATH", next.RepoPath); err != nil {
		return redact.Text(err.Error()), false
	}
	if next.DeployPath, err = ui.Line("TAILARR_DEPLOY_PATH", next.DeployPath); err != nil {
		return redact.Text(err.Error()), false
	}
	if next.LogPath, err = ui.Line("TAILARR_LOG_PATH", next.LogPath); err != nil {
		return redact.Text(err.Error()), false
	}
	if next.AuthkeysPath, err = ui.Line("TAILARR_AUTHKEYS_PATH", next.AuthkeysPath); err != nil {
		return redact.Text(err.Error()), false
	}
	if err := config.Save(next); err != nil {
		return "Error saving: " + redact.Text(err.Error()), false
	}
	effective := next
	effective.AssumeYes = cfg.AssumeYes
	if err := config.ApplyEnv(&effective); err != nil {
		return "Saved config, but the environment override is invalid: " + redact.Text(err.Error()), false
	}
	*cfg = effective
	return "✔ Saved config: " + next.ConfigPath, true
}

// runUpgradeAction checks for a newer release and installs it after a
// confirm. replaced reports that the running binary changed.
func runUpgradeAction(log *logging.Logger, ui prompt.UI, out io.Writer) (text string, replaced bool) {
	opts := upgrade.Options{Current: version.Version, Out: out}
	latest, err := upgrade.Latest(opts)
	if err != nil {
		return "Error: " + redact.Text(err.Error()), false
	}
	if upgrade.Comparable(version.Version, latest) && upgrade.Compare(version.Version, latest) >= 0 {
		return fmt.Sprintf("✔ Already up to date (%s)", version.Version), false
	}
	question := fmt.Sprintf("Upgrade Tailarr %s to %s?", version.Version, latest)
	if !upgrade.Comparable(version.Version, latest) {
		question = fmt.Sprintf("Installed %s is not SemVer; install %s anyway?", version.Version, latest)
	}
	ok, err := ui.Confirm(question, true)
	if err != nil || !ok {
		return "Canceled.", false
	}
	tag, err := upgrade.Upgrade(opts)
	if err != nil {
		return "Error: " + redact.Text(err.Error()), false
	}
	if log != nil {
		log.Event("tailarr upgraded to " + tag)
	}
	return fmt.Sprintf("Upgraded Tailarr to %s. Run tailarr again to use it.", tag), true
}

func runCatalogRefresh(cfg config.Config) string {
	lock, err := deploy.AcquireLock(deploy.RepoLockPath(cfg.RepoPath), deploy.DefaultLockTimeout)
	if err != nil {
		return "Error: repo lock: " + redact.Text(err.Error())
	}
	defer func() { _ = lock.Release() }()
	msg, err := scaletail.Refresh(cfg.RepoURL, cfg.RepoPath)
	if err != nil {
		return "Error: " + redact.Text(err.Error())
	}
	return refreshSummary(msg)
}

// refreshSummary turns git output from a catalog refresh into one result.
func refreshSummary(msg string) string {
	if strings.HasPrefix(msg, "Using local") {
		return msg
	}
	if strings.TrimSpace(msg) == "" || strings.Contains(msg, "Already up to date") {
		return "✔ Catalog is up to date."
	}
	return strings.TrimRight(msg, "\n") + "\n✔ Catalog refreshed."
}

// filterNames returns the names that contain query, ignoring case.
func filterNames(list []string, query string) []string {
	q := strings.ToLower(strings.TrimSpace(query))
	var out []string
	for _, name := range list {
		if q == "" || strings.Contains(strings.ToLower(name), q) {
			out = append(out, name)
		}
	}
	return out
}
