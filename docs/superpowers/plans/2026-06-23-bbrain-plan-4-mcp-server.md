# BBrain — Plan 4: `bbrain mcp` (stdio MCP server) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `bbrain mcp` — a Model Context Protocol server over stdio (newline-delimited JSON-RPC 2.0) that exposes BBrain's memory + wiki operations as MCP tools, so Claude Code (and any MCP client) can drive BBrain. Stdlib only.

**Architecture:** New package `internal/mcp` (transport loop + Tool registry + tool catalog), wired by a new `bbrain mcp` command. The MCP layer calls the existing `internal/app` façade and never touches disk directly. Two small additive capabilities (`index.DeleteFact`, `store.Delete`, `app.Delete`, `app.Get`) back the `mem_get`/`mem_delete` tools.

**Tech Stack:** Go 1.25, stdlib (`bufio`, `bytes`, `encoding/json`, `context`, `io`, `os`, `path/filepath`, `strings`). No new dependencies.

## Global Constraints

- **Module path:** `bbrain`. **Go:** `go 1.25`. **Root:** `BBrain/`.
- **No new dependencies.** MCP-over-stdio is hand-rolled with stdlib.
- **Transport:** newline-delimited JSON — one JSON-RPC message per line. **stdout carries ONLY protocol JSON; all logs/diagnostics go to stderr.**
- **Protocol:** JSON-RPC 2.0; advertise `protocolVersion` `"2025-06-18"`; capability `tools:{}`. Lifecycle `initialize` → `notifications/initialized` (notification, no response) → `tools/list` / `tools/call` / `ping`.
- **JSON-RPC errors:** parse error `-32700`, method not found `-32601`, invalid params `-32602`. A message with no `id` is a **notification** and receives **no response**.
- **Tool failures are NOT protocol errors:** a handler error returns a normal result with `isError:true` and a text content block (per MCP `tools/call` semantics).
- **Layering:** `internal/mcp` imports `app`, `store`, `fact`, stdlib only. `cmd/bbrain` wires the command.
- **`.md` is source of truth;** the index is derived. `app.Delete` removes the `.md` and the index rows together (like `Save`/`Link`).
- **Tests** drive `Serve` over in-memory `io.Reader`/`io.Writer` (no real pipes); handlers run against a real `*app.App` on a `t.TempDir()` brain. `go test ./...` green + `go vet` clean before each commit.

Design spec: `docs/superpowers/specs/2026-06-23-bbrain-mcp-server-design.md`.

## Execution note (parallelism)

Task 1 (`internal/mcp/` core) and Task 2 (`internal/index`/`internal/store`/`internal/app` additions) touch **disjoint files** and may be implemented **in parallel**. Task 3 depends on both (Tool type + `app.Get`/`app.Delete`). Task 4 depends on Tasks 1–3.

---

## File Structure (Plan 4)

- `internal/mcp/tool.go` — **create:** `Tool` type.
- `internal/mcp/server.go` — **create:** `Server`, JSON-RPC envelopes, `Serve`, dispatch, initialize/tools-list/tools-call/ping/error handling.
- `internal/mcp/server_test.go` — **create:** protocol tests with a fake tool.
- `internal/mcp/tools.go` — **create:** `DefaultTools()` + all handlers + arg structs + schema constants + `currentProject`.
- `internal/mcp/tools_test.go` — **create:** handler tests against a real app.
- `internal/index/index.go` — **modify:** add `DeleteFact`.
- `internal/index/index_test.go` — **modify:** `DeleteFact` test.
- `internal/store/store.go` — **modify:** add `Delete`.
- `internal/store/store_test.go` — **modify:** `Delete` test.
- `internal/app/app.go` — **modify:** add `Get`, `Delete`.
- `internal/app/app_test.go` — **modify:** `Get`/`Delete` tests.
- `cmd/bbrain/main.go` — **modify:** `mcp` subcommand + usage line.
- `cmd/bbrain/main_test.go` — **modify:** protocol e2e via `run`.

---

## Task 1: `internal/mcp` — transport + Tool core

**Files:** Create `internal/mcp/tool.go`, `internal/mcp/server.go`, `internal/mcp/server_test.go`.

**Interfaces:**
- Consumes: `bbrain/internal/app`; stdlib `bufio`, `bytes`, `context`, `encoding/json`, `io`.
- Produces: `Tool`, `Server`, `const ProtocolVersion`, `func (s *Server) Serve(ctx, in, out) error`.

- [ ] **Step 1: Write the failing test**

Create `internal/mcp/server_test.go`:

