package vault

import (
	"os"
	"path/filepath"
	"testing"
)

func mustMkdir(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
}

func mustWrite(t *testing.T, p, body string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestMoveRelocatesTree(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "src")
	mustMkdir(t, filepath.Join(src, "raws", "facts"))
	mustWrite(t, filepath.Join(src, "raws", "facts", "a.md"), "alpha")
	mustWrite(t, filepath.Join(src, "CLAUDE.md"), "doc")
	dest := filepath.Join(root, "dest")

	if err := Move(src, dest); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Fatal("source still exists after move")
	}
	b, err := os.ReadFile(filepath.Join(dest, "raws", "facts", "a.md"))
	if err != nil || string(b) != "alpha" {
		t.Fatalf("moved file = %q, %v", b, err)
	}
	if _, err := os.Stat(filepath.Join(dest, "CLAUDE.md")); err != nil {
		t.Fatalf("CLAUDE.md not moved: %v", err)
	}
}

func TestMoveRefusesSameAndNonEmptyDest(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "src")
	mustMkdir(t, src)
	if err := Move(src, src); err == nil {
		t.Fatal("Move should refuse dest == src")
	}
	dest := filepath.Join(root, "dest")
	mustMkdir(t, dest)
	mustWrite(t, filepath.Join(dest, "x"), "occupied")
	if err := Move(src, dest); err == nil {
		t.Fatal("Move should refuse a non-empty destination")
	}
}

func TestCopyTreePreservesContentAndMode(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "src")
	mustMkdir(t, filepath.Join(src, "sub"))
	mustWrite(t, filepath.Join(src, "sub", "f.sh"), "#!/bin/sh\n")
	if err := os.Chmod(filepath.Join(src, "sub", "f.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(root, "copy")
	if err := copyTree(src, dest); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dest, "sub", "f.sh"))
	if err != nil || string(b) != "#!/bin/sh\n" {
		t.Fatalf("copied content = %q, %v", b, err)
	}
	info, err := os.Stat(filepath.Join(dest, "sub", "f.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o100 == 0 {
		t.Fatalf("mode not preserved: %v", info.Mode())
	}
}

func TestMoveMissingSource(t *testing.T) {
	root := t.TempDir()
	if err := Move(filepath.Join(root, "nope"), filepath.Join(root, "dest")); err == nil {
		t.Fatal("Move should error on missing source")
	}
}
