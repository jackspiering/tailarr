// Package version holds the application SemVer string.
// Override at link time:
//
//	go build -ldflags "-X github.com/jackspiering/tailarr/internal/version.Version=1.2.3"
package version

// Version is the current Tailarr release (SemVer MAJOR.MINOR.PATCH).
var Version = "0.2.0"

// Name is the binary product name.
const Name = "tailarr"
