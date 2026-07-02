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

func TestSourceIDs(t *testing.T) {
	dir := t.TempDir()
	// writePage cites f0..f(n-1): both pages cite f0/f1, so the union dedups them.
	writePage(t, dir, "projects/shopapp/decisions/auth.md", "Auth", "decisions", 2)
	writePage(t, dir, "global/people/maria.md", "Maria", "people", 3)
	must(t, os.WriteFile(filepath.Join(dir, "index.md"), []byte("# Wiki Index\n"), 0o644))
	must(t, os.WriteFile(filepath.Join(dir, "log.md"), []byte("# Wiki Log\n"), 0o644))
	ids, err := SourceIDs(dir)
	must(t, err)
	want := map[string]bool{"f0": true, "f1": true, "f2": true}
	if len(ids) != len(want) {
		t.Fatalf("ids = %v, want %v", ids, want)
	}
	for id := range want {
		if !ids[id] {
			t.Fatalf("ids = %v, missing %s", ids, id)
		}
	}
}

func TestSourceIDsMissingDir(t *testing.T) {
	ids, err := SourceIDs(filepath.Join(t.TempDir(), "nope"))
	must(t, err)
	if ids == nil || len(ids) != 0 {
		t.Fatalf("ids = %v, want empty non-nil map", ids)
	}
}

