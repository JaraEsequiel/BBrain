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
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
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
