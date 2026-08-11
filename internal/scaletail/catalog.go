// Package scaletail discovers services in a local ScaleTail-like tree and
// manages optional git clone/pull of the upstream repository.
package scaletail

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/jackspiering/tailarr/internal/security/names"
	"github.com/jackspiering/tailarr/internal/security/paths"
)

// ComposeCandidates is the discovery order for Compose files.
var ComposeCandidates = []string{
	"compose.yaml",
	"compose.yml",
	"docker-compose.yml",
	"docker-compose.yaml",
}

// Service is a catalog entry.
type Service struct {
	Name        string
	Dir         string
	ComposeFile string
	EnvFile     string
}

// ComposeFileIn returns the first non-symlink compose file in dir.
func ComposeFileIn(dir string) (string, bool) {
	for _, name := range ComposeCandidates {
		p := filepath.Join(dir, name)
		if paths.IsSymlink(p) {
			continue
		}
		info, err := os.Stat(p)
		if err != nil || info.IsDir() {
			continue
		}
		return p, true
	}
	return "", false
}

// HasComposeFile reports whether dir has a usable compose file.
func HasComposeFile(dir string) bool {
	_, ok := ComposeFileIn(dir)
	return ok
}

// ListAvailable scans repoPath/services for valid service directories.
// Requirements: valid name, not a symlink, compose file present, .env present.
func ListAvailable(repoPath string) ([]Service, error) {
	servicesDir := filepath.Join(repoPath, "services")
	if paths.IsSymlink(servicesDir) {
		return nil, fmt.Errorf("services directory must not be a symlink: %s", servicesDir)
	}
	info, err := os.Stat(servicesDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("services directory not found: %s", servicesDir)
		}
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("services path is not a directory: %s", servicesDir)
	}

	entries, err := os.ReadDir(servicesDir)
	if err != nil {
		return nil, err
	}
	var out []Service
	for _, e := range entries {
		name := e.Name()
		if !names.ValidServiceName(name) {
			continue
		}
		dir := filepath.Join(servicesDir, name)
		if paths.IsSymlink(dir) {
			continue
		}
		// Use Lstat via Type when possible; re-check directory.
		fi, err := os.Lstat(dir)
		if err != nil || !fi.IsDir() {
			continue
		}
		compose, ok := ComposeFileIn(dir)
		if !ok {
			continue
		}
		envPath := filepath.Join(dir, ".env")
		if paths.IsSymlink(envPath) {
			continue
		}
		if st, err := os.Stat(envPath); err != nil || st.IsDir() {
			continue
		}
		out = append(out, Service{
			Name:        name,
			Dir:         dir,
			ComposeFile: compose,
			EnvFile:     envPath,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// ListDeployed scans deployPath for service directories with compose files.
func ListDeployed(deployPath string) ([]Service, error) {
	if paths.IsSymlink(deployPath) {
		return nil, fmt.Errorf("deployment root must not be a symlink: %s", deployPath)
	}
	info, err := os.Stat(deployPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("deploy path is not a directory: %s", deployPath)
	}

	entries, err := os.ReadDir(deployPath)
	if err != nil {
		return nil, err
	}
	var out []Service
	for _, e := range entries {
		name := e.Name()
		if name == ".tailarr_backups" || name == ".tailarr_locks" {
			continue
		}
		if !names.ValidServiceName(name) {
			continue
		}
		dir := filepath.Join(deployPath, name)
		if paths.IsSymlink(dir) {
			continue
		}
		fi, err := os.Lstat(dir)
		if err != nil || !fi.IsDir() {
			continue
		}
		compose, ok := ComposeFileIn(dir)
		if !ok {
			continue
		}
		out = append(out, Service{
			Name:        name,
			Dir:         dir,
			ComposeFile: compose,
			EnvFile:     filepath.Join(dir, ".env"),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
