package wiki

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/JaraEsequiel/BBrain/internal/fact"
)

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestParseResponse(t *testing.T) {
	pages, err := ParseResponse(`  {"pages":[{"slug":"a","category":"concepts","title":"A","sources":["f1"],"body":"x","change_reason":"created"}]}  `)
	must(t, err)
	if len(pages) != 1 || pages[0].Slug != "a" || pages[0].Sources[0] != "f1" {
		t.Fatalf("pages = %+v", pages)
	}
}

func TestParseResponseInvalid(t *testing.T) {
	if _, err := ParseResponse("not json"); !errors.Is(err, ErrInvalidJSON) {
		t.Fatalf("err = %v, want ErrInvalidJSON", err)
	}
}

func TestRenderAndParsePageMeta(t *testing.T) {
	p := Page{Slug: "auth-model", Category: "decisions", Title: "Auth model",
		Sources: []string{"f1", "f2"}, Body: "# Auth model\n\nbody"}
	rendered := RenderPage(p, "2026-06-23T16:00:00Z")
	if !strings.Contains(rendered, "title: Auth model") || !strings.HasSuffix(rendered, "body\n") {
		t.Fatalf("rendered = %q", rendered)
	}
	meta, err := ParsePageMeta(rendered)
	must(t, err)
	if meta.Title != "Auth model" || meta.Category != "decisions" ||
		len(meta.Sources) != 2 || meta.GeneratedAt != "2026-06-23T16:00:00Z" {
		t.Fatalf("meta = %+v", meta)
	}
}

func TestDeriveBucket(t *testing.T) {
	byID := map[string]fact.Fact{
		"a": {ID: "a", Scope: "project", Project: "shopapp"},
		"b": {ID: "b", Scope: "project", Project: "shopapp"},
		"c": {ID: "c", Scope: "project", Project: "datacli"},
		"g": {ID: "g", Scope: "global"},
	}
	if got := DeriveBucket([]string{"a", "b"}, byID); got != "projects/shopapp" {
		t.Fatalf("single project = %q", got)
	}
	if got := DeriveBucket([]string{"a", "c"}, byID); got != "global" {
		t.Fatalf("cross project = %q, want global", got)
	}
	if got := DeriveBucket([]string{"g"}, byID); got != "global" {
		t.Fatalf("global scope = %q, want global", got)
	}
}

func TestDeriveBucketSanitizesProject(t *testing.T) {
	byID := map[string]fact.Fact{"a": {ID: "a", Scope: "project", Project: "../etc"}}
	if got := DeriveBucket([]string{"a"}, byID); strings.Contains(got, "..") {
		t.Fatalf("bucket not sanitized: %q", got)
	}
}

func TestValidatePage(t *testing.T) {
	byID := map[string]fact.Fact{"f1": {ID: "f1"}}
	valid := map[string]bool{"concepts": true}
	good := Page{Slug: "ok-slug", Category: "concepts", Title: "T", Sources: []string{"f1"}, Body: "b"}
	must(t, ValidatePage(good, valid, byID))

	bad := []Page{
		{Slug: "Bad Slug", Category: "concepts", Title: "T", Sources: []string{"f1"}, Body: "b"},
		{Slug: "../escape", Category: "concepts", Title: "T", Sources: []string{"f1"}, Body: "b"},
		{Slug: "ok", Category: "nope", Title: "T", Sources: []string{"f1"}, Body: "b"},
		{Slug: "ok", Category: "concepts", Title: "", Sources: []string{"f1"}, Body: "b"},
		{Slug: "ok", Category: "concepts", Title: "T", Sources: nil, Body: "b"},
		{Slug: "ok", Category: "concepts", Title: "T", Sources: []string{"missing"}, Body: "b"},
		{Slug: "ok", Category: "concepts", Title: "T", Sources: []string{"f1"}, Body: ""},
	}
	for i, p := range bad {
		if err := ValidatePage(p, valid, byID); err == nil {
			t.Fatalf("bad page %d accepted", i)
		}
	}
}

