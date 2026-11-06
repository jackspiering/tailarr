package deploy

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"github.com/jackspiering/tailarr/internal/scaletail"
	"github.com/jackspiering/tailarr/internal/security/atomic"
	"github.com/jackspiering/tailarr/internal/security/paths"
)

// composeBaseFilename holds the catalog compose file as Tailarr last deployed
// it. Apply merges with it: the operator's edits are the changes from this
// file to the deployed one, and the catalog's are the changes from this file
// to the new template.
const composeBaseFilename = ".tailarr.compose.base.yaml"

// Compose plan kinds.
const (
	// planAdd writes the catalog file where the deployment has none.
	planAdd = "add"
	// planKeep leaves the deployed file as it is.
	planKeep = "keep"
	// planUpdate writes the catalog file over a file without edits.
	planUpdate = "update"
	// planMerge writes the catalog changes merged into the operator's edits.
	planMerge = "merge"
	// planReplace writes the catalog file over a file whose edits Tailarr
	// cannot tell apart from older catalog changes, because no base exists.
	planReplace = "replace"
	// planConflict means the edits and the catalog change the same lines.
	planConflict = "conflict"
)

// composePlan says what Apply does to the deployed compose file.
type composePlan struct {
	// file is the compose file name in the template and the deployment.
	file string
	kind string
	// data is the text to write. It is nil for planKeep and planConflict.
	data []byte
	// catalog is the template text, which becomes the new base.
	catalog []byte
	// mode is the template file mode.
	mode os.FileMode
	// settingsDiffer is set for planReplace when the deployed file differs
	// from the catalog in more than comments and blank lines.
	settingsDiffer bool
	// deployedMode is the mode of the deployed file, when it exists.
	deployedMode os.FileMode
	// edited is set for planKeep when the deployed file holds operator
	// edits that the plan keeps.
	edited bool
}

// planCompose decides how Apply updates the compose file in dest from the
// template in templateDir. It reads files without following symlinks.
func planCompose(templateDir, dest string) (composePlan, error) {
	p := composePlan{file: composeBaseName(templateDir)}
	tplPath := filepath.Join(templateDir, p.file)
	catalog, err := readManaged(tplPath)
	if err != nil {
		return p, fmt.Errorf("template %s: %w", p.file, err)
	}
	info, err := os.Lstat(tplPath)
	if err != nil {
		return p, err
	}
	p.catalog, p.mode = catalog, info.Mode().Perm()

	minePath := filepath.Join(dest, p.file)
	mine, err := readManaged(minePath)
	if err == nil {
		if st, serr := os.Lstat(minePath); serr == nil {
			p.deployedMode = st.Mode().Perm()
		}
	}
	switch {
	case errors.Is(err, os.ErrNotExist):
		p.kind, p.data = planAdd, catalog
		return p, nil
	case err != nil:
		return p, fmt.Errorf("deployed %s: %w", p.file, err)
	case bytes.Equal(mine, catalog):
		p.kind = planKeep
		return p, nil
	}

	base, err := readManaged(filepath.Join(dest, composeBaseFilename))
	switch {
	case errors.Is(err, os.ErrNotExist):
		p.kind, p.data = planReplace, catalog
		p.settingsDiffer = !bytes.Equal(composeSettings(mine), composeSettings(catalog))
		return p, nil
	case err != nil:
		return p, fmt.Errorf("%s: %w", composeBaseFilename, err)
	case bytes.Equal(base, catalog):
		// Only the operator changed the file.
		p.kind, p.edited = planKeep, true
		return p, nil
	case bytes.Equal(mine, base):
		p.kind, p.data = planUpdate, catalog
		return p, nil
	}

	merged, conflict, err := scaletail.MergeFile(mine, base, catalog)
	switch {
	case err != nil:
		return p, fmt.Errorf("%s: %w", p.file, err)
	case conflict:
		p.kind = planConflict
	case bytes.Equal(merged, mine):
		p.kind, p.edited = planKeep, true
	default:
		p.kind, p.data = planMerge, merged
	}
	return p, nil
}