func TestSourceIDsBadFrontmatter(t *testing.T) {
	dir := t.TempDir()
	rel := "global/concepts/broken.md"
	p := filepath.Join(dir, filepath.FromSlash(rel))
	must(t, os.MkdirAll(filepath.Dir(p), 0o755))
	must(t, os.WriteFile(p, []byte("no frontmatter here\n"), 0o644))
	_, err := SourceIDs(dir)
	if err == nil || !strings.Contains(err.Error(), rel) {
		t.Fatalf("err = %v, want error mentioning %s", err, rel)
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

// TestBuildSkipsInvalidPage: a page citing an unknown fact id (the LLM hallucinated
// a source) is best-effort — the page is dropped and reported in res.InvalidPages,
// not aborted. Here the only page is invalid, so nothing is written but Build still
// returns without a Go error. Replaces the old abort-on-invalid-page contract.
func TestBuildSkipsInvalidPage(t *testing.T) {
	dir := t.TempDir()
	facts := []fact.Fact{{ID: "f1", Title: "F", Project: "p", Scope: "project", Body: "b"}}
	fr := &fakeRunner{out: `{"pages":[{"slug":"ok","category":"decisions","title":"T","sources":["nope"],"body":"b","change_reason":"x"}]}`}
	res, err := Build(context.Background(), BuildOptions{
		WikiDir: dir, Facts: facts, Categories: DefaultCategories, Runner: fr, Now: fixedNow,
	})
	must(t, err)
	if len(res.Written) != 0 {
		t.Fatalf("written = %+v, want nothing (only page was invalid)", res.Written)
	}
	if len(res.InvalidPages) != 1 || res.InvalidPages[0].Slug != "ok" || res.InvalidPages[0].Err == "" {
		t.Fatalf("invalidPages = %+v, want one entry for slug 'ok' with a reason", res.InvalidPages)
	}
	for _, e := range mustReadDir(t, dir) {
		if e == "index.md" || e == "log.md" {
			continue
		}
		t.Fatalf("Build wrote a page despite invalid page: %s", e)
	}
}

// TestBuildSkipsInvalidPageWritesValidOnes: two pages in one batch — one cites an
// unknown fact (invalid), one is fine. Build must COMPLETE, write the valid page,
// and report the invalid one in res.InvalidPages, no Go error. Fails against the
// old code that aborted the whole build on the first invalid page.
func TestBuildSkipsInvalidPageWritesValidOnes(t *testing.T) {
	dir := t.TempDir()
	facts := []fact.Fact{
		{ID: "real", Title: "Real", Project: "shopapp", Scope: "project", Body: "b"},
	}
	// One batch, two pages: "good" cites the real fact; "bad" cites a hallucinated id.
	fr := &fakeRunner{out: `{"pages":[
		{"slug":"good","category":"decisions","title":"Good","sources":["real"],"body":"b","change_reason":"x"},
		{"slug":"bad","category":"decisions","title":"Bad","sources":["hallucinated"],"body":"b","change_reason":"x"}
	]}`}
	res, err := Build(context.Background(), BuildOptions{
		WikiDir: dir, Facts: facts, Categories: DefaultCategories, Runner: fr, Now: fixedNow,
	})
	must(t, err)
	if len(res.Written) != 1 || res.Written[0] != "projects/shopapp/decisions/good.md" {
		t.Fatalf("written = %+v, want just the good page", res.Written)
	}
	if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash("projects/shopapp/decisions/good.md"))); err != nil {
		t.Fatalf("good page not written: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash("projects/shopapp/decisions/bad.md"))); err == nil {
		t.Fatal("bad page was written, want it skipped")
	}
	if len(res.InvalidPages) != 1 || res.InvalidPages[0].Slug != "bad" {
		t.Fatalf("invalidPages = %+v, want one entry for slug 'bad'", res.InvalidPages)
	}
	// The invalid page must be reported in the log too.
	logb, _ := os.ReadFile(filepath.Join(dir, "log.md"))
	if !strings.Contains(string(logb), "INVALID") || !strings.Contains(string(logb), "bad") {
		t.Fatalf("log missing invalid-page report:\n%s", logb)
	}
}

// TestBuildInfraFailureStillAborts: the page is VALID, but its target directory
// can't be created — a regular file already sits where the page's parent dir
// needs to go, so MkdirAll fails with ENOTDIR during the write pass. This is
// infrastructure (IO), not bad LLM content — Build must return a Go error, NOT
// treat it as an invalid page and silently drop it.
func TestBuildInfraFailureStillAborts(t *testing.T) {
	dir := t.TempDir()
	// The valid page resolves to projects/p/decisions/ok.md. Plant a file at
	// projects/p so MkdirAll("projects/p/decisions") fails with ENOTDIR.
	must(t, os.MkdirAll(filepath.Join(dir, "projects"), 0o755))
	must(t, os.WriteFile(filepath.Join(dir, "projects", "p"), []byte("x"), 0o644))

	facts := []fact.Fact{{ID: "f1", Title: "F", Project: "p", Scope: "project", Body: "b"}}
	fr := &fakeRunner{out: `{"pages":[{"slug":"ok","category":"decisions","title":"T","sources":["f1"],"body":"b","change_reason":"x"}]}`}
	if _, err := Build(context.Background(), BuildOptions{
		WikiDir: dir, Facts: facts, Categories: DefaultCategories, Runner: fr, Now: fixedNow,
	}); err == nil {
		t.Fatal("Build should return an error on an IO/write failure, not skip the page")
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

// countFactHeaders counts "### <id>" fact markers in a BuildPrompt output. It
// only counts the facts section (before "## Existing wiki pages") so existing
// page "### relpath" headers don't inflate the count.
func countFactHeaders(prompt string) int {
	facts := prompt
	if i := strings.Index(prompt, "## Existing wiki pages"); i >= 0 {
		facts = prompt[:i]
	}
	n := 0
	for _, line := range strings.Split(facts, "\n") {
		if strings.HasPrefix(line, "### ") {
			n++
		}
	}
	return n
}

// batchAwareRunner fails if any single prompt carries more than MaxFacts fact
// headers, proving Build() no longer makes a monolithic call. It returns a
// distinct page per batch (slug derived from the batch's first fact id) so every
// batch's output is validatable.
type batchAwareRunner struct {
	maxFacts int
	batches  int
}

func (r *batchAwareRunner) Run(ctx context.Context, prompt string) (string, error) {
	n := countFactHeaders(prompt)
	if n > r.maxFacts {
		return "", errors.New("prompt too large: " + strconv.Itoa(n) + " facts > " + strconv.Itoa(r.maxFacts))
	}
	r.batches++
	// Echo one page per fact in this batch so each fact becomes a source.
	var pages []string
	for _, line := range strings.Split(prompt, "\n") {
		id, ok := strings.CutPrefix(line, "### ")
		if !ok || strings.Contains(id, "/") { // skip existing-page headers (relpaths)
			continue
		}
		pages = append(pages, `{"slug":"p-`+id+`","category":"concepts","title":"T","sources":["`+id+`"],"body":"b","change_reason":"x"}`)
	}
	return `{"pages":[` + strings.Join(pages, ",") + `]}`, nil
}

func TestBuildBatchesBelowRunnerLimit(t *testing.T) {
	dir := t.TempDir()
	var facts []fact.Fact
	for i := 0; i < 50; i++ {
		facts = append(facts, fact.Fact{
			ID: "f" + strconv.Itoa(i), Title: "T", Scope: "global", Body: "b",
		})
	}
	// Runner refuses any prompt with >10 facts; with BatchSize=10 Build must split.
	fr := &batchAwareRunner{maxFacts: 10}
	res, err := Build(context.Background(), BuildOptions{
		WikiDir: dir, Facts: facts, Categories: DefaultCategories,
		Runner: fr, Now: fixedNow, BatchSize: 10,
	})
	must(t, err)
	if fr.batches != 5 {
		t.Fatalf("batches = %d, want 5", fr.batches)
	}
	if len(res.Written) != 50 {
		t.Fatalf("written = %d, want 50", len(res.Written))
	}
}

func TestBuildDefaultBatchSizeIsTen(t *testing.T) {
	dir := t.TempDir()
	// 11 facts with BatchSize:0 (=> defaultBatchSize) must split into 2 batches.
	// This fails against the old default of 30 (which would be a single batch).
	var facts []fact.Fact
	for i := 0; i < 11; i++ {
		facts = append(facts, fact.Fact{ID: "f" + strconv.Itoa(i), Title: "T", Scope: "global", Body: "b"})
	}
	fr := &batchAwareRunner{maxFacts: 11} // no per-prompt limit; we only count batches
	_, err := Build(context.Background(), BuildOptions{
		WikiDir: dir, Facts: facts, Categories: DefaultCategories,
		Runner: fr, Now: fixedNow, // BatchSize omitted => defaultBatchSize
	})
	must(t, err)
	if fr.batches != 2 {
		t.Fatalf("batches = %d, want 2 (default batch size 10 over 11 facts)", fr.batches)
	}
}

// TestBuildBatchFailureIsVisible: failing batches must stay visible. Under
// skip-on-exhaust they surface via res.Skipped (not a Go error) and never write
// garbage. Here every batch fails (runner refuses >5 facts, BatchSize=10), so all
// 3 batches are skipped, nothing is written, and each is reported.
func TestBuildBatchFailureIsVisible(t *testing.T) {
	dir := t.TempDir()
	var facts []fact.Fact
	for i := 0; i < 30; i++ {
		facts = append(facts, fact.Fact{ID: "f" + strconv.Itoa(i), Title: "T", Scope: "global", Body: "b"})
	}
	fr := &batchAwareRunner{maxFacts: 5}
	res, err := Build(context.Background(), BuildOptions{
		WikiDir: dir, Facts: facts, Categories: DefaultCategories,
		Runner: fr, Now: fixedNow, BatchSize: 10,
	})
	must(t, err)
	if len(res.Written) != 0 {
		t.Fatalf("written = %+v, want nothing when every batch fails", res.Written)
	}
	if len(res.Skipped) != 3 {
		t.Fatalf("skipped = %+v, want all 3 batches reported", res.Skipped)
	}
	for _, e := range mustReadDir(t, dir) {
		if e == "index.md" || e == "log.md" {
			continue
		}
		t.Fatalf("Build wrote a page despite every batch failing: %s", e)
	}
}

// scriptedRunner returns a queued response per successive Run call, so a test can
// script what each batch emits.
type scriptedRunner struct {
	outs []string
	i    int
}

func (r *scriptedRunner) Run(ctx context.Context, prompt string) (string, error) {
	out := r.outs[r.i]
	r.i++
	return out, nil
}

func TestBuildRetriesTransientBatchFailure(t *testing.T) {
	dir := t.TempDir()
	facts := []fact.Fact{{ID: "a", Title: "A", Scope: "project", Project: "shopapp", Body: "ba"}}
	// One batch (BatchSize=1): first attempt returns malformed JSON, second is valid.
	// The non-deterministic backend flaked once; the retry must recover.
	fr := &scriptedRunner{outs: []string{
		`{"pages":[{"slug":"auth","category":"decisions","title":"Auth","sources":True}]}`,
		`{"pages":[{"slug":"auth","category":"decisions","title":"Auth","sources":["a"],"body":"BODY","change_reason":"created"}]}`,
	}}
	res, err := Build(context.Background(), BuildOptions{
		WikiDir: dir, Facts: facts, Categories: DefaultCategories,
		Runner: fr, Now: fixedNow, BatchSize: 1,
	})
	must(t, err)
	if len(res.Written) != 1 {
		t.Fatalf("written = %+v, want the page after retry", res.Written)
	}
	if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash("projects/shopapp/decisions/auth.md"))); err != nil {
		t.Fatalf("page not written after retry: %v", err)
	}
	if fr.i != 2 {
		t.Fatalf("runner calls = %d, want 2 (one flake + one success)", fr.i)
	}
}

// countingFailRunner always fails and counts how many times it was called, to
// prove Build gives up only after maxBatchAttempts attempts on a truly-broken batch.
type countingFailRunner struct{ calls int }

func (r *countingFailRunner) Run(ctx context.Context, prompt string) (string, error) {
	r.calls++
	return "", errors.New("backend down")
}

// TestBuildGivesUpAfterMaxAttempts: with skip-on-exhaust semantics, a batch that
// exhausts its retries is no longer a Go error — the build degrades gracefully.
// When that batch is the ONLY batch, the contract is: no error, nothing written
// (a broken batch never writes garbage), and the drop is reported in res.Skipped
// (so it stays auditable). The retry count is still capped at maxBatchAttempts.
func TestBuildGivesUpAfterMaxAttempts(t *testing.T) {
	dir := t.TempDir()
	facts := []fact.Fact{{ID: "a", Title: "A", Scope: "project", Project: "shopapp", Body: "ba"}}
	fr := &countingFailRunner{}
	res, err := Build(context.Background(), BuildOptions{
		WikiDir: dir, Facts: facts, Categories: DefaultCategories,
		Runner: fr, Now: fixedNow, BatchSize: 1,
	})
	must(t, err) // a flaky batch is not a build error anymore
	if fr.calls != maxBatchAttempts {
		t.Fatalf("runner calls = %d, want maxBatchAttempts=%d", fr.calls, maxBatchAttempts)
	}
	if len(res.Written) != 0 {
		t.Fatalf("written = %+v, want nothing (broken batch must not write)", res.Written)
	}
	if len(res.Skipped) != 1 || res.Skipped[0].Index != 1 || len(res.Skipped[0].FactIDs) != 1 || res.Skipped[0].FactIDs[0] != "a" {
		t.Fatalf("skipped = %+v, want one entry for batch 1 with fact 'a'", res.Skipped)
	}
	if res.Skipped[0].Err == "" {
		t.Fatalf("skipped entry should carry the last error")
	}
	// No page files, but index.md/log.md are fine (build still ran the write pass).
	for _, e := range mustReadDir(t, dir) {
		if e == "index.md" || e == "log.md" {
			continue
		}
		t.Fatalf("unexpected file written for a fully-skipped build: %s", e)
	}
}

// mustReadDir returns the base names in dir.
func mustReadDir(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	must(t, err)
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

// TestBuildSkipsBadBatchAndWritesGoodOnes: batch A (fact "bad") always fails its
// 3 attempts; batch B (fact "good") is valid. Build must COMPLETE without error,
// write B's page, and report A in res.Skipped. Fails against the old code that
// aborted the whole build on the first exhausted batch.
func TestBuildSkipsBadBatchAndWritesGoodOnes(t *testing.T) {
	dir := t.TempDir()
	facts := []fact.Fact{
		{ID: "bad", Title: "Bad", Scope: "project", Project: "shopapp", Body: "b"},
		{ID: "good", Title: "Good", Scope: "project", Project: "shopapp", Body: "g"},
	}
	fr := &perFactRunner{failFact: "bad"}
	res, err := Build(context.Background(), BuildOptions{
		WikiDir: dir, Facts: facts, Categories: DefaultCategories,
		Runner: fr, Now: fixedNow, BatchSize: 1,
	})
	must(t, err)
	if len(res.Written) != 1 {
		t.Fatalf("written = %+v, want 1 page from the good batch", res.Written)
	}
	if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash("projects/shopapp/concepts/good.md"))); err != nil {
		t.Fatalf("good page not written: %v", err)
	}
	if len(res.Skipped) != 1 || res.Skipped[0].FactIDs[0] != "bad" {
		t.Fatalf("skipped = %+v, want one entry for fact 'bad'", res.Skipped)
	}
	// The bad batch cost maxBatchAttempts calls; the good batch cost 1.
	if fr.badCalls != maxBatchAttempts {
		t.Fatalf("bad-batch calls = %d, want %d", fr.badCalls, maxBatchAttempts)
	}
}

