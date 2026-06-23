package fact

import "testing"

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
