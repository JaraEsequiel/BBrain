package fact

import (
	"strings"
	"testing"
)

func TestSlug(t *testing.T) {
	cases := map[string]string{
		"Use JWT with refresh tokens for auth": "use-jwt-with-refresh-tokens-for-auth",
		"  Trim & Symbols!! ":                  "trim-symbols",
		"Postgres vs MySQL":                    "postgres-vs-mysql",
		"":                                     "untitled",
	}
	for in, want := range cases {
		if got := Slug(in); got != want {
			t.Errorf("Slug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNewID(t *testing.T) {
	got := NewID("2026-06-22", "Use JWT for auth")
	want := "2026-06-22-use-jwt-for-auth"
	if got != want {
		t.Fatalf("NewID = %q, want %q", got, want)
	}
}

func TestMarshalParseRoundTrip(t *testing.T) {
	f := Fact{
		ID:       "2026-06-22-use-jwt-for-auth",
		Type:     "decision",
		Scope:    "project",
		Project:  "bbrain",
		TopicKey: "architecture/auth-model",
		Tags:     []string{"auth", "security"},
		Links: []Link{
			{Target: "[[postgres-vs-mysql]]", Relation: "supersedes", Why: "replaces prior DB choice"},
		},
		CreatedAt:     "2026-06-22T17:57:00Z",
		UpdatedAt:     "2026-06-22T17:57:00Z",
		RevisionCount: 1,
		Title:         "Use JWT with refresh tokens for auth",
		Body:          "**What:** use JWT\n**Why:** stateless",
	}

	got, err := Parse(Marshal(f))
	if err != nil {
		t.Fatalf("Parse(Marshal(f)) error: %v", err)
	}
	if got.Title != f.Title {
		t.Errorf("Title = %q, want %q", got.Title, f.Title)
	}
	if got.Body != f.Body {
		t.Errorf("Body = %q, want %q", got.Body, f.Body)
	}
	if got.TopicKey != f.TopicKey || got.Type != f.Type || got.Scope != f.Scope {
		t.Errorf("frontmatter scalars mismatch: %+v", got)
	}
	if len(got.Links) != 1 || got.Links[0].Why != "replaces prior DB choice" {
		t.Errorf("Links not round-tripped: %+v", got.Links)
	}
	if len(got.Tags) != 2 || got.Tags[0] != "auth" {
		t.Errorf("Tags not round-tripped: %+v", got.Tags)
	}
}

func TestParseRejectsMissingFrontmatter(t *testing.T) {
	if _, err := Parse("# Just a title\n\nno frontmatter"); err == nil {
		t.Fatal("Parse should error when frontmatter delimiters are missing")
	}
}

func TestValidRelation(t *testing.T) {
	for _, r := range []string{"relates", "depends-on", "conflicts-with", "supersedes", "scoped", "compatible"} {
		if !ValidRelation(r) {
			t.Errorf("ValidRelation(%q) = false, want true", r)
		}
	}
	if ValidRelation("frobnicates") {
		t.Error("ValidRelation(frobnicates) = true, want false")
	}
	if ValidRelation("") {
		t.Error("ValidRelation(\"\") = true, want false")
	}
}

func TestLinkTargetRoundTrip(t *testing.T) {
	id := "2026-06-22-postgres-vs-mysql"
	if got := FormatTarget(id); got != "[[2026-06-22-postgres-vs-mysql]]" {
		t.Fatalf("FormatTarget = %q", got)
	}
	if got := LinkTargetID(FormatTarget(id)); got != id {
		t.Fatalf("LinkTargetID(FormatTarget(id)) = %q, want %q", got, id)
	}
	// Tolerates a bare slug and surrounding whitespace.
	if got := LinkTargetID("  [[session-model]] "); got != "session-model" {
		t.Fatalf("LinkTargetID(messy) = %q, want %q", got, "session-model")
	}
}

func TestPinnedRoundTrip(t *testing.T) {
	f := Fact{
		ID: "x", Type: "about-me", Scope: "global",
		CreatedAt: "2026-06-24T00:00:00Z", UpdatedAt: "2026-06-24T00:00:00Z",
		RevisionCount: 1, Pinned: true,
		Title: "About", Body: "hi",
	}
	got, err := Parse(Marshal(f))
	if err != nil {
		t.Fatal(err)
	}
	if !got.Pinned {
		t.Fatalf("pinned lost in round-trip: %+v", got)
	}
}

func TestPinnedOmittedWhenFalse(t *testing.T) {
	out := Marshal(Fact{ID: "x", Type: "decision", Title: "T", Body: "b"})
	if strings.Contains(out, "pinned") {
		t.Fatalf("pinned:false must not appear on disk:\n%s", out)
	}
}

func TestValidID(t *testing.T) {
	for _, g := range []string{"2026-06-23-use-jwt", "f1", "a-b-c"} {
		if !ValidID(g) {
			t.Fatalf("ValidID(%q)=false, want true", g)
		}
	}
	for _, b := range []string{"", "../etc", "a/b", "a..b", "A", "a_b", "a b", "..", "/etc/passwd"} {
		if ValidID(b) {
			t.Fatalf("ValidID(%q)=true, want false", b)
		}
	}
}
