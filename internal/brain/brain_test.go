package brain

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInitCreatesStructure(t *testing.T) {
	root := t.TempDir()
	b := New(root)
	if err := b.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	mustDir(t, b.FactsDir())
	mustDir(t, b.UserRawsDir())
	mustDir(t, b.WikiDir())
	mustFile(t, filepath.Join(root, "CLAUDE.md"))
	mustFile(t, filepath.Join(b.WikiDir(), "index.md"))
	mustFile(t, filepath.Join(b.WikiDir(), "log.md"))
}

func TestInitIsIdempotentAndPreservesContent(t *testing.T) {
	root := t.TempDir()
	b := New(root)
	if err := b.Init(); err != nil {
		t.Fatalf("first Init: %v", err)
	}
	claude := filepath.Join(root, "CLAUDE.md")
	if err := os.WriteFile(claude, []byte("EDITED BY USER"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := b.Init(); err != nil {
		t.Fatalf("second Init: %v", err)
	}
	got, _ := os.ReadFile(claude)
	if string(got) != "EDITED BY USER" {
		t.Fatalf("Init overwrote user-edited CLAUDE.md: %q", got)
	}
}

func TestPaths(t *testing.T) {
	b := New("/brains/x")
	if b.IndexPath() != filepath.Join("/brains/x", ".bbrain", "index.db") {
		t.Fatalf("IndexPath = %q", b.IndexPath())
	}
}

func mustDir(t *testing.T, p string) {
	t.Helper()
	info, err := os.Stat(p)
	if err != nil || !info.IsDir() {
		t.Fatalf("expected dir at %s (err: %v)", p, err)
	}
}

func mustFile(t *testing.T, p string) {
	t.Helper()
	info, err := os.Stat(p)
	if err != nil || info.IsDir() {
		t.Fatalf("expected file at %s (err: %v)", p, err)
	}
}
