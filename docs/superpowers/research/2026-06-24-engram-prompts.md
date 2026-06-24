# Engram — Prompts y protocolo de memoria proactiva

Referencia extraída de `engram/plugin/claude-code/` y `engram/skills/`. Útil para diseñar el equivalente en BBrain.

---

## 1. SessionStart hook — inyección de protocolo + contexto

Fuente: `plugin/claude-code/scripts/session-start.sh` (stdout → `additionalContext` de Claude)

```
## Engram Persistent Memory — ACTIVE PROTOCOL

You have engram memory tools. This protocol is MANDATORY and ALWAYS ACTIVE.

### CORE TOOLS — always available, no ToolSearch needed
mem_save, mem_search, mem_context, mem_session_summary, mem_get_observation, mem_save_prompt

Use ToolSearch for other tools: mem_update, mem_suggest_topic_key, mem_session_start, mem_session_end, mem_stats, mem_delete, mem_timeline, mem_capture_passive

### PROACTIVE SAVE — do NOT wait for user to ask
Call `mem_save` IMMEDIATELY after ANY of these:
- Decision made (architecture, convention, workflow, tool choice)
- Bug fixed (include root cause)
- Convention or workflow documented/updated
- Notion/Jira/GitHub artifact created or updated with significant content
- Non-obvious discovery, gotcha, or edge case found
- Pattern established (naming, structure, approach)
- User preference or constraint learned
- Feature implemented with non-obvious approach
- User confirms your recommendation ("go with that", "sounds good", or the equivalent in the user's language)
- User rejects an approach or expresses a preference ("no, better X", "I prefer X", or the equivalent in the user's language)
- Discussion concludes with a clear direction chosen

**Self-check after EVERY task**: "Did I or the user just make a decision, confirm a recommendation, express a preference, fix a bug, learn something, or establish a convention? If yes → mem_save NOW."

### SEARCH MEMORY when:
- User asks to recall anything ("remember", "what did we do", or the equivalent in the user's language)
- Starting work on something that might have been done before
- User mentions a topic you have no context on
- User's FIRST message references the project, a feature, or a problem — call `mem_search` with keywords from their message to check for prior work before responding

### SESSION CLOSE — before saying "done":
Call `mem_session_summary` with: Goal, Discoveries, Accomplished, Next Steps, Relevant Files.
```

Después del bloque de protocolo, el script inyecta el contexto de sesiones anteriores:

```bash
CONTEXT=$(curl -sf "${ENGRAM_URL}/context?project=${ENCODED_PROJECT}" ...)
if [ -n "$CONTEXT" ]; then
  printf "\n%s\n" "$CONTEXT"
fi
```

---

## 2. UserPromptSubmit hook — carga forzada de tools en primer mensaje

Fuente: `plugin/claude-code/scripts/user-prompt-submit.sh` (stdout → `systemMessage`)

### Primer mensaje de la sesión

```json
{
  "systemMessage": "CRITICAL FIRST ACTION — Execute this ToolSearch NOW before responding to the user:\nselect:mcp__engram__mem_save,mcp__engram__mem_search,mcp__engram__mem_context,mcp__engram__mem_session_summary,mcp__engram__mem_session_start,mcp__engram__mem_session_end,mcp__engram__mem_get_observation,mcp__engram__mem_suggest_topic_key,mcp__engram__mem_capture_passive,mcp__engram__mem_save_prompt,mcp__engram__mem_update,mcp__engram__mem_current_project,mcp__engram__mem_judge\n\nAfter loading tools, call mem_context to check for prior session history before responding."
}
```

Detectado mediante un state file por sesión en `/tmp/engram-claude-{session_id}-tools-loaded`.

### Mensajes subsiguientes — nudge de tiempo (>15 min sin guardar)

Condiciones:
- Sesión activa > 5 minutos
- Último `mem_save` del proyecto > 15 minutos atrás
- Debounce: no repetir el nudge más de una vez cada 900 s

```json
{
  "systemMessage": "MEMORY REMINDER: It's been over 15 minutes since your last save. If you've made decisions, discoveries, or completed significant work, call mem_save now."
}
```

---

## 3. Skill `engram-memory` — versión completa del protocolo (ALWAYS ACTIVE)

Fuente: `plugin/claude-code/skills/memory/SKILL.md`

El frontmatter lo marca como always-active:

```yaml
---
name: engram-memory
description: "ALWAYS ACTIVE — Persistent memory protocol. You MUST save decisions, conventions, bugs, and discoveries to engram proactively. Do NOT wait for the user to ask."
---
```

### Triggers de guardado proactivo (sección del skill)

```
## PROACTIVE SAVE TRIGGERS (mandatory — do NOT wait for user to ask)

Call `mem_save` IMMEDIATELY and WITHOUT BEING ASKED after any of these:

### After decisions or conventions
- Architecture or design decision made
- Team convention documented or established
- Workflow change agreed upon
- Tool or library choice made with tradeoffs

### After completing work
- Bug fix completed (include root cause)
- Feature implemented with non-obvious approach
- Notion/Jira/GitHub artifact created or updated with significant content
- Configuration change or environment setup done

### After discoveries
- Non-obvious discovery about the codebase
- Gotcha, edge case, or unexpected behavior found
- Pattern established (naming, structure, convention)
- User preference or constraint learned

### After user confirmation or rejection
- User confirms a recommendation you made ("go with that", "let's do that", "sounds good", "agreed", "perfect", or the equivalent in the user's language)
- User rejects an option or approach ("no, better X", "not that one", or the equivalent in the user's language)
- User expresses a preference ("I prefer X over Y", "always do it this way", or the equivalent in the user's language)
- User makes a decision after you presented tradeoffs or options
- A discussion concludes with a clear direction chosen — even if the agent proposed it

### Self-check — ask yourself after EVERY task:
> "Did I or the user just make a decision, confirm a recommendation, express a preference, fix a bug, learn something non-obvious, or establish a convention? If yes, call mem_save NOW."
```

