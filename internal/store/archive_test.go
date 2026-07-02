package store

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func saveOne(t *testing.T, s *Store, title string, pinned bool) string {
	t.Helper()
	f, err := s.Save(SaveInput{
		Type: "decision", Title: title, Body: "**What:** " + title,
		Project: "bbrain", Scope: "project", Pinned: pinned,
	})
	if err != nil {
		t.Fatal(err)
	}
	return f.ID
}

func TestArchiveDirPathAndNotCreatedByInit(t *testing.T) {
	s := newTestStore(t)
	want := filepath.Join(s.Brain.Root, "raws", "archive")
	if got := s.Brain.ArchiveDir(); got != want {
		t.Fatalf("ArchiveDir() = %q, want %q", got, want)
	}
	if _, err := os.Stat(want); !os.IsNotExist(err) {
		t.Fatalf("Init must not create raws/archive; stat err = %v", err)
	}
}

func TestArchiveMovesFactToArchiveTier(t *testing.T) {
	s := newTestStore(t)
	id := saveOne(t, s, "Move me", false)

	f, err := s.Archive(id)
	if err != nil {
		t.Fatalf("Archive: %v", err)
	}
	if f.ID != id {
		t.Fatalf("Archive returned ID %q, want %q", f.ID, id)
	}
	if _, err := os.Stat(filepath.Join(s.Brain.FactsDir(), id+".md")); !os.IsNotExist(err) {
		t.Fatalf("active file should be gone; stat err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(s.Brain.ArchiveDir(), id+".md")); err != nil {
		t.Fatalf("archived file missing: %v", err)
	}

	// Gone from the active tier, visible in the archive tier.
	if _, ok, err := s.Get(id); err != nil || ok {
		t.Fatalf("Get after archive = ok=%v err=%v, want ok=false err=nil", ok, err)
	}
	got, ok, err := s.GetArchived(id)
	if err != nil || !ok {
		t.Fatalf("GetArchived = ok=%v err=%v", ok, err)
	}
	if got.Title != "Move me" {
		t.Fatalf("GetArchived Title = %q", got.Title)
	}
	archived, err := s.ListArchived()
	if err != nil {
		t.Fatal(err)
	}
	if len(archived) != 1 || archived[0].ID != id {
		t.Fatalf("ListArchived = %+v", archived)
	}
}

func TestArchiveRejectsInvalidID(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Archive("NOT a valid id!"); err == nil {
		t.Fatal("Archive with invalid id must error")
	}
}

func TestArchiveRejectsMissingFact(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Archive("2026-06-22-nope"); err == nil {
		t.Fatal("Archive of a missing fact must error")
	}
}

func TestArchiveRejectsAlreadyArchived(t *testing.T) {
	s := newTestStore(t)
	id := saveOne(t, s, "Twice", false)
	if _, err := s.Archive(id); err != nil {
		t.Fatal(err)
	}
	_, err := s.Archive(id)
	if err == nil {
		t.Fatal("second Archive must error")
	}
	if !strings.Contains(err.Error(), "already archived") {
		t.Fatalf("error should say already archived, got: %v", err)
	}
}

func TestArchiveRejectsPinned(t *testing.T) {
	s := newTestStore(t)
	id := saveOne(t, s, "Keep me", true)
	if _, err := s.Archive(id); err == nil {
		t.Fatal("Archive of a pinned fact must error")
	}
	if _, ok, _ := s.Get(id); !ok {
		t.Fatal("pinned fact must stay in the active tier")
	}
}

func TestArchiveRejectsWhenDestinationExists(t *testing.T) {
	s := newTestStore(t)
	id := saveOne(t, s, "Collide", false)
	if err := os.MkdirAll(s.Brain.ArchiveDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(s.Brain.ArchiveDir(), id+".md")
	if err := os.WriteFile(dst, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Archive(id); err == nil {
		t.Fatal("Archive must not overwrite an existing archived file")
	}
	if data, _ := os.ReadFile(dst); string(data) != "stale" {
		t.Fatal("existing archived file was clobbered")
	}
}

func TestUnarchiveRoundTripIsByteIdentical(t *testing.T) {
	s := newTestStore(t)
	id := saveOne(t, s, "Round trip", false)
	activePath := filepath.Join(s.Brain.FactsDir(), id+".md")
	orig, err := os.ReadFile(activePath)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := s.Archive(id); err != nil {
		t.Fatal(err)
	}
	f, err := s.Unarchive(id)
	if err != nil {
		t.Fatalf("Unarchive: %v", err)
	}
	if f.ID != id {
		t.Fatalf("Unarchive returned ID %q", f.ID)
	}
	after, err := os.ReadFile(activePath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(orig, after) {
		t.Fatalf("round trip changed bytes:\norig:  %q\nafter: %q", orig, after)
	}
	if _, err := os.Stat(filepath.Join(s.Brain.ArchiveDir(), id+".md")); !os.IsNotExist(err) {
		t.Fatalf("archived copy should be gone; stat err = %v", err)
	}
}

func TestUnarchiveRejectsWhenActiveExists(t *testing.T) {
	s := newTestStore(t)
	id := saveOne(t, s, "Guarded", false)
	if _, err := s.Archive(id); err != nil {
		t.Fatal(err)
	}
	activePath := filepath.Join(s.Brain.FactsDir(), id+".md")
	if err := os.WriteFile(activePath, []byte("newer active"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Unarchive(id); err == nil {
		t.Fatal("Unarchive must never overwrite an active fact")
	}
	if data, _ := os.ReadFile(activePath); string(data) != "newer active" {
		t.Fatal("active file was clobbered")
	}
}

func TestUnarchiveRejectsMissingArchivedFact(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Unarchive("2026-06-22-nope"); err == nil {
		t.Fatal("Unarchive of an id not in the archive must error")
	}
}

func TestListArchivedMissingDirIsNilNil(t *testing.T) {
	s := newTestStore(t)
	facts, err := s.ListArchived()
	if err != nil || facts != nil {
		t.Fatalf("ListArchived on absent dir = %v, %v; want nil, nil", facts, err)
	}
}

func TestGetArchivedInvalidIDAndAbsent(t *testing.T) {
	s := newTestStore(t)
	if _, _, err := s.GetArchived("NOT valid"); err == nil {
		t.Fatal("GetArchived with invalid id must error")
	}
	if _, ok, err := s.GetArchived("2026-06-22-nope"); err != nil || ok {
		t.Fatalf("GetArchived absent = ok=%v err=%v, want ok=false err=nil", ok, err)
	}
}
