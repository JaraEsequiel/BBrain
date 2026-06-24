# BBrain — Hint de conflictos/duplicados al guardar (diseño)

Fecha: 2026-06-24
Estado: aprobado (diseño), pendiente de plan.
Track: **Spec B** (Gap 3). Paralelo a Spec A (`...-continuity-protocol-design.md`).
**Frontera de scope (dura):** este spec NO toca `internal/setup/setup.go` ni el
`ClaudeMDBlock`. El surfacing va en la **respuesta del tool `mem_save`** (capa
`mcp`/`app`), no en el texto de protocolo de CLAUDE.md. Eso mantiene a A y B
disjuntos para correr en sesiones paralelas sin conflicto de merge.

## Resumen

Engram corre un LLM-auditor en cada save que clasifica la observación nueva
contra las existentes (`conflicts_with`/`supersedes`/`scoped`/`related`/
`compatible`/`not_conflict`). BBrain tiene las piezas — `mem_candidates`
(similares léxicos no-linkeados) y `wiki_link` (LLM que propone esas MISMAS
relaciones) — pero corren **on-demand**, no al guardar. Resultado: el agente puede
guardar un fact que duplica o contradice uno existente y no se entera hasta correr
`wiki_link`.

Este spec cierra el 80% del valor sin meter el LLM al hot path: al guardar, se
devuelven los **candidatos similares** en la respuesta de `mem_save` para que el
agente linkee/reconcilie en el momento.

## Objetivos

- Que `mem_save` devuelva, junto al fact guardado, los facts existentes
  léxicamente similares y aún no linkeados (si hay, sobre un umbral).
- Que el agente pueda actuar de inmediato (`mem_link` con `conflicts-with`/
  `supersedes`, o `mem_save` con el mismo `topic_key` para upsert) en vez de
  duplicar/contradecir en silencio.
- Cero latencia LLM en el save; cero cambios al texto de protocolo (frontera con A).

## No-objetivos (YAGNI explícito)

- **LLM en el path de `mem_save`.** Es lo que hace engram (server-side, async).
  En BBrain el LLM solo lo usa el wiki; meterlo al save lo vuelve lento y acopla
  el save al `$BBRAIN_AGENT_CLI`. Se rechaza.
- **Mecanismo nuevo de detección.** Se reusa `app.Candidates` (que ya excluye lo
  ya-linkeado) y, para la clasificación semántica, el `wiki_link` existente.
- **Tocar `ClaudeMDBlock` / `setup.go`.** Frontera con Spec A. El agente reacciona
  a los candidatos en el output del tool sin necesidad de instrucción nueva en
  CLAUDE.md (los LLMs reaccionan naturalmente al resultado de un tool).

## Decisión de diseño (la clave de este spec)

Tres opciones consideradas:

| Opción | Qué | Veredicto |
|---|---|---|
| **(a) LLM síncrono en save** | clasificar new-vs-existing con `$BBRAIN_AGENT_CLI` en cada `mem_save` | ❌ lento en el hot path, acopla save↔LLM; es justo lo que BBrain evita |
| **(b) Solo on-demand** | no cambiar nada; `wiki_link` ya clasifica | ❌ no cierra el gap: el conflicto no se cacha cuando se introduce |
| **(c) Hint léxico al guardar** *(elegida)* | `mem_save` devuelve candidatos similares (sin LLM); semántica queda en `wiki_link` | ✅ lazy, rápido, reusa lo que hay, frontera intacta |

> `ponytail:` el hint léxico NO cacha contradicciones con poco solape de palabras
> (mismo significado, otras palabras) — eso lo cacha `wiki_link` on-demand. Upgrade
> path si el hint resulta impreciso/ruidoso: gatear los candidatos con una
> clasificación LLM barata reusando el vocabulario de `wiki_link`. Heavy, diferido.

## Arquitectura

Un solo punto de cambio: el handler `handleMemSave` en `internal/mcp/tools.go`.

```
handleMemSave(input):
    fact = app.Save(input)                      # como hoy
    cands = app.Candidates(fact.ID, limit=N)    # YA existe; excluye lo ya-linkeado
    resp = factView(fact)
    if len(cands) > 0:
        resp.related = [ {id, title, type, score} for c in cands ]  # solo sobre umbral
    return resp
```

- `app.Candidates(id, limit)` ya existe (lo usa el tool `mem_candidates`) y ya
  excluye facts ya-linkeados → para un fact recién guardado devuelve los similares
  existentes. Reuso directo, sin lógica nueva de matching.
- El umbral/limit evita ruido: surfacear solo los top-N por score y/o sobre un
  piso de similitud. Valor exacto a calibrar (ver Preguntas abiertas).
- `factView` (la forma de la respuesta de `mem_save`) gana un campo opcional
  `related` (omitido si vacío) — cambio aditivo, no rompe consumidores.

## Superficie de archivos

- `internal/mcp/tools.go` — `handleMemSave` + el campo `related` en la respuesta.
- `internal/mcp/tools_test.go` — tests.
- (Lectura, sin cambios) `internal/app` `Candidates`, `internal/index` query de candidatos.

**No toca:** `setup.go`, `ClaudeMDBlock`, `app.Save` (firma), `llm`. Disjunto de Spec A.

## Testing

En `internal/mcp/tools_test.go`:
- Guardar un fact, luego guardar otro léxicamente similar (mismo dominio) →
  la respuesta del segundo `mem_save` incluye `related` con el id del primero.
- Guardar un fact sin similares → `related` ausente/vacío.
- Un fact ya linkeado no reaparece como candidato (lo garantiza `Candidates`).

## Preguntas abiertas (para la sesión paralela que ejecute B)

1. **Umbral y N**: ¿cuántos candidatos y con qué piso de score? Calibrar para que
   no sea ruidoso (probablemente top 3, score > X). Decisión empírica del implementador.
2. **Forma de `related`**: ¿solo `{id, title}` o también `type`/`score`? Mínimo útil
   = `id` + `title` (para que el agente decida sin otro lookup).

## Decisiones cerradas

| Decisión | Valor |
|---|---|
| Estrategia | hint léxico al guardar (opción c), sin LLM en el save |
| Reuso | `app.Candidates` existente (excluye ya-linkeados) |
| Clasificación semántica | queda en `wiki_link` on-demand (no se duplica) |
| Surfacing | campo `related` en la respuesta de `mem_save` (capa mcp) |
| Frontera | NO `setup.go`/`ClaudeMDBlock` — disjunto de Spec A |