```go
package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"bbrain/internal/app"
)

var errBoom = errors.New("boom")

// fakeEcho is a tool that echoes its args back as the result. Its handler takes
// the real *app.App (to match Tool.Handler) but never dereferences it.
func fakeEcho() Tool {
	return Tool{
		Name:        "echo",
		Description: "echo args",
		InputSchema: json.RawMessage(`{"type":"object"}`),
		Handler: func(ctx context.Context, a *app.App, args json.RawMessage) (any, error) {
			return map[string]any{"got": json.RawMessage(args)}, nil
		},
	}
}

func serve(t *testing.T, s *Server, requests ...string) []string {
	t.Helper()
	in := strings.NewReader(strings.Join(requests, "\n") + "\n")
	var out strings.Builder
	if err := s.Serve(context.Background(), in, &out); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	lines := []string{}
	for _, l := range strings.Split(strings.TrimRight(out.String(), "\n"), "\n") {
		if l != "" {
			lines = append(lines, l)
		}
	}
	return lines
}

func TestInitializeHandshake(t *testing.T) {
	s := &Server{Tools: []Tool{fakeEcho()}}
	out := serve(t, s, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
	if len(out) != 1 {
		t.Fatalf("want 1 response, got %d: %v", len(out), out)
	}
	var resp map[string]any
	if err := json.Unmarshal([]byte(out[0]), &resp); err != nil {
		t.Fatal(err)
	}
	res := resp["result"].(map[string]any)
	if res["protocolVersion"] != ProtocolVersion {
		t.Fatalf("protocolVersion = %v", res["protocolVersion"])
	}
	caps := res["capabilities"].(map[string]any)
	if _, ok := caps["tools"]; !ok {
		t.Fatalf("missing tools capability: %v", caps)
	}
	if res["serverInfo"].(map[string]any)["name"] != "bbrain" {
		t.Fatalf("serverInfo = %v", res["serverInfo"])
	}
}

func TestInitializedNotificationHasNoResponse(t *testing.T) {
	s := &Server{Tools: []Tool{fakeEcho()}}
	out := serve(t, s, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	if len(out) != 0 {
		t.Fatalf("notification produced a response: %v", out)
	}
}

func TestToolsListAndCall(t *testing.T) {
	s := &Server{Tools: []Tool{fakeEcho()}}
	out := serve(t, s,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"echo","arguments":{"x":1}}}`,
	)
	if len(out) != 2 {
		t.Fatalf("want 2 responses: %v", out)
	}
	if !strings.Contains(out[0], `"name":"echo"`) {
		t.Fatalf("tools/list = %s", out[0])
	}
	var call map[string]any
	json.Unmarshal([]byte(out[1]), &call)
	res := call["result"].(map[string]any)
	if res["isError"] != false {
		t.Fatalf("isError = %v", res["isError"])
	}
	content := res["content"].([]any)[0].(map[string]any)
	if content["type"] != "text" || !strings.Contains(content["text"].(string), `"x": 1`) {
		t.Fatalf("content = %v", content)
	}
}

func TestUnknownMethodAndBadJSON(t *testing.T) {
	s := &Server{Tools: []Tool{fakeEcho()}}
	out := serve(t, s,
		`{"jsonrpc":"2.0","id":4,"method":"no/such"}`,
		`{not json}`,
	)
	if len(out) != 2 {
		t.Fatalf("want 2 responses: %v", out)
	}
	if !strings.Contains(out[0], `-32601`) {
		t.Fatalf("want method-not-found: %s", out[0])
	}
	if !strings.Contains(out[1], `-32700`) {
		t.Fatalf("want parse error: %s", out[1])
	}
}

func TestToolHandlerErrorIsResultNotProtocolError(t *testing.T) {
	failing := Tool{Name: "boom", Handler: func(ctx context.Context, a *app.App, args json.RawMessage) (any, error) {
		return nil, errBoom
	}}
	s := &Server{Tools: []Tool{failing}}
	out := serve(t, s, `{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"boom","arguments":{}}}`)
	var resp map[string]any
	json.Unmarshal([]byte(out[0]), &resp)
	if _, isErr := resp["error"]; isErr {
		t.Fatalf("tool failure must not be a protocol error: %s", out[0])
	}
	res := resp["result"].(map[string]any)
	if res["isError"] != true {
		t.Fatalf("want isError:true, got %v", res)
	}
}
```

Note: the fake handlers take the real `*app.App` (matching `Tool.Handler`) but never dereference it — the `&Server{}` in these tests has a nil `App`, which is fine because the transport never touches it for these methods. `errBoom` is declared at the top of the file (shown above).

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd BBrain && go test ./internal/mcp/`
Expected: FAIL (package `mcp` does not exist).

- [ ] **Step 3: Implement `tool.go`**

Create `internal/mcp/tool.go`:

