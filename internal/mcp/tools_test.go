package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"bbrain/internal/app"
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

func TestCatalogHasExpectedTools(t *testing.T) {
	want := []string{"mem_save", "mem_search", "mem_get", "mem_delete", "mem_link", "mem_why", "mem_related", "mem_candidates", "mem_current_project", "wiki_build", "wiki_link", "wiki_lint"}
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
