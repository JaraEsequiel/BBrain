---
key: BBRAIN-11
type: Epic
title: Auto-reindex de facts hand-editados (staleness del índice)
status: todo
area: index
labels: [index, mcp, watch]
points: 8
created: 2026-07-15T11:14:55Z
updated: 2026-07-15T11:14:55Z
reviewed_by: ticket-reviewer (autonomous), 2026-07-15
---

## Context
El pitch central del README de BBrain es "los `.md` son la fuente de verdad — leelos, grepealos, editalos a mano". Pero si Esequiel edita un `.md` a mano — un workflow que el producto anuncia — `mem_search` sigue devolviendo la versión vieja hasta que alguien corre `bbrain reindex` o el daemon `bbrain watch` a mano. Ni el install wizard ni el quickstart mencionan `watch`. El índice SQLite/FTS5 es derivado; hoy no se reconcilia contra los `.md` en el punto donde alguien lee.

Esto no es un riesgo hipotético — es un bug felt que ya cambió comportamiento: el propio `BBrain/CLAUDE.md` instruye a las sesiones a NO hand-editar `raws/` y usar solo las MCP tools. Esa instrucción es un workaround baked-in para exactamente este bug.

El mecanismo de detección ya existe y está huérfano: `watch.FactsFingerprint` (stat-walk barato) + `a.Reindex()` alimentan `bbrain watch` (`cmd/bbrain/main.go:642`), un loop de polling foreground funcional — cableado a ni install ni setup.

## Acceptance Criteria (child-story outline)
- Story BBRAIN-12 cubre el alcance completo: reconciliación automática de facts hand-editados durante una sesión MCP viva (reusando `watch.FactsFingerprint` + `Reindex`), sin costo de reindex completo en el hot path de cada tool call, portable (pure-Go, sin daemon OS-level), sin nuevas dependencias, y una nota de una línea en el README sobre el borde CLI-only no cubierto (edición sin sesión MCP viva).

## Technical scope
`internal/watch` (FactsFingerprint, ya existe), `internal/mcp/server.go` (proceso `bbrain mcp`), `internal/app` (Reindex/App), `cmd/bbrain/main.go:642` (bbrain watch, referencia). Mecanismo final (Opción B: goroutine in-process vs Opción C: reconcile-on-call) es una decisión de diseño abierta que resuelve la Story vía /vex:design, no esta Epic.

## Constraints
Local-first, pure-Go, sin cgo, portable (constraint duro de Esequiel — nada atado a una sola máquina). Modelo `.md`=fuente de verdad / índice=derivado debe honrarse: la reconciliación reconcilia contra el `.md`, no mantiene un artefacto derivado "caliente" por fe. Reuso obligatorio de `internal/watch` — no reimplementar detección de staleness. Descartado: daemon a nivel OS (no portable).

## Definition of Done
Story BBRAIN-12 implementada, testeada y en review; README actualizado con la nota del borde CLI-only; decisión de diseño (B vs C) documentada en un ADR antes de la implementación.
