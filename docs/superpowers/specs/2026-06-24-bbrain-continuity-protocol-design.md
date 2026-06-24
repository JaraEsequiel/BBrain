# BBrain — Continuity protocol: post-compaction recovery + session close (diseño)

Fecha: 2026-06-24
Estado: aprobado (diseño), pendiente de plan.
Track: **Spec A** (conjunto Gap 1 + Gap 2). Paralelo a Spec B (`...-onsave-conflict-detect-design.md`), archivos disjuntos.

## Resumen

Engram tiene dos comportamientos de continuidad que a BBrain le faltan, y ambos
se resuelven en **un solo archivo** (`internal/setup/setup.go`), reusando los
tools que ya existen (`mem_save`/`mem_search`) — sin modelo de sesión, sin tools
nuevos, sin server:

1. **Recuperación post-compactación (Gap 1).** El hook `SessionStart` de BBrain
   usa `matcher: "startup|resume"`, así que **no dispara en `compact` ni `clear`**:
   cuando Claude compacta una sesión larga, no se re-inyecta nada (ni pinned, ni
   wiki, ni recientes) y el agente sigue ciego. Engram engancha `compact` aparte.
2. **Cierre de sesión (Gap 2).** El bloque de CLAUDE.md tiene SAVE/SEARCH/FORMAT
   pero no la instrucción de guardar un resumen de handoff antes de "done".

## Objetivos

- Que el contexto del cerebro se re-inyecte tras `compact` y `clear`, no solo en
  `startup`/`resume`.
- Que el agente guarde un resumen de handoff al cerrar una sesión con trabajo real.
- Que tras una compactación el agente preserve lo compactado y recupere contexto
  antes de seguir.
- Todo dentro de `setup.go` (matcher + texto de protocolo), para no colisionar con
  Spec B.

## No-objetivos (YAGNI explícito)

- **Modelo de sesión / tools de sesión.** Engram tiene `mem_session_summary`,
  `mem_session_start/end`, `mem_timeline`, `mem_stats` + records de sesión en su
  server. BBrain es fact-céntrico: el resumen es un `mem_save` normal con
  `type: "session-summary"`. Nada de eso se agrega.
- **Hooks `Stop` / `SubagentStop`.** El `Stop` de engram solo hace bookkeeping en
  su server (BBrain no tiene server). La captura pasiva de `SubagentStop` choca con
  la filosofía "el agente decide qué guardar" — se descarta a propósito.
- **Inyección del POST-COMPACTION desde el hook.** Ver "Decisión de diseño": el
  texto va estático en el bloque; la vía hook queda como upgrade documentado.

## Arquitectura

Dos cambios, ambos en `internal/setup/setup.go`:

### 1. Ampliar el matcher de SessionStart (Gap 1)

`SessionStartHookEntry` hoy:
```
"matcher": "startup|resume"
```
pasa a:
```
"matcher": "startup|resume|clear|compact"
```
Con eso, `bbrain context` (que el hook ya ejecuta) se re-inyecta también tras un
`/clear` y tras una compactación — restaurando pinned + wiki + recientes en el
momento en que el agente más los necesita. Cero cambios al comando `context`.

### 2. Dos secciones nuevas en `ClaudeMDBlock` (Gap 2 + el "qué hacer" de Gap 1)

Se agregan al final del bloque gestionado, después de la sección FORMAT y antes
de la línea del wiki backend, con el mismo estilo imperativo que SAVE/SEARCH.
(Recordar: el bloque es un raw string literal de Go → **sin backticks**.)

## Textos exactos (optimizados vía prompt-master para Claude Code)

SESSION CLOSE:

```
### SESSION CLOSE — before you say "done", if the session did real work (decisions, code, fixes, discoveries — not trivial chat or a single lookup):

Call mem_save ONCE, type "session-summary", with a handoff:
- Goal: what this session set out to do
- Accomplished: what got done, with key details
- Next steps: what's left for the next session
- Relevant files: paths touched and why

If nothing is worth handing off, skip it — do not save filler.
```

POST-COMPACTION:

```
### POST-COMPACTION — if you see a context compaction or reset, do this FIRST, in order, before continuing:

1. mem_save the compacted summary as a session-summary fact (same fields as SESSION CLOSE) — preserve what was accomplished before the context was cut.
2. mem_search with keywords from the user's current task to pull back the facts you just lost.
3. Only THEN continue the user's request.

Skipping these means working blind.
```

## Decisión de diseño: POST-COMPACTION estático, no hook-inyectado

Engram inyecta su instrucción post-compactación desde un script de hook
(`post-compaction.sh`, enganchado a `SessionStart: compact`). BBrain podría hacer
lo mismo haciendo que `bbrain context` lea el stdin del hook, detecte
`source == "compact"` y agregue los pasos. **Se descarta** porque:

- Metería `app.go`/`cmdContext` en este spec, **rompiendo la disjunción con Spec B**
  (que edita `app.Save`). El objetivo de mantener todo en `setup.go` es que A y B
  corran en sesiones paralelas sin conflicto de merge.
- El bloque de CLAUDE.md **persiste a través de la compactación** (es system
  context / project instructions, no la conversación que se compacta), así que el
  texto estático sigue presente justo cuando hace falta. El cambio de matcher ya
  re-inyecta el contexto del cerebro automáticamente.

> `ponytail:` upgrade path si el testing real muestra que la instrucción estática
> no dispara de forma fiable tras compactar: hacer que `bbrain context` lea el
> SessionStart stdin y, si `source=="compact"`, anteponga los 3 pasos. Es un
> cambio aditivo que NO rompe nada de este spec.

## Propagación

Ambos cambios viven en el binario. Igual que cualquier cambio a `ClaudeMDBlock` o
al hook: **rebuild + `bbrain install`** reescribe el bloque gestionado (entre los
markers) y el hook de `settings.json` (con el matcher nuevo). Una sesión nueva de
Claude lee ambos. (Recordatorio: `install` ya reindexa — no relacionado, pero el
reinstall refresca todo de una.)

## Testing

En `internal/setup/setup_test.go` (package `setup`):
- **Matcher**: `SessionStartHookEntry(...)` / `MergeSettingsHook(...)` produce un
  SessionStart cuyo matcher contiene `"compact"` (y `"clear"`). Asserción de
  substring sobre el JSON, espejo del test existente `TestSessionStartHookAndMerge`.
- **Bloque**: `ClaudeMDBlock(...)` contiene `"SESSION CLOSE"` y `"POST-COMPACTION"`
  (extender la tabla `want` del test `TestClaudeMDBlockMentionsToolsAndMarkers`).
- **Idempotencia**: re-merge del settings sigue dando exactamente 1 entrada
  SessionStart (el matcher más ancho no rompe `isBBrainHook`, que matchea por
  command+verbo `context`, no por matcher).

## Decisiones cerradas

| Decisión | Valor |
|---|---|
| Recuperación post-compactación | matcher `+clear\|compact` (re-inyecta `bbrain context`) |
| Instrucción post-compactación | texto estático en el bloque (no hook), upgrade path documentado |
| Cierre de sesión | sección SESSION CLOSE → `mem_save type session-summary`, con escape |
| Resumen de sesión | fact normal `type: "session-summary"`, sin tools ni modelo de sesión |
| Superficie de archivos | solo `internal/setup/setup.go` (+ su test) — disjunto de Spec B |
| Hooks Stop/SubagentStop | descartados (sin server / contra filosofía) |
