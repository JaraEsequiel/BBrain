# Architecture

_Detected by `/vex:init-repo`. Re-run to refresh._

## Workspaces

- **BBrain (root)** — Single-binary memory system for AI agents: persistent long-term memory via Markdown facts, FTS5 indexing, LLM-driven wiki distillation, and MCP server interface for Claude Code.

## Dependency edges

No runtime internal workspace dependencies — single monolithic package (`github.com/JaraEsequiel/BBrain`, Go 1.25). Internal architecture uses layered packages (fact → brain → store → index → app → llm/wiki/mcp/install/setup/vault/watch) but all are part of one binary with no cross-workspace imports.

No shared-config packages requiring tooling edges.
