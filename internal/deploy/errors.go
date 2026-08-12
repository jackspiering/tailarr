package deploy

import "errors"

// Sentinel errors for lifecycle operations. CLI maps these via errors.Is.
var (
	// ErrAlreadyDeployed is returned when deploy finds an existing service without --force.
	ErrAlreadyDeployed = errors.New("service already deployed")
	// ErrNotDeployed is returned when a lifecycle op targets a missing deployment.
	ErrNotDeployed = errors.New("service not deployed")
	// ErrNotManaged is returned when a lifecycle op targets a directory without a Tailarr marker.
	ErrNotManaged = errors.New("deployment is not managed by Tailarr")
	// ErrNoCompose is returned when a deploy directory has no compose file.
	ErrNoCompose = errors.New("deployment has no compose file")
	// ErrEmptyAuthkey is returned when TS_AUTHKEY is required but empty after merge.
	ErrEmptyAuthkey = errors.New("TS_AUTHKEY is empty; set it in .env, or store an auth key from the Authkeys menu")
	// ErrComposeFailed is returned when docker compose fails (wrapped with detail).
	ErrComposeFailed = errors.New("docker compose failed")
	// ErrSymlink is returned when a path is or contains a symlink that is refused.
	ErrSymlink = errors.New("symlink refused")
)