```go
package mcp

import (
	"context"
	"encoding/json"

	"bbrain/internal/app"
)

// Tool is one MCP tool: metadata advertised by tools/list and a handler invoked
// by tools/call. The handler receives the brain's App and the raw JSON arguments,
// and returns any value (JSON-encoded into the tool result) or an error (surfaced
// as a tool result with isError:true).
type Tool struct {
	Name        string
	Description string
	InputSchema json.RawMessage
	Handler     func(ctx context.Context, a *app.App, args json.RawMessage) (any, error)
}
```

- [ ] **Step 4: Implement `server.go`**

Create `internal/mcp/server.go`:

```go
// Package mcp is a minimal Model Context Protocol server over stdio. It speaks
// newline-delimited JSON-RPC 2.0 (one message per line) and exposes BBrain's
// operations as MCP tools. Stdlib only. stdout carries only protocol JSON; callers
// must send diagnostics to stderr.
package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"

	"bbrain/internal/app"
)

// ProtocolVersion is the MCP protocol version this server advertises.
const ProtocolVersion = "2025-06-18"

// Server serves MCP over an io.Reader/io.Writer pair.
type Server struct {
	App     *app.App
	Tools   []Tool
	Name    string // serverInfo name; defaults to "bbrain"
	Version string // serverInfo version; defaults to "dev"
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"` // absent => notification
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type rpcResponse struct {
	JSONRPC string    `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any       `json:"result,omitempty"`
	Error   *rpcError `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type callParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// Serve reads newline-delimited JSON-RPC requests from in and writes responses to
// out until EOF or ctx cancellation.
func (s *Server) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	enc := json.NewEncoder(out)
	for sc.Scan() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var req rpcRequest
		if err := json.Unmarshal(line, &req); err != nil {
			_ = enc.Encode(errResp(json.RawMessage("null"), -32700, "parse error"))
			continue
		}
		// Notifications (no id) never get a response.
		if len(req.ID) == 0 {
			continue
		}
		if err := enc.Encode(s.handle(ctx, req)); err != nil {
			return err
		}
	}
	return sc.Err()
}

func (s *Server) handle(ctx context.Context, req rpcRequest) rpcResponse {
	switch req.Method {
	case "initialize":
		return okResp(req.ID, map[string]any{
			"protocolVersion": ProtocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": s.name(), "version": s.version()},
		})
	case "ping":
		return okResp(req.ID, map[string]any{})
	case "tools/list":
		return okResp(req.ID, map[string]any{"tools": s.toolMetas()})
	case "tools/call":
		return s.callTool(ctx, req)
	default:
		return errResp(req.ID, -32601, "method not found: "+req.Method)
	}
}

type toolMeta struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

func (s *Server) toolMetas() []toolMeta {
	metas := make([]toolMeta, 0, len(s.Tools))
	for _, t := range s.Tools {
		schema := t.InputSchema
		if len(schema) == 0 {
			schema = json.RawMessage(`{"type":"object"}`)
		}
		metas = append(metas, toolMeta{Name: t.Name, Description: t.Description, InputSchema: schema})
	}
	return metas
}

func (s *Server) callTool(ctx context.Context, req rpcRequest) rpcResponse {
	var p callParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		return errResp(req.ID, -32602, "invalid params")
	}
	var tool *Tool
	for i := range s.Tools {
		if s.Tools[i].Name == p.Name {
			tool = &s.Tools[i]
			break
		}
	}
	if tool == nil {
		return errResp(req.ID, -32602, "unknown tool: "+p.Name)
	}
	args := p.Arguments
	if len(args) == 0 {
		args = json.RawMessage(`{}`)
	}
	result, err := tool.Handler(ctx, s.App, args)
	if err != nil {
		return okResp(req.ID, toolResult(err.Error(), true))
	}
	payload, mErr := json.MarshalIndent(result, "", "  ")
	if mErr != nil {
		return okResp(req.ID, toolResult("marshal result: "+mErr.Error(), true))
	}
	return okResp(req.ID, toolResult(string(payload), false))
}

func toolResult(text string, isError bool) map[string]any {
	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": text}},
		"isError": isError,
	}
}

func okResp(id json.RawMessage, result any) rpcResponse {
	return rpcResponse{JSONRPC: "2.0", ID: id, Result: result}
}

func errResp(id json.RawMessage, code int, msg string) rpcResponse {
	return rpcResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: msg}}
}

func (s *Server) name() string {
	if s.Name != "" {
		return s.Name
	}
	return "bbrain"
}

