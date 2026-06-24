# BBrain — UserPromptSubmit hook (diseño)

Fecha: 2026-06-24
Estado: aprobado (diseño), pendiente de plan de implementación.

## Resumen

BBrain hoy inyecta memoria en dos capas: el bloque estático de `CLAUDE.md`
(protocolo + tools) y el hook `SessionStart` (que corre `bbrain context` y mete
pinned + wiki + recientes). Falta el mecanismo **activo por mensaje** que engram
sí tiene vía `UserPromptSubmit`:

1. **Primer mensaje de la sesión:** forzar la carga de los tools `mcp__bbrain__*`
   (que el harness difiere cuando hay muchos MCP servers) con un `ToolSearch`.
2. **Mensajes siguientes:** un *nudge* temporal cuando hace rato que no se guarda
   nada del proyecto actual.

Este es el gap de mayor ganancia de comportamiento que queda frente a engram. Se
implementa como un **subcomando Go** (`bbrain prompt-submit`), no como un script
shell — BBrain es un binario único y no depende de `jq`/`curl`/`date`.

## Objetivos

- Garantizar que los tools de BBrain estén disponibles al inicio de cada sesión,
  aunque el harness los difiera (la promesa que el bloque de `CLAUDE.md` no puede
  garantizar por sí sola).
- Recordar guardar cuando una sesión activa lleva >15 min sin un `mem_save` del
  proyecto actual, sin volverse ruidoso.
- Nunca bloquear ni demorar perceptiblemente el mensaje del usuario.

## No-objetivos (YAGNI explícito)

- **Persistencia de prompts.** Engram POSTea cada prompt a su servidor para
  adjuntar el prompt de origen a los saves. BBrain no tiene ese consumidor.
- **Knobs de entorno para los thresholds.** Hardcode 5 / 15 / 900 s. Se
  parametriza el día que pique, no antes.
- **Modo Windows-safe.** Engram lo necesita porque su script forkea helpers bajo
  EDR; un binario Go no forkea. Si algún día corre en Windows, el binario ya es
  portable.
- **Forzar `mem_search` en el primer mensaje.** Decidido: el primer mensaje solo
  fuerza el `ToolSearch`. El contexto ya lo dio `SessionStart`; *cuándo* buscar lo
  decide el protocolo del `CLAUDE.md`.

## Arquitectura

Nuevo subcomando registrado como hook `UserPromptSubmit` en `settings.json`,
espejo del `SessionStart` actual:

```
"UserPromptSubmit": [
  { "hooks": [ { "type": "command",
                 "command": "bbrain",
                 "args": ["prompt-submit", "--home", "<memoryDir>"],
                 "timeout": 10 } ] }
]
```

Claude Code invoca el subcomando en cada mensaje, pasando por stdin un JSON con
`{session_id, cwd, prompt}`. El subcomando decide y escribe a stdout un JSON:
`{}` (no inyectar nada) o `{"systemMessage": "..."}`.

**Contrato duro:** siempre `exit 0` y siempre JSON válido en stdout. Cualquier
error interno (JSON malformado, store no abre, FS) ⇒ imprimir `{}` y salir 0.
Un hook que falla o tarda bloquea el mensaje del usuario; eso nunca debe pasar
por un problema de memoria.

## Componentes

### 1. Función pura de decisión

El corazón es una función sin IO, table-testeable:

```
decide(in DecideInput) DecideOutput
```

`DecideInput` agrupa todo lo que el subcomando ya resolvió por IO:
`firstMessage bool`, `sessionAge time.Duration`, `sinceLastSave time.Duration`
(o "desconocido"), `sinceLastNudge time.Duration` (o "nunca").
`DecideOutput` = `{ json string, writeNudgeState bool }`.

Reglas, en orden barato-primero:

1. `firstMessage` ⇒ `json = mensaje-ToolSearch`. (el subcomando ya creó el
   statefile como efecto de IO; `decide` solo produce el string — es la única
   autoridad de los textos de salida, en todos los casos)
2. `sessionAge < 5m` ⇒ `{}`. (proxy de "sesión recién arrancada")
3. `sinceLastNudge < 15m` (cooldown 900 s) ⇒ `{}`. (debounce: no repetir)
4. `sinceLastSave` desconocido (sin facts del proyecto) ⇒ `{}`.
5. `sinceLastSave <= 15m` ⇒ `{}`.
6. resto ⇒ `json = mensaje-nudge`, `writeNudgeState = true`.

El orden importa: las puertas 1-3 son stats/reads O(1), así que la query al
índice (que alimenta `sinceLastSave`) solo se ejecuta cuando de verdad hace falta.

### 2. Estado de sesión (statefiles en `$TMPDIR`)

- `bbrain-claude-<key>-tools-loaded` — su existencia marca "ya no es el primer
  mensaje"; su `mtime` es el proxy de inicio de sesión (≈ primer mensaje).
- `bbrain-claude-<key>-last-nudge` — epoch del último nudge, para el cooldown.

`<key>` = `session_id` sanitizado (`[^a-zA-Z0-9_-]` → `_`); fallback
`<project>-<pid>` si no llega `session_id`.

### 3. "Último guardado" del proyecto

`project` = env `BBRAIN_PROJECT`, si no, `basename(cwd)` (igual que
`mem_current_project`).

`sinceLastSave` = `now - Index.LastSavedAt(project)`, un método nuevo del
índice: `SELECT max(updated_at) FROM facts_fts WHERE project = ?`. Match
**estricto** por `project` (paridad con engram: un save global/personal NO
resetea el reloj del nudge de este proyecto — el nudge mide específicamente
memoria del proyecto). Sin filas para el proyecto ⇒ `sinceLastSave`
"desconocido" ⇒ no nudge.

