---
key: BBRAIN-7
type: Story
title: "MCP: Expose mem_archive/mem_unarchive with id-only guardrail"
status: todo
parent: BBRAIN-6
area: mcp
labels: [mcp, backend, test]
points: 3
created: 2026-07-14T23:17:52Z
updated: 2026-07-14T23:17:52Z
reviewed_by: ticket-reviewer (autonomous), 2026-07-14
---

## Context
BBRAIN-6 (Epic) documenta que `app.Archive`/`app.Unarchive` (internal/app/app.go) existen y están testeados pero no están expuestos por MCP — la interfaz primaria del agente. Esta Story cierra ese gap: dos handlers MCP nuevos, guardrail id-only (nunca bulk/filtro), confirmación visible en la respuesta, y `internal/prompthook/prompthook.go` actualizado.

Decisión de scope tomada acá: `app.Archive(id string)`/`app.Unarchive(id string)` toman un solo id, no una lista. El criterio de éxito del PRD pide `ids: string[]` como guardrail de "explicit ids, nunca filtro" (distinto del bulk-por-filtro de `PlanArchive`). Se resuelve así: el schema del tool acepta `ids: string[]`, el handler itera `app.Archive`/`app.Unarchive` por cada id y agrega los resultados — sin tocar la firma singular de `app.Archive`/`app.Unarchive`.

Segunda decisión de scope: `/home/vex/.claude/CLAUDE.md` (bloque ToolSearch global, mencionado en BBRAIN-6 como ubicación espejada) vive **fuera** del repo BBrain — un PR de este repo no puede tocarlo. Se saca del scope de esta Story (ver Autonomy Guide y Definition of Done) y queda como paso manual de seguimiento fuera de este ciclo de dev.

**Nota de numeración**: esta Story se redactó originalmente como BBRAIN-4 (parent BBRAIN-3) pero colisionaba con una Story ya shippeada bajo esa clave (PR #32, mergeada a `origin/master` después de la allocation local). Renumerada a BBRAIN-7, parent BBRAIN-6.

## Acceptance Criteria
AC-1  Given un fact existente no archivado con id conocido
      When se invoca `mem_archive` con `ids: ["<id>"]`
      Then el fact queda archivado (mismo efecto que `app.Archive(id)`)

AC-2  Given la misma invocación de AC-1
      When se inspecciona la respuesta del tool
      Then incluye ese id en la lista de ids archivados

AC-3  Given dos facts existentes no archivados con ids conocidos
      When se invoca `mem_archive` con `ids: ["<id1>", "<id2>"]`
      Then la respuesta reporta un conteo de 2 archivados

AC-4  Given un fact archivado con id conocido
      When se invoca `mem_unarchive` con `ids: ["<id>"]`
      Then el fact deja de estar archivado (mismo efecto que `app.Unarchive(id)`)

AC-5  Given la misma invocación de AC-4
      When se inspecciona la respuesta del tool
      Then confirma ese id como desarchivado con conteo de 1

AC-6  Given el schema de entrada de `mem_archive`/`mem_unarchive` en `DefaultTools()`
      When se inspecciona su InputSchema
      Then el único campo aceptado es `ids: string[]` — sin `type`/`older-than`/`distilled`/`project`/`apply` ni ningún otro filtro o flag de bulk

AC-7  Given un id que no existe
      When se invoca `mem_archive` con ese id incluido en `ids`
      Then el tool no crashea — retorna error o marca ese id puntual como no encontrado en la respuesta

AC-8  Given el mismo caso de AC-7
      When se inspecciona el resultado
      Then ese id no se reporta falsamente como archivado

AC-9  Given un fact recién archivado por `mem_archive`
      When se invoca `mem_get` sobre su id
      Then el fact se devuelve con `archived: true` (comportamiento existente, sin regresión)

AC-10 Given los tools `mem_archive`/`mem_unarchive` agregados a `DefaultTools()`
      When se inspecciona `toolSearchMsg` en `internal/prompthook/prompthook.go`
      Then lista `mem_archive` y `mem_unarchive` junto al resto del catálogo

## Technical scope
- `internal/mcp/tools.go` — nuevo schema id-list (`{"ids":{"type":"array","items":{"type":"string"}},"required":["ids"]}`), `handleMemArchive`/`handleMemUnarchive` (mismo patrón que `handleMemDelete`, iterando sobre `ids`), 2 entradas `Tool{}` en `DefaultTools()` con descripción que ancla el uso a intención explícita del usuario (nunca housekeeping autónomo/bulk).
- `internal/app/app.go` — `Archive`/`Unarchive` reusados sin modificar.
- `internal/prompthook/prompthook.go` — agregar `mem_archive`/`mem_unarchive` a la lista `select:...` de `toolSearchMsg`.
- `internal/mcp/tools_test.go` — tests para los 2 handlers nuevos, mismo patrón que los tests existentes de `handleMemDelete`.
- Fuera de scope (repo boundary): `/home/vex/.claude/CLAUDE.md` — ver Context.

## Constraints
Go stdlib-first (solo sqlite/yaml/atomic). Mirror del shape de handlers MCP existentes — cero superficie de diseño nueva. Guardrail de confianza no negociable: id explícito, nunca bulk/auto, respuesta que confirma. NO exponer `PlanArchive` por MCP. NO auto-archive. NO tocar comportamiento del archive tier en search. NO modificar la firma de `app.Archive`/`app.Unarchive`. NO tocar archivos fuera de `/home/vex/Vex/BBrain`.

## Autonomy Guide
| | Actions |
|---|---|
| **Always** (just do it) | Editar `internal/mcp/tools.go` (nuevos handlers + entradas DefaultTools) y `internal/mcp/tools_test.go`; correr `go test ./internal/mcp` y `go vet ./internal/mcp`; editar `internal/prompthook/prompthook.go` (lista de tools) |
| **Ask first** (confirm with human) | Ninguna acción de esta Story requiere confirmación adicional — todo el diff vive dentro de `internal/mcp` y `internal/prompthook`, mismo shape que código existente |
| **Never** (out of scope) | Exponer `PlanArchive`/bulk-por-filtro por MCP; agregar auto-archive; modificar `app.Archive`/`app.Unarchive`; tocar el comportamiento de `mem_search` respecto a facts archivados; modificar CI/CD; editar `/home/vex/.claude/CLAUDE.md` u otro archivo fuera de `/home/vex/Vex/BBrain` (fuera del repo, fuera del alcance de este PR) |

## Definition of Done
1. `handleMemArchive`/`handleMemUnarchive` en `internal/mcp/tools.go`, registrados en `DefaultTools()` con schema `ids: string[]` id-only.
2. Respuesta confirma ids archivados/desarchivados + conteo (AC-1 a AC-5).
3. `toolSearchMsg` (prompthook.go) actualizado (AC-10).
4. `go test ./...` verde, incluyendo los tests nuevos de `tools_test.go`.
5. Verificación manual: archivar un id real vía MCP y confirmar `mem_get` devuelve `archived:true`.
6. **Seguimiento fuera de esta Story** (no bloquea Definition of Done): actualizar el bloque ToolSearch de `/home/vex/.claude/CLAUDE.md:6-7` con `mem_archive`/`mem_unarchive` — acción manual de Esequiel o de una sesión con acceso a ese archivo, fuera del repo BBrain.
