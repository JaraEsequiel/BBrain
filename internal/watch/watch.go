// Package watch detects changes to a brain's raw facts so the derived index can
// be rebuilt. Stdlib only; no filesystem-notification dependency.
package watch

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// FactsFingerprint returns a stable hash of every .md under dir (relpath + size +
// modtime). A missing dir yields ("", nil), so a watch loop treats "no facts yet"
// as a stable state rather than an error.
func FactsFingerprint(dir string) (string, error) {
	var lines []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".md") {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		lines = append(lines, fmt.Sprintf("%s\x00%d\x00%d", filepath.ToSlash(rel), info.Size(), info.ModTime().UnixNano()))
		return nil
	})
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	sort.Strings(lines)
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(sum[:]), nil
}