func (s *Server) version() string {
	if s.Version != "" {
		return s.Version
	}
	return "dev"
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd BBrain && go test ./internal/mcp/`
Expected: PASS (5 tests). Then `go vet ./internal/mcp/`.

- [ ] **Step 6: Commit**

```bash
cd BBrain
git add internal/mcp/tool.go internal/mcp/server.go internal/mcp/server_test.go
git commit -m "feat(mcp): minimal stdio JSON-RPC server core (initialize/tools-list/tools-call)"
```

---

## Task 2: `index`/`store`/`app` — Delete + Get (backing mem_get/mem_delete)

**Files:** Modify `internal/index/index.go`, `internal/index/index_test.go`, `internal/store/store.go`, `internal/store/store_test.go`, `internal/app/app.go`, `internal/app/app_test.go`.

**Interfaces:**
- Produces: `index.DeleteFact(id string) error`, `store.Delete(id string) (bool, error)`, `app.Get(id string) (fact.Fact, bool, error)`, `app.Delete(id string) (bool, error)`.

- [ ] **Step 1: Write the failing tests**

Append to `internal/index/index_test.go`:

```go
func TestDeleteFactRemovesFromSearchAndLinks(t *testing.T) {
	ix, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Close()
	f := fact.Fact{ID: "f1", Title: "JWT auth", Body: "tokens", Links: []fact.Link{{Target: "[[f2]]", Relation: "relates", Why: "x"}}}
	if err := ix.IndexFact(f, "p"); err != nil {
		t.Fatal(err)
	}
	if err := ix.IndexLinks(f); err != nil {
		t.Fatal(err)
	}
	if err := ix.DeleteFact("f1"); err != nil {
		t.Fatal(err)
	}
	res, _ := ix.Search("jwt", 10)
	if len(res) != 0 {
		t.Fatalf("search still returns deleted fact: %v", res)
	}
	if n, _ := ix.Neighbors("f2"); len(n) != 0 {
		t.Fatalf("links survived delete: %v", n)
	}
}
```

(`fact` is already imported in `index_test.go`.)

Append to `internal/store/store_test.go`:

```go
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
```

Append to `internal/app/app_test.go`:

```go
func TestAppGetAndDelete(t *testing.T) {
	a := New(t.TempDir())
	must(t, a.Init())
	f, err := a.Save(store.SaveInput{Type: "decision", Title: "JWT tokens", Body: "stateless", Project: "p", Scope: "project"})
	must(t, err)
	got, ok, err := a.Get(f.ID)
	must(t, err)
	if !ok || got.ID != f.ID {
		t.Fatalf("Get = %+v, ok=%v", got, ok)
	}
	deleted, err := a.Delete(f.ID)
	must(t, err)
	if !deleted {
		t.Fatal("Delete returned false")
	}
	if _, ok, _ := a.Get(f.ID); ok {
		t.Fatal("Get still finds the fact after Delete")
	}
	// Index reflects the delete too.
	res, err := a.Search("jwt", 10)
	must(t, err)
	if len(res) != 0 {
		t.Fatalf("search returns deleted fact: %v", res)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd BBrain && go test ./internal/index/ ./internal/store/ ./internal/app/`
Expected: FAIL (undefined `DeleteFact`, `Delete`, `Get`).

- [ ] **Step 3: Implement `index.DeleteFact`**

In `internal/index/index.go`, append:

```go
// DeleteFact removes a fact's search row and its outgoing links from the index.
func (ix *Index) DeleteFact(id string) error {
	tx, err := ix.db.Begin()
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM facts_fts WHERE fact_id = ?`, id); err != nil {
		tx.Rollback()
		return err
	}
	if _, err := tx.Exec(`DELETE FROM links WHERE src_id = ?`, id); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}
```

- [ ] **Step 4: Implement `store.Delete`**

In `internal/store/store.go`, append (uses already-imported `os`, `path/filepath`):

```go
// Delete removes a fact's .md file. It returns (false, nil) when no such file
// exists, so deleting an absent fact is a no-op rather than an error.
func (s *Store) Delete(id string) (bool, error) {
	if err := os.Remove(filepath.Join(s.Brain.FactsDir(), id+".md")); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}
```

- [ ] **Step 5: Implement `app.Get` + `app.Delete`**

In `internal/app/app.go`, append (uses already-imported `index`, `fact`):

```go
// Get returns a fact by id (ok=false if absent).
func (a *App) Get(id string) (fact.Fact, bool, error) {
	return a.Store.Get(id)
}

// Delete removes a fact's .md and drops it from the derived index. Returns
// (false, nil) if the fact did not exist.
func (a *App) Delete(id string) (bool, error) {
	deleted, err := a.Store.Delete(id)
	if err != nil {
		return false, err
	}
	if !deleted {
		return false, nil
	}
	if err := a.ensureIndexDir(); err != nil {
		return false, err
	}
	ix, err := index.Open(a.Brain.IndexPath())
	if err != nil {
		return false, err
	}
	defer ix.Close()
	if err := ix.DeleteFact(id); err != nil {
		return false, err
	}
	return true, nil
}
```

- [ ] **Step 6: Run tests to verify they pass**

Run: `cd BBrain && go test ./internal/index/ ./internal/store/ ./internal/app/`
Expected: PASS. Then `go vet ./...`.

- [ ] **Step 7: Commit**

```bash
cd BBrain
git add internal/index/ internal/store/ internal/app/
git commit -m "feat(index,store,app): DeleteFact + Delete + Get (fact removal keeps index in sync)"
```

---

## Task 3: `internal/mcp/tools.go` — the tool catalog

**Files:** Create `internal/mcp/tools.go`, `internal/mcp/tools_test.go`.

**Interfaces:**
- Consumes: Task 1 `Tool`; `app` methods incl. Task 2 `Get`/`Delete`; `store.SaveInput`, `fact`, `index` result types; stdlib `context`, `encoding/json`, `os`, `path/filepath`, `strings`.
- Produces: `func DefaultTools() []Tool`.

- [ ] **Step 1: Write the failing tests**

Create `internal/mcp/tools_test.go`:

```go
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd BBrain && go test ./internal/mcp/`
Expected: FAIL (undefined `DefaultTools`).

- [ ] **Step 3: Implement `tools.go`**

Create `internal/mcp/tools.go`:

```go
package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"bbrain/internal/app"
	"bbrain/internal/fact"
	"bbrain/internal/store"
)

// DefaultTools is BBrain's MCP tool catalog.
func DefaultTools() []Tool {
	return []Tool{
		{Name: "mem_save", Description: "Save a memory (fact) as Markdown and index it.", InputSchema: schemaMemSave, Handler: handleMemSave},
		{Name: "mem_search", Description: "Full-text search memories.", InputSchema: schemaMemSearch, Handler: handleMemSearch},
		{Name: "mem_get", Description: "Fetch one memory by id.", InputSchema: schemaID, Handler: handleMemGet},
		{Name: "mem_delete", Description: "Delete a memory by id.", InputSchema: schemaID, Handler: handleMemDelete},
		{Name: "mem_link", Description: "Add a reasoned typed link between two memories.", InputSchema: schemaMemLink, Handler: handleMemLink},
		{Name: "mem_why", Description: "Explain how two memories are directly related.", InputSchema: schemaMemWhy, Handler: handleMemWhy},
		{Name: "mem_related", Description: "List memories linked to/from a memory.", InputSchema: schemaID, Handler: handleMemRelated},
		{Name: "mem_candidates", Description: "Suggest memories lexically similar but not yet linked.", InputSchema: schemaMemCandidates, Handler: handleMemCandidates},
		{Name: "mem_current_project", Description: "Best-effort current project (env BBRAIN_PROJECT or cwd basename).", InputSchema: schemaEmpty, Handler: handleCurrentProject},
		{Name: "wiki_build", Description: "Distil facts into wiki pages via the configured LLM.", InputSchema: schemaWikiBuild, Handler: handleWikiBuild},
		{Name: "wiki_link", Description: "Grow the fact graph via the configured LLM.", InputSchema: schemaWikiLink, Handler: handleWikiLink},
		{Name: "wiki_lint", Description: "Check (and optionally --fix) wiki/fact consistency.", InputSchema: schemaWikiLint, Handler: handleWikiLint},
	}
}

// ---- schemas (hand-authored JSON Schema literals) ----

var (
	schemaEmpty   = json.RawMessage(`{"type":"object"}`)
	schemaID      = json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"}},"required":["id"]}`)
	schemaMemSave = json.RawMessage(`{"type":"object","properties":{"type":{"type":"string"},"title":{"type":"string"},"body":{"type":"string"},"project":{"type":"string"},"scope":{"type":"string"},"topic_key":{"type":"string"},"tags":{"type":"array","items":{"type":"string"}}},"required":["type","title","body"]}`)
	schemaMemSearch = json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"},"limit":{"type":"integer"}},"required":["query"]}`)
	schemaMemLink = json.RawMessage(`{"type":"object","properties":{"from":{"type":"string"},"to":{"type":"string"},"relation":{"type":"string"},"why":{"type":"string"}},"required":["from","to","relation","why"]}`)
	schemaMemWhy  = json.RawMessage(`{"type":"object","properties":{"a":{"type":"string"},"b":{"type":"string"}},"required":["a","b"]}`)
	schemaMemCandidates = json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"},"limit":{"type":"integer"}},"required":["id"]}`)
	schemaWikiBuild = json.RawMessage(`{"type":"object","properties":{"project":{"type":"string"},"scope":{"type":"string"},"categories":{"type":"array","items":{"type":"string"}},"dry_run":{"type":"boolean"}}}`)
	schemaWikiLink  = json.RawMessage(`{"type":"object","properties":{"project":{"type":"string"},"scope":{"type":"string"},"limit":{"type":"integer"},"dry_run":{"type":"boolean"}}}`)
	schemaWikiLint  = json.RawMessage(`{"type":"object","properties":{"categories":{"type":"array","items":{"type":"string"}},"fix":{"type":"boolean"}}}`)
)

// ---- views ----

func factView(f fact.Fact) map[string]any {
	return map[string]any{
		"id": f.ID, "type": f.Type, "scope": f.Scope, "project": f.Project,
		"title": f.Title, "body": f.Body, "tags": f.Tags,
		"created_at": f.CreatedAt, "updated_at": f.UpdatedAt,
		"revision_count": f.RevisionCount, "links": f.Links,
	}
}

// ---- handlers ----

type memSaveArgs struct {
	Type, Title, Body, Project, Scope, TopicKey string
	Tags                                        []string
}

func (m *memSaveArgs) UnmarshalJSON(b []byte) error {
	var raw struct {
		Type     string   `json:"type"`
		Title    string   `json:"title"`
		Body     string   `json:"body"`
		Project  string   `json:"project"`
		Scope    string   `json:"scope"`
		TopicKey string   `json:"topic_key"`
		Tags     []string `json:"tags"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	m.Type, m.Title, m.Body = raw.Type, raw.Title, raw.Body
	m.Project, m.Scope, m.TopicKey, m.Tags = raw.Project, raw.Scope, raw.TopicKey, raw.Tags
	return nil
}

func handleMemSave(ctx context.Context, a *app.App, raw json.RawMessage) (any, error) {
	var in memSaveArgs
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, err
	}
	f, err := a.Save(store.SaveInput{
		Type: in.Type, Title: in.Title, Body: in.Body,
		Project: in.Project, Scope: in.Scope, TopicKey: in.TopicKey, Tags: in.Tags,
	})
	if err != nil {
		return nil, err
	}
	return factView(f), nil
}

func handleMemSearch(ctx context.Context, a *app.App, raw json.RawMessage) (any, error) {
	var in struct {
		Query string `json:"query"`
		Limit int    `json:"limit"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, err
	}
	if in.Limit <= 0 {
		in.Limit = 10
	}
	res, err := a.Search(in.Query, in.Limit)
	if err != nil {
		return nil, err
	}
	return map[string]any{"results": res}, nil
}

func handleMemGet(ctx context.Context, a *app.App, raw json.RawMessage) (any, error) {
	var in struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, err
	}
	f, ok, err := a.Get(in.ID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return map[string]any{"found": false}, nil
	}
	return factView(f), nil
}

func handleMemDelete(ctx context.Context, a *app.App, raw json.RawMessage) (any, error) {
	var in struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, err
	}
	deleted, err := a.Delete(in.ID)
	if err != nil {
		return nil, err
	}
	return map[string]any{"deleted": deleted}, nil
}

