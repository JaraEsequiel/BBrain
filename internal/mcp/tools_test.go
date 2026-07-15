package mcp

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/JaraEsequiel/BBrain/internal/app"
	"github.com/JaraEsequiel/BBrain/internal/index"
	"github.com/JaraEsequiel/BBrain/internal/store"
)

func toolByName(t *testing.T, name string) Tool {
	t.Helper()
	for _, tl := range DefaultTools() {
		if tl.Name == name {
			return tl
		}
	}
	t.Fatalf("tool %q not in catalog", name)
	return Tool{}
}

func call(t *testing.T, a *app.App, name, args string) string {
	t.Helper()
	res, err := toolByName(t, name).Handler(context.Background(), a, json.RawMessage(args))
	if err != nil {
		t.Fatalf("%s handler: %v", name, err)
	}
	b, _ := json.Marshal(res)
	return string(b)
}

// BBRAIN-5: catalog must include mem_browse.
func TestCatalogHasExpectedTools(t *testing.T) {
	want := []string{"mem_save", "mem_search", "mem_get", "mem_delete", "mem_link", "mem_why", "mem_related", "mem_candidates", "mem_current_project", "mem_browse", "wiki_build", "wiki_link", "wiki_lint", "mem_archive", "mem_unarchive"}
	have := map[string]bool{}
	for _, tl := range DefaultTools() {
		have[tl.Name] = true
		if len(tl.InputSchema) == 0 {
			t.Fatalf("tool %q has empty InputSchema", tl.Name)
		}
	}
	for _, w := range want {
		if !have[w] {
			t.Fatalf("catalog missing tool %q", w)
		}
	}
}

