---
key: BBRAIN-6
type: Epic
title: "MCP: Archive/unarchive con guardrail de visibilidad"
status: in-progress
area: mcp
labels: [mcp, backend]
created: 2026-07-14T23:12:57Z
updated: 2026-07-15T00:30:06Z
reviewed_by: ticket-reviewer (autonomous), 2026-07-14
---

## Context
El fact-lifecycle archival tier se shipeó (PR #28) pero solo por CLI (`bbrain mem archive`). El catálogo MCP (`internal/mcp/tools.go`, `DefaultTools()`) no expone `mem_archive`/`mem_unarchive`, aunque `app.Archive`/`app.Unarchive` ya existen y están testeados (`app_test.go`). Como `CLAUDE.md` declara que MCP es la interfaz primaria del agente, la capacidad más nueva de BBrain queda inalcanzable desde donde más se usa — Esequiel tiene que salir a un shell para archivar. Además, la lista de tools está hardcodeada en `internal/prompthook/prompthook.go` (`toolSearchMsg`) y espejada en el bloque ToolSearch de `/home/vex/.claude/CLAUDE.md` (líneas 6-7, global privado — **no** en el `CLAUDE.md` del repo) — cualquier tool nuevo que no toque ambos lugares queda indescubrible.

Verificado en el codebase: `app.Archive(id string) (fact.Fact, error)` y `app.Unarchive(id string) (fact.Fact, error)` (internal/app/app.go) toman un solo id, no una lista — a diferencia de lo que el PRD asumía por analogía con `handleMemDelete`. `handleMemDelete` (internal/mcp/tools.go) también opera sobre un `id` singular (`schemaID`), no una lista. La Story hija debe resolver si el schema `ids: string[]` del PRD se implementa iterando `app.Archive` por id, o si el criterio de éxito se reinterpreta como singular — no bloqueante para este Epic.

Debate verdict: GO (4/4 tras concesión de User) — id-only, confirma, nunca bulk/auto; cierra contradicción MCP-first; #3 del run.

**Nota de numeración**: este Epic se redactó originalmente como BBRAIN-3 pero colisionaba con un Epic ya shippeado bajo esa clave (PR #32, "Search scoping and browse"), mergeado a `origin/master` después de la allocation local. Renumerado a BBRAIN-6 tras el merge.

## Acceptance Criteria (child-story outline)
Story BBRAIN-7 cubre el scope completo:
- `mem_archive`/`mem_unarchive` en `DefaultTools()` envolviendo `app.Archive`/`app.Unarchive`.
- Guardrail id-only (nunca bulk/filtro/auto — `PlanArchive` se queda en CLI).
- Respuesta que confirma explícitamente ids archivados + conteo.
- `toolSearchMsg` (prompthook.go) y bloque ToolSearch de `/home/vex/.claude/CLAUDE.md:6-7` actualizados con los tools nuevos.
- `mem_get` sobre id archivado sigue devolviendo `archived: true` (no regresión).
- Tests de handler siguiendo el patrón de `tools_test.go`.

## Technical scope
- `internal/mcp/tools.go` — `DefaultTools()`, nuevos handlers `handleMemArchive`/`handleMemUnarchive`.
- `internal/app/app.go` — `Archive`/`Unarchive` ya existen y testeados, se envuelven sin modificar.
- `internal/prompthook/prompthook.go` (`toolSearchMsg`) — agregar los tools nuevos a la lista.
- `/home/vex/.claude/CLAUDE.md:6-7` (bloque ToolSearch, global privado) — agregar los tools nuevos.
- `internal/mcp/tools_test.go` — tests de los nuevos handlers.

## Constraints
Go stdlib-first (solo sqlite/yaml/atomic). Mirror del shape de handlers MCP existentes — cero superficie de diseño nueva. Guardrail de confianza no negociable: id explícito, nunca bulk/auto, respuesta que confirma. NO exponer `PlanArchive` por MCP. NO auto-archive. NO tocar comportamiento del archive tier en search (ya existente e intencional).

## Definition of Done
Story BBRAIN-7 shippeada: handlers registrados en `DefaultTools()`, guardrail id-only enforced, respuesta confirma ids+conteo, docs (`toolSearchMsg` + `/home/vex/.claude/CLAUDE.md` ToolSearch) actualizadas, tests verdes (`go test ./...`).
