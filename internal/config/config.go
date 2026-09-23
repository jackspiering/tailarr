// Package config loads and saves plain-text KEY=VALUE Tailarr configuration.
// User files are never source'd or eval'd.
package config

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
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
	LogMaxBytes  int64
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
	return applyEnv(cfg)
}

// LoadFile returns the defaults overlaid with the config file at path, without
// TAILARR_* environment overrides. Config edits start from it, so a one-off
// environment override is never written to the file.
func LoadFile(path string) (Config, error) {
	cfg := Default()
	cfg.ConfigPath = path
	return cfg, loadFile(&cfg, path)
}

// ApplyEnv applies TAILARR_* environment overrides to cfg.
func ApplyEnv(cfg *Config) error {
	return applyEnv(cfg)
}

// EnvOverrides lists the TAILARR_* settings that the environment overrides.
func EnvOverrides() []string {
	var keys []string
	for _, k := range []string{
		"TAILARR_REPO_URL", "TAILARR_REPO_PATH", "TAILARR_DEPLOY_PATH",
		"TAILARR_LOG_PATH", "TAILARR_AUTHKEYS_PATH", "TAILARR_LOG_MAX_BYTES",
	} {
		if strings.TrimSpace(os.Getenv(k)) != "" {
			keys = append(keys, k)
		}
	}
	return keys
}

func loadFile(cfg *Config, path string) error {
	if err := requireAbsPath("TAILARR_CONFIG_PATH", path); err != nil {
		return err
	}
	if err := paths.RefuseSymlinkAncestry(filepath.Dir(path)); err != nil {
		return fmt.Errorf("config directory: %w", err)
	}
	if paths.IsSymlink(path) {
		return fmt.Errorf("config file must not be a symlink: %s", path)
	}
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
		line := sc.Text()
		// Strip a UTF-8 BOM on every line so a copy-paste artifact does not
		// silently drop a key.
		line = strings.TrimPrefix(line, "\ufeff")
		line = strings.TrimSpace(line)
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
			if err := names.ValidateRepoURL(strings.TrimSpace(value)); err != nil {
				return err
			}
			cfg.RepoURL = strings.TrimSpace(value)
		case "TAILARR_REPO_PATH":
			if err := setOptionalAbsPath(&cfg.RepoPath, key, value); err != nil {
				return err
			}
		case "TAILARR_DEPLOY_PATH":
			if err := setOptionalAbsPath(&cfg.DeployPath, key, value); err != nil {
				return err
			}
		case "TAILARR_LOG_PATH":
			if err := setOptionalAbsPath(&cfg.LogPath, key, value); err != nil {
				return err
			}
		case "TAILARR_AUTHKEYS_PATH":
			if err := setOptionalAbsPath(&cfg.AuthkeysPath, key, value); err != nil {
				return err
			}
		case "TAILARR_LOG_MAX_BYTES":
			n, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
			if err != nil || n <= 0 {
				return fmt.Errorf("invalid TAILARR_LOG_MAX_BYTES %q: want a positive integer", strings.TrimSpace(value))
			}
			cfg.LogMaxBytes = n
		}
	}
	return sc.Err()
}

func applyEnv(cfg *Config) error {
	if v := os.Getenv("TAILARR_REPO_URL"); v != "" {
		v = strings.TrimSpace(v)
		if err := names.ValidateRepoURL(v); err != nil {
			return err
		}
		cfg.RepoURL = v
	}
	if v := strings.TrimSpace(os.Getenv("TAILARR_REPO_PATH")); v != "" {
		if err := requireAbsPath("TAILARR_REPO_PATH", v); err != nil {
			return err
		}
		cfg.RepoPath = v
	}
	if v := strings.TrimSpace(os.Getenv("TAILARR_DEPLOY_PATH")); v != "" {
		if err := requireAbsPath("TAILARR_DEPLOY_PATH", v); err != nil {
			return err
		}
		cfg.DeployPath = v
	}
	if v := strings.TrimSpace(os.Getenv("TAILARR_LOG_PATH")); v != "" {
		if err := requireAbsPath("TAILARR_LOG_PATH", v); err != nil {
			return err
		}
		cfg.LogPath = v
	}
	if v := strings.TrimSpace(os.Getenv("TAILARR_AUTHKEYS_PATH")); v != "" {
		if err := requireAbsPath("TAILARR_AUTHKEYS_PATH", v); err != nil {
			return err
		}
		cfg.AuthkeysPath = v
	}
	if v := os.Getenv("TAILARR_LOG_MAX_BYTES"); v != "" {
		value := strings.TrimSpace(v)
		n, err := strconv.ParseInt(value, 10, 64)
		if err != nil || n <= 0 {
			return fmt.Errorf("invalid TAILARR_LOG_MAX_BYTES %q: want a positive integer", value)
		}
		cfg.LogMaxBytes = n
	}

	if v := os.Getenv("TAILARR_ASSUME_YES"); v == "1" || strings.EqualFold(v, "true") {
		cfg.AssumeYes = true
	}
	return nil
}

// Save writes the config atomically as plain KEY=VALUE (mode 0600).
// Restrictive mode because RepoURL or paths could be sensitive in some setups.
func Save(cfg Config) error {
	if err := requireAbsPath("TAILARR_CONFIG_PATH", cfg.ConfigPath); err != nil {
		return err
	}
	if err := requireStoredPaths(cfg); err != nil {
		return err
	}
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
	fmt.Fprintf(&b, "TAILARR_LOG_MAX_BYTES=%d\n", cfg.LogMaxBytes)
	return b.String()
}

// String returns a multi-line non-secret dump for display.
func (c Config) String() string {
	return format(c)
}

// setOptionalAbsPath trims a file value. Empty values keep the existing
// default. Non-absolute values are rejected so a path cannot follow the
// process working directory.
func setOptionalAbsPath(dst *string, key, value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	if err := requireAbsPath(key, value); err != nil {
		return err
	}
	*dst = value
	return nil
}

func requireStoredPaths(cfg Config) error {
	pairs := []struct {
		key, value string
	}{
		{"TAILARR_REPO_PATH", cfg.RepoPath},
		{"TAILARR_DEPLOY_PATH", cfg.DeployPath},
		{"TAILARR_LOG_PATH", cfg.LogPath},
		{"TAILARR_AUTHKEYS_PATH", cfg.AuthkeysPath},
	}
	for _, p := range pairs {
		if err := requireAbsPath(p.key, p.value); err != nil {
			return err
		}
	}
	return nil
}

func requireAbsPath(key, value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return fmt.Errorf("%s must not be empty", key)
	}
	if !filepath.IsAbs(value) {
		return fmt.Errorf("%s must be an absolute path: %q", key, value)
	}
	return nil
}