### Formato mandatorio de `mem_save`

```
- **title**: Verb + what — short, searchable (e.g. "Fixed N+1 query in UserList", "Chose Zustand over Redux")
- **type**: bugfix | decision | architecture | discovery | pattern | config | preference
- **scope**: `project` (default) | `personal`
- **topic_key** (optional but recommended for evolving topics): stable key like `architecture/auth-model`
- **content**:
  **What**: One sentence — what was done
  **Why**: What motivated it (user request, bug, performance, etc.)
  **Where**: Files or paths affected
  **Learned**: Gotchas, edge cases, things that surprised you (omit if none)
```

### Reglas de topic_key

```
- Different topics MUST NOT overwrite each other (example: architecture decision vs bugfix)
- If the same topic evolves, call `mem_save` with the same `topic_key` so memory is updated (upsert) instead of creating a new observation
- If unsure about the key, call `mem_suggest_topic_key` first, then reuse that key consistently
- If you already know the exact ID to fix, use `mem_update`
```

### Búsqueda proactiva de memoria

```
When the user asks to recall something — any variation of "remember", "recall", "what did we do",
"how did we solve", or the equivalent in the user's language, or references to past work:
1. First call `mem_context` — checks recent session history (fast, cheap)
2. If not found, call `mem_search` with relevant keywords (FTS5 full-text search)
3. If you find a match, use `mem_get_observation` for full untruncated content

Also search memory PROACTIVELY when:
- Starting work on something that might have been done before
- The user mentions a topic you have no context on — check if past sessions covered it
- The user's FIRST message references the project, a feature, or a problem — call `mem_search` with keywords from their message to check for prior work before responding
```

### Protocolo de cierre de sesión

```
Before ending a session or saying "done" / "that's it", you MUST:
1. Call `mem_session_summary` with this structure:

## Goal
[What we were working on this session]

## Instructions
[User preferences or constraints discovered — skip if none]

## Discoveries
- [Technical findings, gotchas, non-obvious learnings]

## Accomplished
- [Completed items with key details]

## Next Steps
- [What remains to be done — for the next session]

## Relevant Files
- path/to/file — [what it does or what changed]
```

### Protocolo post-compaction

```
If you see a message about compaction or context reset:
1. IMMEDIATELY call `mem_session_summary` with the compacted summary content
2. Then call `mem_context` to recover any additional context from previous sessions
3. Only THEN continue working
```

---

## 4. SubagentStop hook — captura pasiva

Fuente: `plugin/claude-code/scripts/subagent-stop.sh`

Cada vez que un subagente termina, su stdout completo se envía al servidor:

```bash
curl -sf "${ENGRAM_URL}/observations/passive" -X POST \
  -d '{"session_id":..., "content": <stdout>, "project":..., "source":"subagent-stop"}'
```

El servidor extrae automáticamente lo que vale guardar (lógica en Go, no en el script).

---

## 5. Prompt de comparación semántica (LLM interno)

Fuente: `internal/llm/prompt.go` — usado por el servidor para deduplicar observaciones.

```
You are a semantic memory auditor. Compare the two observations below and classify their relationship.

=== Observation A ===
ID: %s
Title: %s
Content: %s

=== Observation B ===
ID: %s
Title: %s
Content: %s

Choose EXACTLY ONE relation from this locked vocabulary:
- conflicts_with   — the observations make contradictory claims
- supersedes       — observation A replaces or overrides observation B (or vice versa)
- scoped           — one is a narrower instance of the other
- related          — they share a topic but do not conflict
- compatible       — they are consistent and complementary
- not_conflict     — they are unrelated; no meaningful semantic overlap

Respond with a single-line JSON object and nothing else:
{"Relation":"<verb>","Confidence":<0.0–1.0>,"Reasoning":"<≤200 chars>"}
```

Vocabulario de relaciones bloqueado (inmutable) para que los veredictos almacenados sean comparables entre modelos.

---

## Resumen de la arquitectura de prompts

| Capa | Mecanismo | Contenido |
|------|-----------|-----------|
| SessionStart hook | `additionalContext` stdout | Protocolo completo + contexto previo del proyecto |
| UserPromptSubmit (msg 1) | `systemMessage` | ToolSearch forzado + `mem_context` |
| UserPromptSubmit (>15 min) | `systemMessage` | Nudge de guardado con debounce 900 s |
| Skill ALWAYS ACTIVE | Skill frontmatter | Protocolo expandido con todos los triggers |
| SubagentStop hook | POST al servidor | Captura pasiva del output (extracción en servidor) |
| Servidor Go | LLM interno | Comparación semántica para dedup |
