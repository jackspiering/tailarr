//go:build !unix

package deploy

import "os"

// preserveOwner is a no-op where Unix ownership does not apply.
func preserveOwner(string, os.FileInfo) error { return nil }
