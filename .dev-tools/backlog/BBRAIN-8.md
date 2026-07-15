---
key: BBRAIN-8
type: Epic
title: "Search: Snippet/body preview en resultados de search & graph"
status: todo
area: search
labels: [index, mcp]
points: 8
created: 2026-07-15T03:14:04Z
updated: 2026-07-15T03:14:04Z
reviewed_by: ticket-reviewer (autonomous), 2026-07-15
---

## Context
`mem_search`, `mem_related` y `mem_why` — las tools más usadas del catálogo, disparadas varias
veces por sesión — devuelven hoy solo `fact_id` + `title`. Para saber si un hit es relevante, el
agente tiene que hacer un `mem_get` adicional por cada resultado: cada búsqueda cuesta N+1 tool
calls en el caso común "buscar → decidir relevancia".

`facts_fts` ya indexa la columna `body` (`internal/index/index.go:28,80`) y ya existe un helper
`snippet(body, max)` en `internal/app/app.go:291`, usado hoy solo internamente para el prompt de
`wiki_link`. La información para dar un snippet por hit ya se computa o está a un `SELECT` de
distancia — hoy se descarta antes de llegar al agente.

## Acceptance Criteria (child-story outline)
- Story 1 (Search: Add snippet field to mem_search results): `mem_search` (y `mem_candidates`,
  mismo shape `Result`) devuelve un snippet de una o dos líneas del body por resultado, sin
  cortar a mitad de palabra ni perder el término buscado.
- Story 2 (Graph: Add title+snippet to mem_related/mem_why): `mem_related` y `mem_why` devuelven
  `title` + snippet por vecino/edge, vía JOIN a `facts_fts` (hoy `Neighbor`/`Edge` solo tienen
  `fact_id`, salen de la tabla plana `links`).

## Technical scope
- `internal/index/index.go`: `Result` (línea ~226), `Neighbor` (línea ~148), `Edge` (línea ~138),
  `Search`/`SearchAny` (línea ~236+).
- `internal/app/app.go`: `snippet()` helper (línea ~291) — reusar, no reimplementar.
- `internal/mcp/tools.go`: surface de los campos nuevos en los handlers de `mem_search`,
  `mem_related`, `mem_why`, `mem_candidates`.

## Constraints
- Local-first, stdlib-first, pure-Go `modernc.org/sqlite` (sin cgo). Preferir el builtin FTS5
  `snippet()` si no rompe el build no-cgo; si hay duda, reusar el helper Go existente.
- Reuso obligatorio: no reimplementar truncado/snippet.
- Cero dependencias nuevas, cero migración de schema.
- Cap de longitud razonable (~160 chars) — no inflar de forma significativa el payload.
- Out of scope: semantic/embedding search, reranking, hybrid, cambios de scoring/ranking,
  paginación o cambios de límite de resultados.

## Definition of Done
- Ambas Stories mergeadas a master, verdes en CI.
- `mem_search`, `mem_candidates`, `mem_related`, `mem_why` devuelven snippet (y title donde
  falte) sin round-trip adicional de `mem_get` para el caso de triage común.
- Tests que verifiquen snippet no vacío con el término presente, y title en Related/Why.