func handleMemLink(ctx context.Context, a *app.App, raw json.RawMessage) (any, error) {
	var in struct {
		From, To, Relation, Why string
	}
	var rawArgs struct {
		From     string `json:"from"`
		To       string `json:"to"`
		Relation string `json:"relation"`
		Why      string `json:"why"`
	}
	if err := json.Unmarshal(raw, &rawArgs); err != nil {
		return nil, err
	}
	in.From, in.To, in.Relation, in.Why = rawArgs.From, rawArgs.To, rawArgs.Relation, rawArgs.Why
	f, err := a.Link(in.From, in.To, in.Relation, in.Why)
	if err != nil {
		return nil, err
	}
	return factView(f), nil
}

func handleMemWhy(ctx context.Context, a *app.App, raw json.RawMessage) (any, error) {
	var in struct {
		A string `json:"a"`
		B string `json:"b"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, err
	}
	edges, err := a.Why(in.A, in.B)
	if err != nil {
		return nil, err
	}
	return map[string]any{"edges": edges}, nil
}

func handleMemRelated(ctx context.Context, a *app.App, raw json.RawMessage) (any, error) {
	var in struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, err
	}
	n, err := a.Related(in.ID)
	if err != nil {
		return nil, err
	}
	return map[string]any{"neighbors": n}, nil
}

func handleMemCandidates(ctx context.Context, a *app.App, raw json.RawMessage) (any, error) {
	var in struct {
		ID    string `json:"id"`
		Limit int    `json:"limit"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, err
	}
	if in.Limit <= 0 {
		in.Limit = 8
	}
	res, err := a.Candidates(in.ID, in.Limit)
	if err != nil {
		return nil, err
	}
	return map[string]any{"candidates": res}, nil
}