// BBRAIN-4 AC-5: mem_search MCP schema passes project/type through to
// ix.Search. TC-5.1 project param reaches the filter at the MCP boundary;
// TC-5.2 omitting project/type behaves identically to pre-change (unfiltered).
func TestMemSearchProjectFilter(t *testing.T) {
	a := app.New(t.TempDir())
	if err := a.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if _, err := a.Save(store.SaveInput{Title: "alpha shared", Body: "b", Type: "decision", Project: "bbrain"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := a.Save(store.SaveInput{Title: "beta shared", Body: "b", Type: "decision", Project: "vexforge"}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// AC-5 TC-5.1: project param reaches ix.Search through the MCP boundary
	out := call(t, a, "mem_search", `{"query":"shared","project":"bbrain"}`)
	if !strings.Contains(out, "alpha shared") || strings.Contains(out, "beta shared") {
		t.Fatalf("AC-5 TC-5.1 project filter not applied at MCP boundary: %s", out)
	}

	// AC-5 TC-5.2: omitting project/type behaves identically to pre-change (unfiltered)
	out = call(t, a, "mem_search", `{"query":"shared"}`)
	if !strings.Contains(out, "alpha shared") || !strings.Contains(out, "beta shared") {
		t.Fatalf("AC-5 TC-5.2 unfiltered mem_search should return both facts: %s", out)
	}
}

// BBRAIN-5 AC-1/AC-2: mem_browse filters by project at the MCP boundary and
// never returns the full fact body (title+id+type projection only).
func TestMemBrowseFiltersByProject(t *testing.T) {
	a := app.New(t.TempDir())
	if err := a.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if _, err := a.Save(store.SaveInput{Title: "bbrain fact", Body: "b", Type: "decision", Project: "bbrain"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := a.Save(store.SaveInput{Title: "vexforge fact", Body: "b", Type: "decision", Project: "vexforge"}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	out := call(t, a, "mem_browse", `{"project":"bbrain"}`)
	if !strings.Contains(out, "bbrain fact") || strings.Contains(out, "vexforge fact") {
		t.Fatalf("project filter not applied at MCP boundary: %s", out)
	}
	if strings.Contains(out, `"body"`) {
		t.Fatalf("mem_browse must not return full fact body: %s", out)
	}
}

func TestMemSaveGetSearchDelete(t *testing.T) {
	a := app.New(t.TempDir())
	if err := a.Init(); err != nil {
		t.Fatal(err)
	}
	out := call(t, a, "mem_save", `{"type":"decision","title":"Use JWT","body":"stateless tokens","project":"shopapp","scope":"project","tags":["auth"]}`)
	var saved map[string]any
	json.Unmarshal([]byte(out), &saved)
	id, _ := saved["id"].(string)
	if id == "" {
		t.Fatalf("mem_save returned no id: %s", out)
	}
	if g := call(t, a, "mem_get", `{"id":"`+id+`"}`); !strings.Contains(g, "Use JWT") {
		t.Fatalf("mem_get = %s", g)
	}
	if sr := call(t, a, "mem_search", `{"query":"jwt"}`); !strings.Contains(sr, id) {
		t.Fatalf("mem_search = %s", sr)
	}
	if d := call(t, a, "mem_delete", `{"id":"`+id+`"}`); !strings.Contains(d, `"deleted":true`) {
		t.Fatalf("mem_delete = %s", d)
	}
	if g := call(t, a, "mem_get", `{"id":"`+id+`"}`); !strings.Contains(g, `"found":false`) {
		t.Fatalf("mem_get after delete = %s", g)
	}
}

func TestMemLinkWhyAndWikiBuild(t *testing.T) {
	a := app.New(t.TempDir())
	if err := a.Init(); err != nil {
		t.Fatal(err)
	}
	o1 := call(t, a, "mem_save", `{"type":"decision","title":"Auth model","body":"jwt","project":"p","scope":"project"}`)
	o2 := call(t, a, "mem_save", `{"type":"decision","title":"Session storage","body":"redis","project":"p","scope":"project"}`)
	id1 := mustID(t, o1)
	id2 := mustID(t, o2)
	if l := call(t, a, "mem_link", `{"from":"`+id1+`","to":"`+id2+`","relation":"depends-on","why":"auth needs sessions"}`); !strings.Contains(l, id2) {
		t.Fatalf("mem_link = %s", l)
	}
	if w := call(t, a, "mem_why", `{"a":"`+id1+`","b":"`+id2+`"}`); !strings.Contains(w, "depends-on") {
		t.Fatalf("mem_why = %s", w)
	}
	// wiki_build with a fake runner injected on the app.
	a.Runner = fakeBuildRunner{out: `{"pages":[{"slug":"auth","category":"decisions","title":"Auth","sources":["` + id1 + `"],"body":"# Auth","change_reason":"x"}]}`}
	if wb := call(t, a, "wiki_build", `{}`); !strings.Contains(wb, "auth.md") {
		t.Fatalf("wiki_build = %s", wb)
	}
}

func TestMemGetFallsBackToArchiveTier(t *testing.T) {
	a := app.New(t.TempDir())
	if err := a.Init(); err != nil {
		t.Fatal(err)
	}
	id := mustID(t, call(t, a, "mem_save", `{"type":"decision","title":"Old auth call","body":"legacy jwt","project":"p","scope":"project"}`))

	// Active fact: response must be exactly as today — no "archived" key.
	if g := call(t, a, "mem_get", `{"id":"`+id+`"}`); strings.Contains(g, "archived") {
		t.Fatalf("active mem_get must not carry an archived key: %s", g)
	}

	if _, err := a.Store.Archive(id); err != nil {
		t.Fatal(err)
	}
	g := call(t, a, "mem_get", `{"id":"`+id+`"}`)
	if !strings.Contains(g, "Old auth call") || !strings.Contains(g, `"archived":true`) {
		t.Fatalf("mem_get on archived id should return the fact with archived:true, got: %s", g)
	}

	// Unknown id still reports found:false.
	if g := call(t, a, "mem_get", `{"id":"2026-01-01-nope"}`); !strings.Contains(g, `"found":false`) {
		t.Fatalf("mem_get on missing id = %s", g)
	}
}

func TestMemRelatedToleratesArchivedNeighbor(t *testing.T) {
	a := app.New(t.TempDir())
	if err := a.Init(); err != nil {
		t.Fatal(err)
	}
	idA := mustID(t, call(t, a, "mem_save", `{"type":"decision","title":"Auth model","body":"jwt","project":"p","scope":"project"}`))
	idB := mustID(t, call(t, a, "mem_save", `{"type":"decision","title":"Session storage","body":"redis","project":"p","scope":"project"}`))
	call(t, a, "mem_link", `{"from":"`+idA+`","to":"`+idB+`","relation":"depends-on","why":"auth needs sessions"}`)

	// Archive B the way App.Archive will: move the .md and drop B from the index.
	if _, err := a.Store.Archive(idB); err != nil {
		t.Fatal(err)
	}
	ix, err := index.Open(a.Brain.IndexPath())
	if err != nil {
		t.Fatal(err)
	}
	if err := ix.DeleteFact(idB); err != nil {
		ix.Close()
		t.Fatal(err)
	}
	ix.Close()

	// The A→B edge is now dangling in `links` — mem_related must still return it.
	if r := call(t, a, "mem_related", `{"id":"`+idA+`"}`); !strings.Contains(r, idB) {
		t.Fatalf("mem_related should keep the edge to the archived fact %s, got: %s", idB, r)
	}
}

func mustID(t *testing.T, out string) string {
	t.Helper()
	var m map[string]any
	json.Unmarshal([]byte(out), &m)
	id, _ := m["id"].(string)
	if id == "" {
		t.Fatalf("no id in %s", out)
	}
	return id
}

type fakeBuildRunner struct{ out string }

func (f fakeBuildRunner) Run(ctx context.Context, prompt string) (string, error) { return f.out, nil }

func TestMemDeleteRejectsTraversal(t *testing.T) {
	a := app.New(t.TempDir())
	if err := a.Init(); err != nil {
		t.Fatal(err)
	}
	if _, err := toolByName(t, "mem_delete").Handler(context.Background(), a, json.RawMessage(`{"id":"../../victim"}`)); err == nil {
		t.Fatal("mem_delete must reject a traversal id with an error")
	}
}

func TestMemSearchResultUsesSnakeCase(t *testing.T) {
	a := app.New(t.TempDir())
	if err := a.Init(); err != nil {
		t.Fatal(err)
	}
	call(t, a, "mem_save", `{"type":"decision","title":"JWT thing","body":"b","project":"p","scope":"project"}`)
	sr := call(t, a, "mem_search", `{"query":"jwt"}`)
	if !strings.Contains(sr, `"fact_id"`) {
		t.Fatalf("mem_search result not snake_case: %s", sr)
	}
}

func TestMemSearchOmitsStaleKeyWhenIndexCurrent(t *testing.T) {
	a := app.New(t.TempDir())
	if err := a.Init(); err != nil {
		t.Fatal(err)
	}
	call(t, a, "mem_save", `{"type":"note","title":"Fresh fact","body":"current schema","project":"p","scope":"project"}`)

	raw, _ := json.Marshal(map[string]any{"query": "fresh", "limit": 10})
	out, err := handleMemSearch(context.Background(), a, raw)
	if err != nil {
		t.Fatalf("handleMemSearch: %v", err)
	}
	m, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("handleMemSearch returned %T, want map[string]any", out)
	}
	if _, present := m["stale"]; present {
		t.Fatalf("response = %+v, want no \"stale\" key against a current index", m)
	}
}

func TestMemSaveTopicKeyUpsert(t *testing.T) {
	a := app.New(t.TempDir())
	if err := a.Init(); err != nil {
		t.Fatal(err)
	}
	id1 := mustID(t, call(t, a, "mem_save", `{"type":"decision","title":"V1","body":"b1","project":"p","scope":"project","topic_key":"auth/jwt"}`))
	id2 := mustID(t, call(t, a, "mem_save", `{"type":"decision","title":"V2","body":"b2","project":"p","scope":"project","topic_key":"auth/jwt"}`))
	if id1 != id2 {
		t.Fatalf("topic_key upsert created a new id: %s vs %s", id1, id2)
	}
}

func TestHandleMemSavePinned(t *testing.T) {
	a := app.New(t.TempDir())
	if err := a.Init(); err != nil {
		t.Fatal(err)
	}
	raw := json.RawMessage(`{"type":"about-me","title":"About","body":"hi","scope":"global","topic_key":"profile/about-me","pinned":true}`)
	out, err := handleMemSave(context.Background(), a, raw)
	if err != nil {
		t.Fatal(err)
	}
	m, ok := out.(map[string]any)
	if !ok || m["pinned"] != true {
		t.Fatalf("output did not echo pinned:true: %#v", out)
	}
	// Persisted — reload by id (independent of the digest / Task B).
	got, found, err := a.Get(m["id"].(string))
	if err != nil {
		t.Fatal(err)
	}
	if !found || !got.Pinned {
		t.Fatalf("pinned not persisted: found=%v fact=%+v", found, got)
	}
}

// BBRAIN-7

func TestMemArchiveKnownId(t *testing.T) {
	// AC-1: mem_archive with a known id archives the fact
	// AC-2: response includes the archived id
	a := app.New(t.TempDir())
	if err := a.Init(); err != nil {
		t.Fatal(err)
	}
	out := call(t, a, "mem_save", `{"type":"decision","title":"x","body":"y","project":"p","scope":"project"}`)
	var saved map[string]any
	if err := json.Unmarshal([]byte(out), &saved); err != nil {
		t.Fatal(err)
	}
	id, _ := saved["id"].(string)

	res := call(t, a, "mem_archive", `{"ids":["`+id+`"]}`)
	if !strings.Contains(res, `"count":1`) {
		t.Fatalf("AC-1/AC-2: mem_archive count = %s", res)
	}
	if !strings.Contains(res, id) {
		t.Fatalf("AC-2: mem_archive response missing archived id: %s", res)
	}
	if _, ok, err := a.GetArchived(id); err != nil || !ok {
		t.Fatalf("AC-1: fact not archived after mem_archive: ok=%v err=%v", ok, err)
	}
	if _, ok, _ := a.Get(id); ok {
		t.Fatal("AC-1: fact still active after mem_archive")
	}
}

func TestMemArchiveBatchCount(t *testing.T) {
	// AC-3: batch archive reports correct count
	a := app.New(t.TempDir())
	if err := a.Init(); err != nil {
		t.Fatal(err)
	}
	var ids []string
	for i := 0; i < 2; i++ {
		out := call(t, a, "mem_save", `{"type":"decision","title":"x`+strconv.Itoa(i)+`","body":"y","project":"p","scope":"project"}`)
		var saved map[string]any
		json.Unmarshal([]byte(out), &saved)
		id, _ := saved["id"].(string)
		ids = append(ids, id)
	}
	res := call(t, a, "mem_archive", `{"ids":["`+ids[0]+`","`+ids[1]+`"]}`)
	if !strings.Contains(res, `"count":2`) {
		t.Fatalf("AC-3: expected count 2, got %s", res)
	}
}

func TestMemArchiveUnknownIdSkippedNotCrashed(t *testing.T) {
	// AC-7: unknown id doesn't crash the tool
	// AC-8: unknown id is never falsely reported as archived
	a := app.New(t.TempDir())
	if err := a.Init(); err != nil {
		t.Fatal(err)
	}
	out := call(t, a, "mem_save", `{"type":"decision","title":"x","body":"y","project":"p","scope":"project"}`)
	var saved map[string]any
	json.Unmarshal([]byte(out), &saved)
	knownID, _ := saved["id"].(string)

	res := call(t, a, "mem_archive", `{"ids":["`+knownID+`","unknown-id-zzz"]}`)
	if !strings.Contains(res, `"count":1`) {
		t.Fatalf("AC-7/AC-8: expected only the known id archived, got %s", res)
	}
	if strings.Contains(res, "unknown-id-zzz") {
		t.Fatalf("AC-8: unknown id falsely reported as archived: %s", res)
	}
	if _, ok, err := a.GetArchived(knownID); err != nil || !ok {
		t.Fatal("AC-7: known id in a mixed batch must still archive")
	}
}

func TestMemArchivePinnedFactSkipped(t *testing.T) {
	// design D2 / candidate AC: a pinned fact is skipped, not a batch abort
	a := app.New(t.TempDir())
	if err := a.Init(); err != nil {
		t.Fatal(err)
	}
	out := call(t, a, "mem_save", `{"type":"decision","title":"x","body":"y","project":"p","scope":"project","pinned":true}`)
	var saved map[string]any
	json.Unmarshal([]byte(out), &saved)
	id, _ := saved["id"].(string)

	res := call(t, a, "mem_archive", `{"ids":["`+id+`"]}`)
	if !strings.Contains(res, `"count":0`) {
		t.Fatalf("pinned fact must be skipped, not archived: %s", res)
	}
}

func TestMemArchiveEmptyIdsReturnsZeroCount(t *testing.T) {
	// design D2: empty ids is the degenerate zero-iteration case, no error
	a := app.New(t.TempDir())
	if err := a.Init(); err != nil {
		t.Fatal(err)
	}
	res := call(t, a, "mem_archive", `{"ids":[]}`)
	if !strings.Contains(res, `"count":0`) {
		t.Fatalf("empty ids must return count 0, not an error: %s", res)
	}
}

func TestMemArchiveRejectsOversizedBatch(t *testing.T) {
	// security review finding: an unbounded ids array does unbounded filesystem
	// work; reject a batch over maxBatchIDs before looping.
	a := app.New(t.TempDir())
	if err := a.Init(); err != nil {
		t.Fatal(err)
	}
	ids := make([]string, maxBatchIDs+1)
	for i := range ids {
		ids[i] = "nonexistent"
	}
	raw, err := json.Marshal(map[string][]string{"ids": ids})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := toolByName(t, "mem_archive").Handler(context.Background(), a, raw); err == nil {
		t.Fatal("mem_archive must reject a batch over maxBatchIDs")
	}
	if _, err := toolByName(t, "mem_unarchive").Handler(context.Background(), a, raw); err == nil {
		t.Fatal("mem_unarchive must reject a batch over maxBatchIDs")
	}
}

func TestMemArchiveSchemaIsIdListOnly(t *testing.T) {
	// AC-6: schema is id-list-only, no filter/bulk fields
	schema := string(toolByName(t, "mem_archive").InputSchema)
	for _, forbidden := range []string{"older-than", "distilled", "\"project\"", "\"apply\"", "\"type\":{"} {
		if strings.Contains(schema, forbidden) {
			t.Fatalf("AC-6: mem_archive schema must not contain %q: %s", forbidden, schema)
		}
	}
	if !strings.Contains(schema, `"ids"`) {
		t.Fatalf("AC-6: mem_archive schema must declare \"ids\": %s", schema)
	}
}

func TestMemGetAfterArchiveReturnsArchivedTrue(t *testing.T) {
	// AC-9: mem_get on an archived id still returns archived:true (no regression)
	a := app.New(t.TempDir())
	if err := a.Init(); err != nil {
		t.Fatal(err)
	}
	out := call(t, a, "mem_save", `{"type":"decision","title":"x","body":"y","project":"p","scope":"project"}`)
	var saved map[string]any
	json.Unmarshal([]byte(out), &saved)
	id, _ := saved["id"].(string)
	call(t, a, "mem_archive", `{"ids":["`+id+`"]}`)

	got := call(t, a, "mem_get", `{"id":"`+id+`"}`)
	if !strings.Contains(got, `"archived":true`) {
		t.Fatalf("AC-9: mem_get after mem_archive must return archived:true, got %s", got)
	}
}

func TestMemUnarchiveKnownId(t *testing.T) {
	// AC-4: mem_unarchive with a known id unarchives the fact
	// AC-5: unarchive response confirms id + count
	a := app.New(t.TempDir())
	if err := a.Init(); err != nil {
		t.Fatal(err)
	}
	out := call(t, a, "mem_save", `{"type":"decision","title":"x","body":"y","project":"p","scope":"project"}`)
	var saved map[string]any
	json.Unmarshal([]byte(out), &saved)
	id, _ := saved["id"].(string)
	call(t, a, "mem_archive", `{"ids":["`+id+`"]}`)

	res := call(t, a, "mem_unarchive", `{"ids":["`+id+`"]}`)
	if !strings.Contains(res, `"count":1`) || !strings.Contains(res, id) {
		t.Fatalf("AC-5: mem_unarchive response = %s", res)
	}
	if _, ok, _ := a.Get(id); !ok {
		t.Fatal("AC-4: fact not active after mem_unarchive")
	}
	if _, ok, _ := a.GetArchived(id); ok {
		t.Fatal("AC-4: fact still in archive tier after mem_unarchive")
	}
}

func TestMemUnarchiveUnknownIdSkippedNotCrashed(t *testing.T) {
	// AC-7/AC-8 mirrored for unarchive
	a := app.New(t.TempDir())
	if err := a.Init(); err != nil {
		t.Fatal(err)
	}
	res := call(t, a, "mem_unarchive", `{"ids":["unknown-id-zzz"]}`)
	if !strings.Contains(res, `"count":0`) {
		t.Fatalf("unknown id must not be unarchived: %s", res)
	}
	if strings.Contains(res, "unknown-id-zzz") {
		t.Fatalf("unknown id falsely reported: %s", res)
	}
}

func TestMemUnarchiveSchemaIsIdListOnly(t *testing.T) {
	// AC-6, mem_unarchive half
	schema := string(toolByName(t, "mem_unarchive").InputSchema)
	for _, forbidden := range []string{"older-than", "distilled", "\"project\"", "\"apply\"", "\"type\":{"} {
		if strings.Contains(schema, forbidden) {
			t.Fatalf("AC-6: mem_unarchive schema must not contain %q: %s", forbidden, schema)
		}
	}
	if !strings.Contains(schema, `"ids"`) {
		t.Fatalf("AC-6: mem_unarchive schema must declare \"ids\": %s", schema)
	}
}

func TestMemSaveSurfacesRelatedCandidates(t *testing.T) {
	a := app.New(t.TempDir())

	// Save fact A.
	outA := call(t, a, "mem_save", `{"type":"decision","title":"Postgres connection pool tuning","body":"set max pool size to 20","project":"p"}`)
	var ra map[string]any
	if err := json.Unmarshal([]byte(outA), &ra); err != nil {
		t.Fatalf("A response not JSON: %v\n%s", err, outA)
	}
	idA, _ := ra["id"].(string)
	if idA == "" {
		t.Fatalf("A has no id: %s", outA)
	}

	// Save fact B — lexically similar to A (shared distinctive terms).
	outB := call(t, a, "mem_save", `{"type":"decision","title":"Postgres connection pool sizing","body":"reconsider the pool size","project":"p"}`)
	if !strings.Contains(outB, `"related"`) {
		t.Fatalf("B's save should surface related candidates:\n%s", outB)
	}
	if !strings.Contains(outB, idA) {
		t.Fatalf("B's related should include A's id %q:\n%s", idA, outB)
	}

	// Save fact C — disjoint vocabulary → no related.
	outC := call(t, a, "mem_save", `{"type":"decision","title":"Frontend teal palette","body":"pick teal accents","project":"p"}`)
	if strings.Contains(outC, `"related"`) {
		t.Fatalf("C is dissimilar; should have no related:\n%s", outC)
	}
}
