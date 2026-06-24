<!-- BBRAIN:BEGIN -->
## BBrain memory

This project uses BBrain for durable memory (brain at /home/vex/.bbrain/default). The bbrain MCP server exposes:
- mcp__bbrain__mem_save / mem_search / mem_get / mem_delete — save and recall facts.
- mcp__bbrain__mem_link / mem_why / mem_related / mem_candidates — the reasoned graph.
- mcp__bbrain__wiki_build / wiki_link / wiki_lint — distil and maintain the wiki.

Save durable decisions and learnings via mem_save; search with mem_search before answering.
The wiki LLM backend is $BBRAIN_AGENT_CLI -> /home/vex/.bbrain/default/.bbrain/agents/claude-code.sh. Workflow: build -> link -> rebuild; wiki_lint --fix for consistency.
<!-- BBRAIN:END -->