func handleCurrentProject(ctx context.Context, a *app.App, raw json.RawMessage) (any, error) {
	return map[string]any{"project": currentProject()}, nil
}

// currentProject is a best-effort guess: $BBRAIN_PROJECT, else the cwd basename.
func currentProject() string {
	if p := strings.TrimSpace(os.Getenv("BBRAIN_PROJECT")); p != "" {
		return p
	}
	if wd, err := os.Getwd(); err == nil {
		return filepath.Base(wd)
	}
	return ""
}

func handleWikiBuild(ctx context.Context, a *app.App, raw json.RawMessage) (any, error) {
	var in struct {
		Project    string   `json:"project"`
		Scope      string   `json:"scope"`
		Categories []string `json:"categories"`
		DryRun     bool     `json:"dry_run"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, err
	}
	return a.WikiBuild(ctx, app.WikiBuildOptions{Project: in.Project, Scope: in.Scope, Categories: in.Categories, DryRun: in.DryRun})
}

func handleWikiLink(ctx context.Context, a *app.App, raw json.RawMessage) (any, error) {
	var in struct {
		Project string `json:"project"`
		Scope   string `json:"scope"`
		Limit   int    `json:"limit"`
		DryRun  bool   `json:"dry_run"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, err
	}
	return a.WikiLink(ctx, app.WikiLinkOptions{Project: in.Project, Scope: in.Scope, Limit: in.Limit, DryRun: in.DryRun})
}

