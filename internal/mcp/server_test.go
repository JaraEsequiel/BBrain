package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/JaraEsequiel/BBrain/internal/app"
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
