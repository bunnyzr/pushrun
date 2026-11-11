// Package fsutil holds the small filesystem helpers shared across pushrun's
// internal packages: atomic file writes and the common name validation rule.
package fsutil

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// WriteFileAtomic writes data to path via a temporary file in the same
// directory followed by a rename, so readers never see a partial file.
func WriteFileAtomic(path string, data []byte, perm fs.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.RemoveAll(tmpName) }()

	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// ValidName reports whether s is a safe single path segment: non-empty, no
// slash or backslash, and no leading dot.
func ValidName(s string) bool {
	return s != "" && !strings.ContainsAny(s, `/\`) && !strings.HasPrefix(s, ".")
}
