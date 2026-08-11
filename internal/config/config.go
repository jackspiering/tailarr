// Package config loads and saves plain-text KEY=VALUE Tailarr configuration.
// User files are never source'd or eval'd.
package config

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/jackspiering/tailarr/internal/security/atomic"
	"github.com/jackspiering/tailarr/internal/security/names"
	"github.com/jackspiering/tailarr/internal/security/paths"
)

// Conventional default paths for a single-host operator install.
const (
	DefaultConfigPath   = "/opt/tailarr/tailarr.conf"
	DefaultAuthkeysPath = "/opt/tailarr/authkeys.conf"
	DefaultRepoPath     = "/opt/tailarr/scaletail"
	DefaultDeployPath   = "/opt/docker/stacks"
	DefaultLogPath      = "/opt/tailarr/logs/tailarr.log"
	DefaultRepoURL      = "https://github.com/tailscale-dev/ScaleTail.git"
	DefaultLogMaxBytes  = 5 * 1024 * 1024
	BackupDirName       = ".tailarr_backups"
	LockDirName         = ".tailarr_locks"
)

// Config holds runtime configuration for Tailarr.
type Config struct {
	ConfigPath   string
	RepoURL      string
	RepoPath     string
	DeployPath   string
	LogPath      string
	AuthkeysPath string
	RepoRef      string
	LogMaxBytes  int64
	NoRefresh    bool
	AssumeYes    bool
}

// Default returns a Config with built-in defaults (before file/env/flags).
func Default() Config {
	return Config{
		ConfigPath:   envOr("TAILARR_CONFIG_PATH", DefaultConfigPath),
		RepoURL:      DefaultRepoURL,
		RepoPath:     DefaultRepoPath,
		DeployPath:   DefaultDeployPath,
		LogPath:      DefaultLogPath,
		AuthkeysPath: DefaultAuthkeysPath,
		RepoRef:      "",
		LogMaxBytes:  DefaultLogMaxBytes,
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// Load reads the config file (if present), then applies environment overrides.
// Missing config file is not an error.
func Load(cfg *Config) error {
	if cfg.ConfigPath == "" {
		cfg.ConfigPath = envOr("TAILARR_CONFIG_PATH", DefaultConfigPath)
	}
	if err := loadFile(cfg, cfg.ConfigPath); err != nil {
		return err
	}
	applyEnv(cfg)
	return nil
}

func loadFile(cfg *Config, path string) error {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("open config: %w", err)
	}
	defer func() { _ = f.Close() }()

	sc := bufio.NewScanner(f)
	// Allow long lines without loading secrets into huge buffers unnecessarily.
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		// Keep value as-is after first '=' (may contain '=').
		switch key {
		case "TAILARR_REPO_URL":
			cfg.RepoURL = value
		case "TAILARR_REPO_PATH":
			cfg.RepoPath = value
		case "TAILARR_DEPLOY_PATH":
			cfg.DeployPath = value
		case "TAILARR_LOG_PATH":
			cfg.LogPath = value
		case "TAILARR_AUTHKEYS_PATH":
			cfg.AuthkeysPath = value
		case "TAILARR_REPO_REF":
			cfg.RepoRef = value
		case "TAILARR_LOG_MAX_BYTES":
			n, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
			if err == nil && n > 0 {
				cfg.LogMaxBytes = n
			}
		}
	}
	return sc.Err()
}

func applyEnv(cfg *Config) {
	if v := os.Getenv("TAILARR_REPO_URL"); v != "" {
		cfg.RepoURL = v
	}
	if v := os.Getenv("TAILARR_REPO_PATH"); v != "" {
		cfg.RepoPath = v
	}
	if v := os.Getenv("TAILARR_DEPLOY_PATH"); v != "" {
		cfg.DeployPath = v
	}
	if v := os.Getenv("TAILARR_LOG_PATH"); v != "" {
		cfg.LogPath = v
	}
	if v := os.Getenv("TAILARR_AUTHKEYS_PATH"); v != "" {
		cfg.AuthkeysPath = v
	}
	if v := os.Getenv("TAILARR_REPO_REF"); v != "" {
		cfg.RepoRef = v
	}
	if v := os.Getenv("TAILARR_LOG_MAX_BYTES"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			cfg.LogMaxBytes = n
		}
	}
	if v := os.Getenv("TAILARR_ASSUME_YES"); v == "1" || strings.EqualFold(v, "true") {
		cfg.AssumeYes = true
	}
}

// Save writes the config atomically as plain KEY=VALUE (mode 0600).
// Restrictive mode because RepoURL or paths could be sensitive in some setups.
func Save(cfg Config) error {
	if paths.IsSymlink(cfg.ConfigPath) {
		return fmt.Errorf("config file must not be a symlink: %s", cfg.ConfigPath)
	}
	// Reject credential-bearing URLs before persisting.
	if err := names.ValidateRepoURL(cfg.RepoURL); err != nil {
		return err
	}
	body := format(cfg)
	return atomic.WriteFileString(cfg.ConfigPath, body, 0o600)
}

func format(cfg Config) string {
	var b strings.Builder
	b.WriteString("# Tailarr configuration\n")
	// Always redact userinfo when displaying/saving so tokens cannot leak via `config show`.
	fmt.Fprintf(&b, "TAILARR_REPO_URL=%s\n", names.RedactRepoURL(cfg.RepoURL))
	fmt.Fprintf(&b, "TAILARR_REPO_PATH=%s\n", cfg.RepoPath)
	fmt.Fprintf(&b, "TAILARR_DEPLOY_PATH=%s\n", cfg.DeployPath)
	fmt.Fprintf(&b, "TAILARR_LOG_PATH=%s\n", cfg.LogPath)
	fmt.Fprintf(&b, "TAILARR_AUTHKEYS_PATH=%s\n", cfg.AuthkeysPath)
	fmt.Fprintf(&b, "TAILARR_REPO_REF=%s\n", cfg.RepoRef)
	fmt.Fprintf(&b, "TAILARR_LOG_MAX_BYTES=%d\n", cfg.LogMaxBytes)
	return b.String()
}

// BackupRoot returns the backup directory under the deploy path.
func (c Config) BackupRoot() string {
	return strings.TrimRight(c.DeployPath, string(os.PathSeparator)) + string(os.PathSeparator) + BackupDirName
}

// String returns a multi-line non-secret dump for display.
func (c Config) String() string {
	return format(c)
}
