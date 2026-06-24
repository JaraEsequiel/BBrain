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

func TestGetReturnsFactOrNotFound(t *testing.T) {
	s := newTestStore(t)
	if _, ok, err := s.Get("nope"); err != nil || ok {
		t.Fatalf("Get(missing) = ok=%v err=%v, want ok=false err=nil", ok, err)
	}
	f, _ := s.Save(SaveInput{Type: "decision", Title: "Use JWT", Body: "b",
		Project: "bbrain", Scope: "project"})
	got, ok, err := s.Get(f.ID)
	if err != nil || !ok {
		t.Fatalf("Get(existing) ok=%v err=%v", ok, err)
	}
	if got.Title != "Use JWT" {
		t.Fatalf("Get title = %q, want %q", got.Title, "Use JWT")
	}
}

func TestAddLinkWritesReasonedWikilink(t *testing.T) {
	s := newTestStore(t)
	src, _ := s.Save(SaveInput{Type: "architecture", Title: "Auth model", Body: "jwt",
		Project: "bbrain", Scope: "project"})
	// Advance time so the second save is distinct (and not deduped).
	s.Now = func() time.Time { return time.Date(2026, 6, 22, 13, 0, 0, 0, time.UTC) }
	dst, _ := s.Save(SaveInput{Type: "decision", Title: "Session storage", Body: "redis",
		Project: "bbrain", Scope: "project"})

	updated, err := s.AddLink(src.ID, dst.ID, "depends-on", "auth model assumes the session storage")
	if err != nil {
		t.Fatalf("AddLink: %v", err)
	}
	if len(updated.Links) != 1 {
		t.Fatalf("links = %+v, want 1", updated.Links)
	}
	l := updated.Links[0]
	if l.Target != "[["+dst.ID+"]]" || l.Relation != "depends-on" || l.Why == "" {
		t.Fatalf("link fields wrong: %+v", l)
	}
	// Persisted to disk, not just returned.
	reloaded, ok, _ := s.Get(src.ID)
	if !ok || len(reloaded.Links) != 1 || reloaded.Links[0].Relation != "depends-on" {
		t.Fatalf("link not persisted: ok=%v links=%+v", ok, reloaded.Links)
	}
}

func TestAddLinkUpsertsSameTarget(t *testing.T) {
	s := newTestStore(t)
	src, _ := s.Save(SaveInput{Type: "architecture", Title: "A", Body: "x", Project: "p", Scope: "project"})
	s.Now = func() time.Time { return time.Date(2026, 6, 22, 13, 0, 0, 0, time.UTC) }
	dst, _ := s.Save(SaveInput{Type: "decision", Title: "B", Body: "y", Project: "p", Scope: "project"})

	if _, err := s.AddLink(src.ID, dst.ID, "relates", "first reason"); err != nil {
		t.Fatal(err)
	}
	updated, err := s.AddLink(src.ID, dst.ID, "conflicts-with", "second reason")
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.Links) != 1 {
		t.Fatalf("re-linking the same target must not duplicate: %+v", updated.Links)
	}
	if updated.Links[0].Relation != "conflicts-with" || updated.Links[0].Why != "second reason" {
		t.Fatalf("link not updated in place: %+v", updated.Links[0])
	}
}

func TestAddLinkValidates(t *testing.T) {
	s := newTestStore(t)
	src, _ := s.Save(SaveInput{Type: "architecture", Title: "A", Body: "x", Project: "p", Scope: "project"})
	s.Now = func() time.Time { return time.Date(2026, 6, 22, 13, 0, 0, 0, time.UTC) }
	dst, _ := s.Save(SaveInput{Type: "decision", Title: "B", Body: "y", Project: "p", Scope: "project"})

	if _, err := s.AddLink(src.ID, dst.ID, "bogus-relation", "why"); err == nil {
		t.Fatal("AddLink should reject an invalid relation")
	}
	if _, err := s.AddLink(src.ID, dst.ID, "relates", ""); err == nil {
		t.Fatal("AddLink should require a non-empty why")
	}
	if _, err := s.AddLink(src.ID, "missing-fact", "relates", "why"); err == nil {
		t.Fatal("AddLink should reject a missing target fact")
	}
	if _, err := s.AddLink("missing-src", dst.ID, "relates", "why"); err == nil {
		t.Fatal("AddLink should reject a missing source fact")
	}
}

