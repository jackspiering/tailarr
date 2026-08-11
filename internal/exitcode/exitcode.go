// Package exitcode defines sysexits-style process exit codes used by Tailarr.
package exitcode

const (
	// OK is success.
	OK = 0
	// Usage is invalid CLI usage or bad arguments (EX_USAGE).
	Usage = 64
	// NotFound is a missing path or service (EX_NOTFOUND).
	NotFound = 65
	// Canceled is user cancellation (US spelling for misspell/locale).
	Canceled = 66
	// Unsafe is a path or symlink safety violation.
	Unsafe = 67
	// Docker is Docker/Compose unavailable.
	Docker = 69
	// Health is a failed health or readiness check.
	Health = 70
	// Perm is a permission or lock failure.
	Perm = 77
)
