---
key: BBRAIN-12
type: Story
title: "MCP: auto-reindex de facts hand-editados en sesión viva"
status: todo
parent: BBRAIN-11
area: index
labels: [index, mcp, watch]
points: 5
created: 2026-07-15T11:14:55Z
updated: 2026-07-15T11:14:55Z
reviewed_by: ticket-reviewer (autonomous), 2026-07-15
---

## Context
`mem_search`/`mem_get` sirven contra el índice FTS5 derivado (`internal/index`), que hoy solo se actualiza vía `mem_save`/`mem_delete` (rutas que ya mutan el índice) o manualmente vía `bbrain reindex`. Si Esequiel edita un fact `.md` a mano en `raws/facts/` durante una sesión MCP viva, el índice queda stale hasta el próximo reindex manual — y `mem_search` sirve el fact viejo sin ninguna señal de error. El mecanismo de detección (`watch.FactsFingerprint`, un stat-walk sobre `raws/facts/`) y la reconciliación (`a.Reindex()`, full Reset+rebuild — `internal/app/app.go:56`) ya existen y alimentan `bbrain watch` (`cmd/bbrain/main.go:642`), un comando standalone que nadie invoca junto al servidor MCP.

La decisión de diseño abierta del Epic (Opción B: goroutine in-process en el arranque de `Serve` — `internal/mcp/server.go` — vs Opción C: reconcile-on-call sincrónico en `mem_search`/`mem_get`) será resuelta mediante `/vex:design` previo a la implementación de esta Story — el either/or se documenta en un ADR antes de que este Story entre en write-plan.

## Acceptance Criteria
AC-1  Given una sesión `bbrain mcp` viva y un fact `.md` editado a mano en `raws/facts/`
      When pasa el intervalo de detección del mecanismo elegido (goroutine periódica u check on-call)
      Then una llamada subsiguiente a `mem_search`/`mem_get` sobre ese fact devuelve el contenido actualizado sin comando manual

AC-2  Given el mecanismo elegido es la Opción B (goroutine in-process)
      When la goroutine dispara un `Reindex()` mientras un `mem_save` está en curso
      Then no hay corrupción ni deadlock del índice SQLite (acceso concurrente serializado, con mutex explícito si `modernc.org/sqlite` no lo garantiza)

AC-3  Given ningún fact `.md` cambió desde el último reindex
      When se cumple el intervalo/check del mecanismo elegido
      Then no se ejecuta un reindex completo (cero costo en el hot path cuando no hay staleness)

AC-4  Given un fact `.md` es editado a mano sin ninguna sesión `bbrain mcp` viva (edición CLI-only)
      When el usuario corre `mem_search` en una sesión posterior sin haber corrido `bbrain reindex`
      Then el resultado puede seguir stale — este borde queda documentado en el README, no resuelto por esta Story

## Technical scope
`internal/watch` (`FactsFingerprint` — stat-walk existente, reusar sin modificar su firma pública salvo necesidad probada) · `internal/mcp/server.go` (`Serve` — punto de arranque del proceso `bbrain mcp`, candidato para Opción B) · `internal/app/app.go:56` (`Reindex()` — full Reset+rebuild; Opción C requeriría una vía de reindex incremental, hoy inexistente) · `cmd/bbrain/main.go:642` (`bbrain watch` — implementación de referencia del loop, no tocar su comportamiento standalone) · `internal/index` (si Opción C: necesita exponer reindex de facts individuales) · `README.md` (nota de una línea sobre el borde CLI-only, AC-4).

## Constraints
Pure-Go, sin cgo, portable — ni Opción B ni C pueden introducir un daemon OS-level o un archivo de servicio específico de plataforma. Reuso obligatorio de `watch.FactsFingerprint`: no reimplementar detección de staleness desde cero. El modelo `.md`=fuente de verdad debe preservarse: el reindex reconcilia contra el `.md`, nunca al revés. Sin nuevas dependencias. La decisión B vs C se resuelve en un ADR de /vex:design antes de este write-plan — el Story no se implementa sobre un mecanismo no decidido.

## Autonomy Guide
| | Actions |
|---|---|
| **Always** (just do it) | Editar `internal/mcp/server.go`, `internal/watch/*`, `internal/app/app.go`, `README.md` dentro del alcance de esta Story; agregar/actualizar tests unitarios e de integración en esos paquetes; correr `go build ./...`, `go vet ./...`, `go test ./...`; arreglar errores de lint/build introducidos por el cambio |
| **Ask first** (confirm with human) | Cambiar la firma pública de `watch.FactsFingerprint` o `app.Reindex()` si otros callers dependen de ellas; introducir un mutex/lock nuevo en `app.App` que afecte otros callers del índice; cambiar el intervalo default de polling si Opción B lo hereda de `bbrain watch` |
| **Never** (out of scope) | Implementar un daemon a nivel OS (systemd/launchd); tocar `cmd/bbrain/main.go:642` (`bbrain watch`) más allá de referencia de lectura; agregar dependencias nuevas; modificar CI/CD o el installer/setup wizard |

## Definition of Done
ADR de /vex:design resolviendo B vs C, mergeado antes del write-plan · AC-1 a AC-4 con test unitario/integración pasando · `go build ./... && go vet ./... && go test ./...` verde · README actualizado con la nota del borde CLI-only (AC-4) · sin dependencias nuevas en `go.mod`.