func writePage(t *testing.T, dir, rel, title, cat string, srcs int) {
	t.Helper()
	s := "---\ntitle: " + title + "\ncategory: " + cat + "\nsources:\n"
	for i := 0; i < srcs; i++ {
		s += "  - f" + strconv.Itoa(i) + "\n"
	}
	s += "generated_at: 2026-06-23T16:00:00Z\n---\n\n# " + title + "\n\nbody\n"
	p := filepath.Join(dir, filepath.FromSlash(rel))
	must(t, os.MkdirAll(filepath.Dir(p), 0o755))
	must(t, os.WriteFile(p, []byte(s), 0o644))
}

func TestReadPagesSkipsReserved(t *testing.T) {
	dir := t.TempDir()
	writePage(t, dir, "projects/shopapp/decisions/auth.md", "Auth", "decisions", 1)
	must(t, os.WriteFile(filepath.Join(dir, "index.md"), []byte("# Wiki Index\n"), 0o644))
	must(t, os.WriteFile(filepath.Join(dir, "log.md"), []byte("# Wiki Log\n"), 0o644))
	pages, err := readPages(dir)
	must(t, err)
	if len(pages) != 1 || pages[0].RelPath != "projects/shopapp/decisions/auth.md" {
		t.Fatalf("pages = %+v", pages)
	}
}

func TestReadPagesMissingDir(t *testing.T) {
	pages, err := readPages(filepath.Join(t.TempDir(), "nope"))
	if err != nil || pages != nil {
		t.Fatalf("pages=%v err=%v, want nil,nil", pages, err)
	}
}

func TestRegenerateIndex(t *testing.T) {
	dir := t.TempDir()
	writePage(t, dir, "projects/shopapp/decisions/auth.md", "Auth", "decisions", 1)
	writePage(t, dir, "global/people/maria.md", "Maria", "people", 2)
	must(t, RegenerateIndex(dir))
	idx, _ := os.ReadFile(filepath.Join(dir, "index.md"))
	s := string(idx)
	if !strings.Contains(s, "## global") || !strings.Contains(s, "## projects/shopapp") {
		t.Fatalf("index missing buckets:\n%s", s)
	}
	if !strings.Contains(s, "[Auth](projects/shopapp/decisions/auth.md) — decisions — 1 source") {
		t.Fatalf("index missing auth line:\n%s", s)
	}
	if !strings.Contains(s, "— 2 sources") {
		t.Fatalf("index plural wrong:\n%s", s)
	}
}

func TestAppendLog(t *testing.T) {
	dir := t.TempDir()
	must(t, AppendLog(dir, "## entry1\n"))
	must(t, AppendLog(dir, "## entry2\n"))
	b, _ := os.ReadFile(filepath.Join(dir, "log.md"))
	if got := string(b); got != "## entry1\n## entry2\n" {
		t.Fatalf("log = %q", got)
	}
}

type fakeRunner struct {
	out       string
	err       error
	gotPrompt string
}

func (f *fakeRunner) Run(ctx context.Context, prompt string) (string, error) {
	f.gotPrompt = prompt
	return f.out, f.err
}

func fixedNow() time.Time { return time.Date(2026, 6, 23, 16, 0, 0, 0, time.UTC) }

func TestBuildWritesPagesIndexLog(t *testing.T) {
	dir := t.TempDir()
	facts := []fact.Fact{
		{ID: "2026-06-20-shopapp-jwt", Title: "JWT", Type: "decision", Project: "shopapp", Scope: "project", Body: "jwt"},
		{ID: "2026-06-22-datacli-sqlite", Title: "SQLite", Type: "decision", Project: "datacli", Scope: "project", Body: "sqlite"},
	}
	fr := &fakeRunner{out: `{"pages":[
		{"slug":"auth-model","category":"decisions","title":"Auth model","sources":["2026-06-20-shopapp-jwt"],"body":"# Auth model","change_reason":"created"},
		{"slug":"data-store","category":"decisions","title":"Data store","sources":["2026-06-22-datacli-sqlite"],"body":"# Data store","change_reason":"created"}
	]}`}
	res, err := Build(context.Background(), BuildOptions{
		WikiDir: dir, Facts: facts, Categories: DefaultCategories, Runner: fr, Now: fixedNow,
	})
	must(t, err)
	if len(res.Written) != 2 {
		t.Fatalf("written = %+v", res.Written)
	}
	for _, rel := range []string{"projects/shopapp/decisions/auth-model.md", "projects/datacli/decisions/data-store.md"} {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(rel))); err != nil {
			t.Fatalf("missing page %s: %v", rel, err)
		}
	}
	idx, _ := os.ReadFile(filepath.Join(dir, "index.md"))
	if !strings.Contains(string(idx), "## projects/shopapp") {
		t.Fatalf("index not regenerated:\n%s", idx)
	}
	logb, _ := os.ReadFile(filepath.Join(dir, "log.md"))
	if !strings.Contains(string(logb), "wiki build") || !strings.Contains(string(logb), "auth-model.md") {
		t.Fatalf("log not appended:\n%s", logb)
	}
}

