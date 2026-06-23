package watch

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFactsFingerprintChangesAndIsStable(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("a.md", "alpha")
	fp1, err := FactsFingerprint(dir)
	if err != nil {
		t.Fatal(err)
	}
	if fp1 == "" {
		t.Fatal("fingerprint empty for non-empty dir")
	}
	// stable when nothing changes
	fp1b, _ := FactsFingerprint(dir)
	if fp1b != fp1 {
		t.Fatal("fingerprint not stable across calls")
	}
	// changes when a fact is added
	write("b.md", "beta")
	fp2, _ := FactsFingerprint(dir)
	if fp2 == fp1 {
		t.Fatal("fingerprint unchanged after adding a fact")
	}
	// changes when a fact's content changes (size differs)
	time.Sleep(5 * time.Millisecond)
	write("a.md", "alpha-modified-longer")
	fp3, _ := FactsFingerprint(dir)
	if fp3 == fp2 {
		t.Fatal("fingerprint unchanged after modifying a fact")
	}
	// non-.md files are ignored
	write("notes.txt", "ignored")
	fp4, _ := FactsFingerprint(dir)
	if fp4 != fp3 {
		t.Fatal("fingerprint changed for a non-.md file")
	}
}

func TestFactsFingerprintMissingDir(t *testing.T) {
	fp, err := FactsFingerprint(filepath.Join(t.TempDir(), "nope"))
	if err != nil || fp != "" {
		t.Fatalf("missing dir = %q, %v; want \"\", nil", fp, err)
	}
}
