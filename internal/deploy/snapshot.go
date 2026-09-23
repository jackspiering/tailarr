package deploy

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/jackspiering/tailarr/internal/config"
	"github.com/jackspiering/tailarr/internal/security/names"
	"github.com/jackspiering/tailarr/internal/security/paths"
)

// applySnapshot records the files Apply may change: template paths, .env,
// and the managed override. Container data is never copied or moved, so a
// failed Apply cannot pull a bind-mounted directory out from under a
// running container.
type applySnapshot struct {
	dest string
	// dir holds copies of the files that existed before Apply.
	dir string
	// saved lists paths (relative to dest) copied into dir.
	saved []string
	// created lists file paths that did not exist before Apply.
	created []string
	// createdDirs lists directories that did not exist, parents first.
	createdDirs []string
}

// snapshotManaged copies the files Apply may write under dest into a new
// backup directory under deployPath/.tailarr_backups.
func snapshotManaged(deployPath, service, templateDir, dest string) (*applySnapshot, error) {
	if err := names.ValidateServiceName(service); err != nil {
		return nil, err
	}
	root := filepath.Join(deployPath, config.BackupDirName)
	if err := paths.EnsureDirMode(root, "backup directory", 0o700); err != nil {
		return nil, err
	}
	dir, err := backupPathFor(root, service, time.Now().UTC().Format("20060102T150405Z"))
	if err != nil {
		return nil, err
	}
	if err := os.Mkdir(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create backup: %w", err)
	}
	s := &applySnapshot{dest: dest, dir: dir}

	var files []string
	err = filepath.Walk(templateDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(templateDir, path)
		if err != nil {
			return err
		}
		if rel == "." || rel == ".env" {
			return nil
		}
		if info.IsDir() {
			if _, err := os.Lstat(filepath.Join(dest, rel)); os.IsNotExist(err) {
				s.createdDirs = append(s.createdDirs, rel)
			} else if err != nil {
				return err
			}
			return nil
		}
		files = append(files, rel)
		return nil
	})
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}
	files = append(files, ".env", overrideFilename)
	for _, rel := range files {
		if err := s.save(rel); err != nil {
			_ = os.RemoveAll(dir)
			return nil, err
		}
	}
	_ = pruneBackups(root, service, 2)
	return s, nil
}

func (s *applySnapshot) save(rel string) error {
	src := filepath.Join(s.dest, rel)
	info, err := os.Lstat(src)
	switch {
	case os.IsNotExist(err):
		s.created = append(s.created, rel)
		return nil
	case err != nil:
		return err
	case info.Mode()&os.ModeSymlink != 0:
		return fmt.Errorf("%w: deployment file is a symlink: %s", ErrSymlink, src)
	case !info.Mode().IsRegular():
		// A directory where the template has a file: sync fails closed on
		// the type mismatch before writing it, so there is nothing to save.
		return nil
	}
	if err := copyRegular(src, filepath.Join(s.dir, rel), info); err != nil {
		return fmt.Errorf("back up %s: %w", rel, err)
	}
	s.saved = append(s.saved, rel)
	return nil
}

// restore writes the saved files back in place, removes files and
// directories that Apply created, and leaves everything else untouched.
func (s *applySnapshot) restore() error {
	var errs []error
	for _, rel := range s.saved {
		src := filepath.Join(s.dir, rel)
		dst := filepath.Join(s.dest, rel)
		if err := paths.RefuseSymlinkAncestry(filepath.Dir(dst)); err != nil {
			errs = append(errs, fmt.Errorf("%w: %w", ErrSymlink, err))
			continue
		}
		info, err := os.Lstat(src)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if err := copyRegular(src, dst, info); err != nil {
			errs = append(errs, fmt.Errorf("restore %s: %w", rel, err))
		}
	}
	for _, rel := range s.created {
		p := filepath.Join(s.dest, rel)
		if st, err := os.Lstat(p); err == nil && st.IsDir() {
			continue
		}
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			errs = append(errs, fmt.Errorf("remove new file %s: %w", rel, err))
		}
	}
	// Deepest first; a directory that now holds container data stays.
	for i := len(s.createdDirs) - 1; i >= 0; i-- {
		_ = os.Remove(filepath.Join(s.dest, s.createdDirs[i]))
	}
	return errors.Join(errs...)
}