El hook abre el índice en solo-lectura. Si la query falla por cualquier motivo
(db ocupada por un write del MCP server, o columna ausente en un índice viejo
sin reindexar) ⇒ se trata como "desconocido" ⇒ `{}`. El índice es cache: leerlo
mal nunca debe romper el mensaje del usuario.

### 3b. Cambio de schema del índice (`updated_at` / `created_at`)

`facts_fts` gana dos columnas UNINDEXED: `updated_at` y `created_at` (el `fact`
ya las tiene; `IndexFact` las escribe). El nudge usa solo `updated_at`;
`created_at` se guarda en la **misma** migración para no repetir el baile de
schema cuando una feature de timeline/edad lo pida — es columna muerta (sin
método que la lea) hasta entonces. Decisión consciente, no scope creep: el costo
real es el cambio de schema, que se paga una vez.

**Migración.** FTS5 no soporta `ALTER TABLE ADD COLUMN`, y `CREATE ... IF NOT
EXISTS` no actualiza un `index.db` preexistente. Por eso `reindex` pasa a
**recrear** la tabla (`DROP TABLE` + `CREATE` + repoblar desde los `.md`) en vez
del `DELETE FROM` de `Clear()` — así cualquier cambio de schema se propaga con un
solo `bbrain reindex`. Instalación nueva: `Open` crea el schema correcto de una.
Cerebro existente: un `bbrain reindex` tras actualizar el binario (y mientras
tanto el hook degrada a `{}`, nunca crashea). Tu caso (cerebro ~vacío): el
reindex es instantáneo y sin riesgo — el índice es derivado de los `.md`.

### 4. Registro en `settings.json`

- Nuevo `setup.UserPromptSubmitHookEntry(memoryDir)`, análogo a
  `SessionStartHookEntry`.
- Generalizar el merge/remove de hooks para gestionar `SessionStart` **y**
  `UserPromptSubmit` idempotentemente, reusando el patrón de `isBBrainHook`
  (entrada cuyo `command == "bbrain"` y cuyos args contienen el verbo del hook).
- `install`/`uninstall` agregan/quitan ambas entradas en el mismo paso de
  `merge-settings`.

## Mensajes inyectados (textos exactos)

Primer mensaje (optimizado vía prompt-master para Claude Code):

```
FIRST ACTION — before responding, run this ToolSearch once to load BBrain's memory tools (they are deferred and not yet callable):
select:mcp__bbrain__mem_save,mcp__bbrain__mem_search,mcp__bbrain__mem_get,mcp__bbrain__mem_delete,mcp__bbrain__mem_link,mcp__bbrain__mem_why,mcp__bbrain__mem_related,mcp__bbrain__mem_candidates,mcp__bbrain__mem_current_project,mcp__bbrain__wiki_build,mcp__bbrain__wiki_link,mcp__bbrain__wiki_lint
```

Nudge (optimizado vía prompt-master — nótese la salida explícita para no forzar saves basura):

```
MEMORY CHECK — over 15 minutes since your last save to this project. If anything since then is worth remembering (a decision, discovery, fixed bug, or fact about the user), call mem_save now. If nothing is, ignore this and continue.
```

## Data flow

```
Claude Code --(stdin JSON: session_id, cwd, prompt)--> bbrain prompt-submit
  ├─ ¿existe tools-loaded? no → crearlo, emitir ToolSearch, exit 0
  ├─ sí → reunir: sessionAge(mtime), sinceLastNudge(read), [puertas O(1)]
  ├─ si pasa: project(cwd) → Index.LastSavedAt(project) → sinceLastSave
  ├─ decide(...) → json (+ writeNudgeState)
  └─ si writeNudgeState: escribir last-nudge=now; imprimir json; exit 0
```

## Manejo de errores y rendimiento

- Todo error ⇒ `{}` + `exit 0`. Sin excepciones que escapen.
- `timeout: 10` en el hook como red de seguridad; el subcomando apunta a < 50 ms.
- Una sola query al índice (`max(updated_at)`), y solo cuando las puertas O(1) no cortaron antes.

## Testing

- **`decide(...)` table-driven** (sin IO): primer mensaje · sesión <5m ·
  dentro de cooldown · sin facts del proyecto · último save ≤15m · último save
  >15m fuera de cooldown (único caso que nudgea).
- **Merge de settings**: idempotencia (re-run no duplica), coexistencia con el
  hook `SessionStart`, y `remove` que limpia solo lo de BBrain — espejo de los
  tests existentes de `SessionStart`.
- **Detección de proyecto**: `BBRAIN_PROJECT` gana sobre `basename(cwd)`.
- **Índice `LastSavedAt`**: devuelve el `max(updated_at)` del proyecto, ignora
  otros proyectos, y "desconocido" si no hay filas. Y `reindex` recrea la tabla
  (schema fresco con las columnas nuevas), no solo borra filas.

## Decisiones cerradas

| Decisión | Valor |
|---|---|
| Primer mensaje | solo forzar `ToolSearch` |
| Scope del nudge | proyecto actual (paridad con engram) |
| Inicio de sesión | proxy: `mtime` del statefile tools-loaded |
| Thresholds | sesión 5 min · save 15 min · cooldown 900 s (hardcode) |
| Fuente de "último save" | índice: `max(updated_at) WHERE project=?` (columna nueva, O(1)) |
| Columnas de tiempo en el índice | `updated_at` (usada) + `created_at` (futura) en una sola migración |
| Migración de schema | `reindex` recrea la tabla (DROP+CREATE), no solo `DELETE` |
| Forma | subcomando `bbrain prompt-submit` |