// TestBuildAllBatchesSkippedWritesNothing: every batch exhausts its retries.
// Contract: no Go error, res.Written empty, res.Skipped holds all batches. The
// caller/CLI treats "nothing written + skips" as a failure; Build itself does not.
func TestBuildAllBatchesSkippedWritesNothing(t *testing.T) {
	dir := t.TempDir()
	facts := []fact.Fact{
		{ID: "a", Title: "A", Scope: "global", Body: "a"},
		{ID: "b", Title: "B", Scope: "global", Body: "b"},
	}
	fr := &countingFailRunner{}
	res, err := Build(context.Background(), BuildOptions{
		WikiDir: dir, Facts: facts, Categories: DefaultCategories,
		Runner: fr, Now: fixedNow, BatchSize: 1,
	})
	must(t, err)
	if len(res.Written) != 0 {
		t.Fatalf("written = %+v, want nothing", res.Written)
	}
	if len(res.Skipped) != 2 {
		t.Fatalf("skipped = %+v, want both batches", res.Skipped)
	}
}

// perFactRunner returns malformed JSON for the batch containing failFact and a
// valid one-page-per-fact response otherwise.
type perFactRunner struct {
	failFact string
	badCalls int
}

func (r *perFactRunner) Run(ctx context.Context, prompt string) (string, error) {
	var ids []string
	for _, line := range strings.Split(prompt, "\n") {
		id, ok := strings.CutPrefix(line, "### ")
		if !ok || strings.Contains(id, "/") { // skip existing-page headers (relpaths)
			continue
		}
		ids = append(ids, id)
	}
	for _, id := range ids {
		if id == r.failFact {
			r.badCalls++
			return `{"pages":[{"slug":"x","category":"concepts","title":"T","sources":True}]}`, nil
		}
	}
	var pages []string
	for _, id := range ids {
		pages = append(pages, `{"slug":"`+id+`","category":"concepts","title":"T","sources":["`+id+`"],"body":"b","change_reason":"x"}`)
	}
	return `{"pages":[` + strings.Join(pages, ",") + `]}`, nil
}