func TestBuildAbortsOnInvalidPage(t *testing.T) {
	dir := t.TempDir()
	facts := []fact.Fact{{ID: "f1", Title: "F", Project: "p", Scope: "project", Body: "b"}}
	fr := &fakeRunner{out: `{"pages":[{"slug":"ok","category":"decisions","title":"T","sources":["nope"],"body":"b","change_reason":"x"}]}`}
	if _, err := Build(context.Background(), BuildOptions{
		WikiDir: dir, Facts: facts, Categories: DefaultCategories, Runner: fr, Now: fixedNow,
	}); err == nil {
		t.Fatal("Build should abort on unknown source")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("Build wrote files despite invalid page: %v", entries)
	}
}

func TestBuildReconciliationPassesExistingPageToPrompt(t *testing.T) {
	dir := t.TempDir()
	writePageRaw(t, dir, "projects/shopapp/decisions/auth-model.md",
		"---\ntitle: Auth model\ncategory: decisions\nsources:\n  - f1\ngenerated_at: 2026-06-20T00:00:00Z\n---\n\nMANUAL EDIT KEEP THIS\n")
	facts := []fact.Fact{{ID: "f1", Title: "JWT", Project: "shopapp", Scope: "project", Body: "jwt"}}
	fr := &fakeRunner{out: `{"pages":[]}`}
	_, err := Build(context.Background(), BuildOptions{
		WikiDir: dir, Facts: facts, Categories: DefaultCategories, Runner: fr, Now: fixedNow,
	})
	must(t, err)
	if !strings.Contains(fr.gotPrompt, "MANUAL EDIT KEEP THIS") {
		t.Fatalf("existing page not passed to LLM for reconciliation:\n%s", fr.gotPrompt)
	}
}

func TestBuildDryRunWritesNothing(t *testing.T) {
	dir := t.TempDir()
	facts := []fact.Fact{{ID: "f1", Title: "F", Project: "p", Scope: "project", Body: "b"}}
	fr := &fakeRunner{out: `{"pages":[{"slug":"ok","category":"decisions","title":"T","sources":["f1"],"body":"b","change_reason":"x"}]}`}
	res, err := Build(context.Background(), BuildOptions{
		WikiDir: dir, Facts: facts, Categories: DefaultCategories, Runner: fr, Now: fixedNow, DryRun: true,
	})
	must(t, err)
	if !res.DryRun || len(res.Written) != 1 {
		t.Fatalf("dry-run result = %+v", res)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("dry-run wrote files: %v", entries)
	}
}

func TestBuildPromptContainsFactsCategoriesAndSchema(t *testing.T) {
	facts := []fact.Fact{{ID: "f1", Title: "JWT decision", Project: "shopapp", Scope: "project", Body: "jwt body"}}
	p := BuildPrompt(facts, nil, DefaultCategories)
	for _, want := range []string{"JWT decision", "jwt body", "decisions, concepts", "json", "[[fact-id]]"} {
		if !strings.Contains(strings.ToLower(p), strings.ToLower(want)) {
			t.Fatalf("prompt missing %q:\n%s", want, p)
		}
	}
}

func writePageRaw(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	must(t, os.MkdirAll(filepath.Dir(p), 0o755))
	must(t, os.WriteFile(p, []byte(content), 0o644))
}
