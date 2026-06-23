package store

import (
	"testing"
	"time"

	"bbrain/internal/brain"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	b := brain.New(t.TempDir())
	if err := b.Init(); err != nil {
		t.Fatal(err)
	}
	s := New(b)
	base := time.Date(2026, 6, 22, 12, 0, 0, 0, time.UTC)
	s.Now = func() time.Time { return base }
	return s
}

func TestSaveCreatesFactFile(t *testing.T) {
	s := newTestStore(t)
	f, err := s.Save(SaveInput{
		Type: "decision", Title: "Use JWT for auth", Body: "**What:** jwt",
		Project: "bbrain", Scope: "project",
	})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if f.ID != "2026-06-22-use-jwt-for-auth" {
		t.Fatalf("ID = %q", f.ID)
	}
	facts, err := s.ListFacts()
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) != 1 || facts[0].Title != "Use JWT for auth" {
		t.Fatalf("ListFacts = %+v", facts)
	}
}

func TestSaveWithTopicKeyUpserts(t *testing.T) {
	s := newTestStore(t)
	first, _ := s.Save(SaveInput{
		Type: "architecture", Title: "Auth model", Body: "v1",
		Project: "bbrain", Scope: "project", TopicKey: "architecture/auth-model",
	})
	// Advance time so the dedup window does not swallow the second save.
	s.Now = func() time.Time { return time.Date(2026, 6, 22, 13, 0, 0, 0, time.UTC) }
	second, err := s.Save(SaveInput{
		Type: "architecture", Title: "Auth model", Body: "v2 — now stateless",
		Project: "bbrain", Scope: "project", TopicKey: "architecture/auth-model",
	})
	if err != nil {
		t.Fatalf("second Save: %v", err)
	}
	if second.ID != first.ID {
		t.Fatalf("upsert should reuse id: first=%q second=%q", first.ID, second.ID)
	}
	if second.RevisionCount != 2 {
		t.Fatalf("RevisionCount = %d, want 2", second.RevisionCount)
	}
	facts, _ := s.ListFacts()
	if len(facts) != 1 {
		t.Fatalf("upsert must not add a new file: got %d facts", len(facts))
	}
	if facts[0].Body != "v2 — now stateless" {
		t.Fatalf("body not updated: %q", facts[0].Body)
	}
}

func TestSaveDedupesIdenticalWithinWindow(t *testing.T) {
	s := newTestStore(t)
	in := SaveInput{Type: "bugfix", Title: "Nil panic", Body: "guard nil",
		Project: "bbrain", Scope: "project"}
	if _, err := s.Save(in); err != nil {
		t.Fatal(err)
	}
	// Same content, 5 minutes later (inside the 15-min window): no new file.
	s.Now = func() time.Time { return time.Date(2026, 6, 22, 12, 5, 0, 0, time.UTC) }
	if _, err := s.Save(in); err != nil {
		t.Fatal(err)
	}
	facts, _ := s.ListFacts()
	if len(facts) != 1 {
		t.Fatalf("dedup failed: got %d facts, want 1", len(facts))
	}
}