// conflictError explains a planConflict and how to get past it.
func (p composePlan) conflictError(dest string) error {
	return fmt.Errorf("%w: your edits to %s and the catalog change the same lines; "+
		"undo your edit there and run Apply again, or delete %s to take the catalog file as is (Apply saves your file in its backup first)",
		ErrComposeConflict, filepath.Join(dest, p.file), filepath.Join(dest, composeBaseFilename))
}

// modeOnly reports whether the plan keeps the catalog text of the deployed
// file and only its mode differs from the template.
func (p composePlan) modeOnly() bool {
	return p.kind == planKeep && !p.edited && p.deployedMode != p.mode
}

// write applies the plan to dest and records the catalog text as the new
// base. The compose file is written in place, so its inode (and a single-file
// bind mount of it) survives. The caller has saved both files in the Apply
// snapshot, so a later failure rolls the base back with the compose file.
func (p composePlan) write(dest string) error {
	path := filepath.Join(dest, p.file)
	switch {
	case p.data != nil:
		if err := writeInPlace(path, bytes.NewReader(p.data), p.mode); err != nil {
			return err
		}
	case p.modeOnly():
		if err := paths.ChmodNoFollow(path, p.mode); err != nil {
			return err
		}
	}
	return writeComposeBase(dest, p.catalog)
}

// writeComposeBase records catalog as the base compose text in dest. It
// refuses a symlink at the path or in its parents, because containers can
// write into the deployment.
func writeComposeBase(dest string, catalog []byte) error {
	path := filepath.Join(dest, composeBaseFilename)
	if err := paths.RefuseSymlinkAncestry(dest); err != nil {
		return fmt.Errorf("%w: %w", ErrSymlink, err)
	}
	if paths.IsSymlink(path) {
		return fmt.Errorf("%w: deployment file is a symlink: %s", ErrSymlink, path)
	}
	return atomic.WriteFile(path, catalog, 0o644)
}

// readManaged reads a regular file of at most maxManagedFileBytes without
// following a final symlink or blocking on a FIFO.
func readManaged(path string) ([]byte, error) {
	data, truncated, err := paths.ReadRegularFile(path, maxManagedFileBytes)
	if err != nil {
		return nil, symlinkHint(err, path)
	}
	if truncated {
		return nil, fmt.Errorf("file is larger than %d bytes", maxManagedFileBytes)
	}
	return data, nil
}

var (
	composeCommentLineRE  = regexp.MustCompile(`(?m)^[ \t]*#.*$`)
	composeTrailCommentRE = regexp.MustCompile(`(?m)[ \t]+#.*$`)
	composeTrailSpaceRE   = regexp.MustCompile(`(?m)[ \t\r]+$`)
	composeBlankLinesRE   = regexp.MustCompile(`\n{2,}`)
)

// composeSettings returns compose text without comments, trailing spaces,
// and blank lines, so two files with the same settings compare equal.
func composeSettings(data []byte) []byte {
	s := composeCommentLineRE.ReplaceAll(data, nil)
	s = composeTrailCommentRE.ReplaceAll(s, nil)
	s = composeTrailSpaceRE.ReplaceAll(s, nil)
	s = composeBlankLinesRE.ReplaceAll(s, []byte("\n"))
	return bytes.Trim(s, "\n")
}

// reportComposePlan tells the operator what Apply did to the compose file.
// It names files only and never prints their contents.
func (m *Manager) reportComposePlan(service string, p composePlan, backupDir string) {
	var msg string
	switch p.kind {
	case planMerge:
		msg = fmt.Sprintf("%s: merged the catalog changes into your edits.", p.file)
	case planReplace:
		if !p.settingsDiffer {
			msg = fmt.Sprintf("%s: replaced with the catalog version (only comments or spacing differed).", p.file)
			break
		}
		msg = fmt.Sprintf("%s: replaced with the catalog version, because Tailarr has no record of the version you deployed. "+
			"If you edited this file, your previous version is in %s. Copy your edits back once, and later Applies keep them.",
			p.file, backupDir)
	default:
		return
	}
	m.log("apply %s: %s", service, msg)
	if m.UI != nil {
		m.UI.Printf("%s\n", msg)
	}
}
