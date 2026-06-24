package mcp

import (
	"context"
	"encoding/json"

	"github.com/JaraEsequiel/BBrain/internal/app"
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