func handleWikiLint(ctx context.Context, a *app.App, raw json.RawMessage) (any, error) {
	var in struct {
		Categories []string `json:"categories"`
		Fix        bool     `json:"fix"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, err
	}
	return a.WikiLint(app.WikiLintOptions{Categories: in.Categories, Fix: in.Fix})
}
```

(Note: `memSaveArgs` and the `mem_link` rawArgs use json-tagged shadow structs so snake_case keys like `topic_key` map correctly — Go's case-insensitive matching does not bridge underscores. The `in struct{From,To,...}` indirection in `handleMemLink` is redundant; the implementer may simplify to use `rawArgs` directly.)

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd BBrain && go test ./internal/mcp/`
Expected: PASS (Task 1 tests + the catalog tests). Then `go vet ./internal/mcp/`.

- [ ] **Step 5: Commit**

```bash
cd BBrain
git add internal/mcp/tools.go internal/mcp/tools_test.go
git commit -m "feat(mcp): tool catalog — mem_* and wiki_* handlers over the app facade"
```

---

## Task 4: `cmd/bbrain mcp` + protocol e2e

**Files:** Modify `cmd/bbrain/main.go`, `cmd/bbrain/main_test.go`.

**Interfaces:**
- Consumes: `internal/mcp` (`Server`, `DefaultTools`), `app.New`, `brainRoot`; stdlib `context`, `os`.
- Produces: `bbrain mcp` command.

- [ ] **Step 1: Write the failing e2e test**

Append to `cmd/bbrain/main_test.go`:

```go
func TestEndToEndMCP(t *testing.T) {
	t.Setenv("BBRAIN_HOME", t.TempDir())
	var out, errOut bytes.Buffer
	if code := run([]string{"init"}, &out, &errOut); code != 0 {
		t.Fatalf("init: %s", errOut.String())
	}
	// Drive the server over stdin/stdout via run(): initialize, then save+search.
	reqs := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"mem_save","arguments":{"type":"decision","title":"Use JWT","body":"stateless","project":"shopapp","scope":"project"}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"mem_search","arguments":{"query":"jwt"}}}`,
	}, "\n") + "\n"

	out.Reset()
	errOut.Reset()
	code := runStdin(t, []string{"mcp"}, reqs, &out, &errOut)
	if code != 0 {
		t.Fatalf("mcp exit=%d err=%s", code, errOut.String())
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	// Expect 3 responses (initialize, mem_save, mem_search); the notification yields none.
	if len(lines) != 3 {
		t.Fatalf("want 3 responses, got %d:\n%s", len(lines), out.String())
	}
	if !strings.Contains(lines[0], `"protocolVersion"`) {
		t.Fatalf("initialize resp = %s", lines[0])
	}
	if !strings.Contains(lines[2], "Use JWT") {
		t.Fatalf("search resp = %s", lines[2])
	}
}
```

This test needs a `runStdin` helper that runs the command with a provided stdin. Add it to `main_test.go`:

```go
func runStdin(t *testing.T, args []string, stdin string, out, errOut *bytes.Buffer) int {
	t.Helper()
	return runWithIn(args, strings.NewReader(stdin), out, errOut)
}
```

`runWithIn` is the stdin-aware entry point added to `main.go` in Step 3 (the existing `run` delegates to it with `os.Stdin`-equivalent empty input).

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd BBrain && go test ./cmd/...`
Expected: FAIL (`mcp` unknown; `runWithIn`/`runStdin` undefined).

- [ ] **Step 3: Wire the command**

In `cmd/bbrain/main.go`, add `"context"` and `bbrain/internal/mcp` to imports (the file already imports `os`, `io`). Refactor `run` to take an input reader so `mcp` can read stdin in tests, then add the `mcp` case.

Change the `run` signature usage: keep the existing `func run(args []string, stdout, stderr io.Writer) int` as a thin wrapper, and add the real body as `runWithIn`:

```go
// run is the CLI entrypoint used by main(); it reads from os.Stdin.
func run(args []string, stdout, stderr io.Writer) int {
	return runWithIn(args, os.Stdin, stdout, stderr)
}

func runWithIn(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: bbrain <version|init|save|search|reindex|link|why|related|candidates|wiki|mcp> [args]")
		return 2
	}
	switch args[0] {
	// ... existing cases unchanged ...
	case "mcp":
		return cmdMCP(args[1:], stdin, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown command: %s\n", args[0])
		return 2
	}
}
```

(Move the existing body of `run` into `runWithIn` verbatim, leaving every existing `case` intact, and add the `mcp` case + the `runWithIn`/`run` split. The `main()` function continues to call `run(os.Args[1:], os.Stdout, os.Stderr)`.)

Append `cmdMCP`:

```go
func cmdMCP(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	a := app.New(brainRoot())
	srv := &mcp.Server{App: a, Tools: mcp.DefaultTools()}
	if err := srv.Serve(context.Background(), stdin, stdout); err != nil {
		fmt.Fprintf(stderr, "mcp: %v\n", err)
		return 1
	}
	return 0
}
```

- [ ] **Step 4: Run the full suite**

Run: `cd BBrain && go test ./...`
Expected: PASS (all packages). Then `go vet ./...`.

- [ ] **Step 5: Manual smoke test**

```bash
cd BBrain
go build ./cmd/bbrain
rm -rf /tmp/bbrain-mcp-smoke && export BBRAIN_HOME=/tmp/bbrain-mcp-smoke
./bbrain init
printf '%s\n' \
  '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}' \
  '{"jsonrpc":"2.0","id":2,"method":"tools/list"}' \
  '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"mem_save","arguments":{"type":"decision","title":"Use JWT","body":"stateless","project":"shopapp","scope":"project"}}}' \
  '{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"mem_search","arguments":{"query":"jwt"}}}' \
  | ./bbrain mcp
unset BBRAIN_HOME
```
Expected: 4 JSON-RPC responses on stdout — `initialize` (protocolVersion + tools capability), `tools/list` (the catalog), a saved fact, and a search hit for "Use JWT".

- [ ] **Step 6: Commit**

```bash
cd BBrain
git add cmd/bbrain/main.go cmd/bbrain/main_test.go
git commit -m "feat(cli): bbrain mcp — serve the MCP tool catalog over stdio, with protocol e2e"
```

---

## Task 5 (runtime, not SDD): register with Claude Code and validate

After merge, validate the server against the real Claude Code MCP client:

```bash
cd BBrain && go build -o ./bbrain ./cmd/bbrain
claude mcp add bbrain --env BBRAIN_HOME=$HOME/.bbrain/default -- "$(pwd)/bbrain" mcp
claude mcp list           # expect: bbrain ... ✓ Connected
```
Then, from a `claude -p` session (or interactive), confirm a `mem_save` + `mem_search` round-trip works through the `bbrain` MCP tools. Record results in `docs/runtime-validation-claude-code.md`. (This step is performed by the controller inline, not a subagent.)

---

## Self-Review

**1. Spec coverage:** stdio JSON-RPC server (Task 1), tool catalog mem_*/wiki_* (Task 3), Delete/Get backing mem_get/mem_delete (Task 2), `bbrain mcp` CLI + e2e (Task 4), Claude Code runtime registration (Task 5). Deferred sessions/mem_context/mem_update per spec §7. ✓

**2. Placeholder scan:** Every code step shows complete code; tests show commands + expected output. The `handleMemLink` redundant indirection is flagged as an allowed simplification, not a gap. ✓

**3. Type consistency:** `Tool{Name,Description,InputSchema,Handler}` defined Task 1, consumed Task 3. `Server{App,Tools,Name,Version}` + `Serve(ctx,in,out)` defined Task 1, used Task 4. `app.Get`/`app.Delete` defined Task 2, used Task 3. `DefaultTools()` defined Task 3, used Task 4. Handlers return `any`; `wiki_*` handlers return the `app` result types directly (JSON-encodable). ✓

**4. Import/dependency sanity:** `internal/mcp` imports `app`, `store`, `fact`, stdlib — no cycle (`app` does not import `mcp`). `cmd` imports `mcp` + `app`. No `go.mod` change. ✓