func TestBuildMergesPagesSameKey(t *testing.T) {
	dir := t.TempDir()
	// Two facts, same project => same bucket. Two batches (BatchSize=1) each emit
	// a page with the same (bucket, category, slug) but partially-overlapping sources.
	facts := []fact.Fact{
		{ID: "a", Title: "A", Scope: "project", Project: "shopapp", Body: "ba"},
		{ID: "b", Title: "B", Scope: "project", Project: "shopapp", Body: "bb"},
	}
	fr := &scriptedRunner{outs: []string{
		`{"pages":[{"slug":"auth","category":"decisions","title":"Auth","sources":["a"],"body":"BODY-1","change_reason":"created"}]}`,
		`{"pages":[{"slug":"auth","category":"decisions","title":"Auth-later","sources":["a","b"],"body":"BODY-2","change_reason":"updated"}]}`,
	}}
	res, err := Build(context.Background(), BuildOptions{
		WikiDir: dir, Facts: facts, Categories: DefaultCategories,
		Runner: fr, Now: fixedNow, BatchSize: 1,
	})
	must(t, err)
	if len(res.Written) != 1 {
		t.Fatalf("written = %+v, want a single merged page", res.Written)
	}
	rel := "projects/shopapp/decisions/auth.md"
	b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
	must(t, err)
	content := string(b)
	meta, err := ParsePageMeta(content)
	must(t, err)
	// Sources: union with dedup, first-seen order => [a, b].
	if len(meta.Sources) != 2 || meta.Sources[0] != "a" || meta.Sources[1] != "b" {
		t.Fatalf("merged sources = %v, want [a b]", meta.Sources)
	}
	// Title from first batch that emitted the key.
	if meta.Title != "Auth" {
		t.Fatalf("merged title = %q, want Auth", meta.Title)
	}
	// Body: both bodies preserved (append with separator), deterministic order.
	if !strings.Contains(content, "BODY-1") || !strings.Contains(content, "BODY-2") {
		t.Fatalf("merged body lost information:\n%s", content)
	}
	if strings.Index(content, "BODY-1") > strings.Index(content, "BODY-2") {
		t.Fatalf("merged body order not deterministic (batch order):\n%s", content)
	}
}