func TestAddLinkBumpsUpdatedAtNotRevisionCount(t *testing.T) {
	s := newTestStore(t)
	src, err := s.Save(SaveInput{Type: "architecture", Title: "A", Body: "x", Project: "p", Scope: "project"})
	if err != nil {
		t.Fatal(err)
	}
	// Advance time so the second save is a distinct file (not deduped).
	s.Now = func() time.Time { return time.Date(2026, 6, 22, 13, 0, 0, 0, time.UTC) }
	dst, err := s.Save(SaveInput{Type: "decision", Title: "B", Body: "y", Project: "p", Scope: "project"})
	if err != nil {
		t.Fatal(err)
	}

	// Link at a later, distinct timestamp so the bump is observable.
	linkTime := time.Date(2026, 6, 23, 9, 0, 0, 0, time.UTC)
	s.Now = func() time.Time { return linkTime }
	updated, err := s.AddLink(src.ID, dst.ID, "relates", "because")
	if err != nil {
		t.Fatalf("AddLink: %v", err)
	}

	// updated_at is bumped to the link time...
	if want := linkTime.Format(time.RFC3339); updated.UpdatedAt != want {
		t.Fatalf("updated_at = %q, want %q", updated.UpdatedAt, want)
	}
	// ...and it actually moved (the source was saved at the base time).
	if updated.UpdatedAt == src.UpdatedAt {
		t.Fatalf("updated_at was not bumped: still %q", updated.UpdatedAt)
	}
	// revision_count is unchanged — a link edit is not a content revision.
	if updated.RevisionCount != src.RevisionCount {
		t.Fatalf("revision_count = %d, want %d (unchanged)", updated.RevisionCount, src.RevisionCount)
	}

	// Both invariants survive a reload from disk.
	reloaded, ok, err := s.Get(src.ID)
	if err != nil || !ok {
		t.Fatalf("reload: ok=%v err=%v", ok, err)
	}
	if reloaded.UpdatedAt != updated.UpdatedAt {
		t.Fatalf("persisted updated_at = %q, want %q", reloaded.UpdatedAt, updated.UpdatedAt)
	}
	if reloaded.RevisionCount != src.RevisionCount {
		t.Fatalf("persisted revision_count = %d, want %d", reloaded.RevisionCount, src.RevisionCount)
	}
}

func TestRemoveLink(t *testing.T) {
	s := newTestStore(t)
	a, err := s.Save(SaveInput{Type: "decision", Title: "Alpha", Body: "a", Project: "p", Scope: "project"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Save(SaveInput{Type: "decision", Title: "Beta", Body: "b", Project: "p", Scope: "project"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddLink(a.ID, b.ID, "relates", "x"); err != nil {
		t.Fatal(err)
	}
	got, removed, err := s.RemoveLink(a.ID, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !removed {
		t.Fatal("removed should be true after removing an existing link")
	}
	if len(got.Links) != 0 {
		t.Fatalf("links after remove = %+v", got.Links)
	}
	// Removing a non-existent link is a no-op, not an error.
	before, _, _ := s.Get(a.ID)
	got2, removed2, err := s.RemoveLink(a.ID, "does-not-exist")
	if err != nil {
		t.Fatalf("no-op remove errored: %v", err)
	}
	if removed2 {
		t.Fatal("removed2 should be false for a no-op")
	}
	if got2.UpdatedAt != before.UpdatedAt {
		t.Fatalf("no-op bumped updated_at: before=%q after=%q", before.UpdatedAt, got2.UpdatedAt)
	}
}

func TestSavePinnedNewAndUpsert(t *testing.T) {
	s := newTestStore(t) // reuse this file's existing store constructor
	f, err := s.Save(SaveInput{Type: "about-me", Title: "About", Body: "v1",
		Scope: "global", TopicKey: "profile/about-me", Pinned: true})
	if err != nil {
		t.Fatal(err)
	}
	if !f.Pinned {
		t.Fatalf("new save lost pinned: %+v", f)
	}
	f2, err := s.Save(SaveInput{Type: "about-me", Title: "About", Body: "v2",
		Scope: "global", TopicKey: "profile/about-me", Pinned: true})
	if err != nil {
		t.Fatal(err)
	}
	if !f2.Pinned || f2.Body != "v2" || f2.ID != f.ID {
		t.Fatalf("upsert wrong: %+v", f2)
	}
}

func TestDelete(t *testing.T) {
	s := newTestStore(t)
	f, err := s.Save(SaveInput{Type: "decision", Title: "Doomed", Body: "x", Project: "p", Scope: "project"})
	if err != nil {
		t.Fatal(err)
	}
	deleted, err := s.Delete(f.ID)
	if err != nil || !deleted {
		t.Fatalf("Delete = %v, %v; want true, nil", deleted, err)
	}
	if _, ok, _ := s.Get(f.ID); ok {
		t.Fatal("fact still present after Delete")
	}
	// Deleting an absent fact is a no-op, not an error.
	if again, err := s.Delete(f.ID); err != nil || again {
		t.Fatalf("second Delete = %v, %v; want false, nil", again, err)
	}
}

func TestGetDeleteRejectUnsafeID(t *testing.T) {
	s := newTestStore(t)
	for _, id := range []string{"../../etc", "a/b", "..", "bad space"} {
		if _, _, err := s.Get(id); err == nil {
			t.Fatalf("Get(%q) should reject unsafe id", id)
		}
		if _, err := s.Delete(id); err == nil {
			t.Fatalf("Delete(%q) should reject unsafe id", id)
		}
	}
}
