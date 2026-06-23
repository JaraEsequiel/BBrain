// Package vault relocates a brain directory. Stdlib only; the caller rebuilds the
// derived index after a move (it stores absolute paths that a move invalidates).
package vault

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Move relocates the tree at src to dest. It refuses when dest equals src or when
// dest already exists as a non-empty directory. It prefers an atomic os.Rename and
// falls back to a copy-then-remove when src and dest are on different filesystems;
// on a copy failure it removes the partial destination and leaves src intact.
func Move(src, dest string) error {
	srcInfo, err := os.Stat(src)
	if err != nil {
		return err
	}
	if !srcInfo.IsDir() {
		return fmt.Errorf("vault: source %q is not a directory", src)
	}
	absSrc, err := filepath.Abs(src)
	if err != nil {
		return err
	}
	absDest, err := filepath.Abs(dest)
	if err != nil {
		return err
	}
	if absSrc == absDest {
		return fmt.Errorf("vault: destination equals source")
	}
	if within(absSrc, absDest) || within(absDest, absSrc) {
		return fmt.Errorf("vault: destination %q overlaps the source %q", dest, src)
	}
	if nonEmptyDir(absDest) {
		return fmt.Errorf("vault: destination %q already exists and is not empty", dest)
	}
	if err := os.MkdirAll(filepath.Dir(absDest), 0o755); err != nil {
		return err
	}
	// Fast path: an atomic rename within one filesystem. Any rename error
	// (cross-device EXDEV, etc.) falls through to the copy fallback below.
	if err := os.Rename(absSrc, absDest); err == nil {
		return nil
	}
	// Fallback: copy the tree, then remove the source only on success.
	if err := copyTree(absSrc, absDest); err != nil {
		if rmErr := os.RemoveAll(absDest); rmErr != nil {
			return fmt.Errorf("vault: copy failed (%w); partial destination left at %s: %v", err, absDest, rmErr)
		}
		return err
	}
	return os.RemoveAll(absSrc)
}

// within reports whether child is the same as, or nested under, parent.
func within(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

// nonEmptyDir reports whether path is a directory containing at least one entry.
func nonEmptyDir(path string) bool {
	entries, err := os.ReadDir(path)
	return err == nil && len(entries) > 0
}

// copyTree copies the directory tree at src into dest, preserving file modes.
func copyTree(src, dest string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dest, rel)
		if d.IsDir() {
			info, err := d.Info()
			if err != nil {
				return err
			}
			return os.MkdirAll(target, info.Mode().Perm())
		}
		return copyFile(p, target, d)
	})
}

func copyFile(srcPath, destPath string, d fs.DirEntry) error {
	info, err := d.Info()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return err
	}
	in, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