func TestBuildDoesNotMergeDifferentBuckets(t *testing.T) {
	dir := t.TempDir()
	// Same slug+category, but different projects => different buckets => two files.
	facts := []fact.Fact{
		{ID: "a", Title: "A", Scope: "project", Project: "shopapp", Body: "ba"},
		{ID: "b", Title: "B", Scope: "project", Project: "datacli", Body: "bb"},
	}
	fr := &scriptedRunner{outs: []string{
		`{"pages":[{"slug":"auth","category":"decisions","title":"Auth","sources":["a"],"body":"BODY-A","change_reason":"x"}]}`,
		`{"pages":[{"slug":"auth","category":"decisions","title":"Auth","sources":["b"],"body":"BODY-B","change_reason":"x"}]}`,
	}}
	res, err := Build(context.Background(), BuildOptions{
		WikiDir: dir, Facts: facts, Categories: DefaultCategories,
		Runner: fr, Now: fixedNow, BatchSize: 1,
	})
	must(t, err)
	if len(res.Written) != 2 {
		t.Fatalf("written = %+v, want two files (distinct buckets)", res.Written)
	}
}

func writePageRaw(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	must(t, os.MkdirAll(filepath.Dir(p), 0o755))
	must(t, os.WriteFile(p, []byte(content), 0o644))
}
