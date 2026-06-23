# BBrain — Design Spec

**Date:** 2026-06-22
**Status:** Approved (design phase)
**Author:** vex (esequieljara2002@gmail.com)

---

## 1. Resumen

**BBrain** es un sistema de memoria persistente para agentes de IA, en la línea de
[engram](../../../engram), pero con dos cambios fundamentales:

1. **`.md` como source of truth.** Los archivos markdown son la única fuente de
   verdad. SQLite/FTS deja de ser la fuente y pasa a ser un **índice derivado y
   desechable**, reconstruible 100% desde los `.md`.
2. **Patrón LLM-wiki ([Karpathy](https://gist.github.com/karpathy/442a6bf555914893e9891c11519de94f)).**
   Una capa cruda (`raws/`) que BBrain y el usuario escriben, y una capa destilada
   (`wiki/`) que un LLM enchufable mantiene (ingest/lint), con wikilinks razonados
   entre memorias.

BBrain es **un sistema de memoria, nada más** — equivalente a engram en propósito.
El motor de recomendaciones / cowork que el usuario quiere construir a futuro queda
**fuera de scope**.

### Principios

- **Single binary Go**, local-first, zero-deps en el runtime base (FTS5 vía SQLite
  embebido, como engram).
- **`.md` manda.** El índice se borra sin pérdida de datos.
- **Memoria cross-session y cross-project.** Hay **un único brain** en la ubicación
  elegida al instalar. El agente lo lee y escribe vía **MCP tools** desde cualquier
  carpeta o proyecto en el que esté trabajando — no necesita estar parado dentro de
  la carpeta del brain. Los facts se separan por `project` y `scope`
  (`project`/`personal`/`global`), de modo que se puede recordar contexto de un
  proyecto desde otro y filtrar por proyecto cuando hace falta.
- **Correlación de memorias = objetivo central**: wikilinks tipados y razonados.
- **Instalación local.** Sin capa de sync propia; el usuario decide si versiona con
  git o no, y BBrain ni se entera.

```
raws/  = verdad cruda      (lo que BBrain y el usuario escriben)
wiki/  = destilado         (lo que el LLM routine mantiene)
índice = aceleración       (derivado, reconstruible, desechable)
```

---

## 2. Estructura en disco

La genera el TUI al instalar (`bbrain init`), en la ubicación que elige el usuario.
Es **un único brain** que sirve a todos los proyectos; no hay una carpeta por
proyecto. La separación entre proyectos vive en el frontmatter (`project` / `scope`),
no en la jerarquía de carpetas.

```
<memoria>/                       ← ubicación elegida en el TUI install (el "brain")
├── CLAUDE.md                    ← SCHEMA: describe la estructura del brain.
│                                  Solo se usa si abrís un cowork / Claude Code
│                                  PARADO dentro de esta carpeta. NO es la
│                                  integración con el agente (ver §6 · Integración).
├── raws/
│   ├── facts/                   ← .md que crea BBrain (flat, frontmatter rico)
│   │   ├── 2026-06-22-auth-jwt-decision.md
│   │   └── 2026-06-22-postgres-vs-mysql.md
│   └── user-raws/               ← notas crudas del usuario
└── wiki/
    ├── index.md                 ← catálogo de páginas de la wiki
    ├── log.md                   ← append-only: ingests / queries / lints
    └── <subfolders>/            ← entidades, conceptos, comparaciones (wikilinks)

# Índice FTS local, derivado, NO source of truth (gitignored si el usuario versiona).
# Ubicación: <memoria>/.bbrain/index.db  (o ~/.bbrain/<hash-ruta>/index.db)
```

### Roles de los archivos de índice/schema en markdown

- **`CLAUDE.md`** — schema/convenciones del brain. Deja la estructura
  esquematizada **por si abrís un cowork / Claude Code directamente dentro de la
  carpeta del brain**, para que la lea y escriba correctamente. No es el mecanismo
  por el cual los agentes usan BBrain en su trabajo diario — eso son las MCP tools
  + la integración por hooks (ver §6 · Integración con agentes).
- **`wiki/index.md`** — catálogo de páginas de la wiki (una línea por página).
- **`wiki/log.md`** — registro append-only de ingests, queries y lints.
- Los `facts/` **no** tienen un índice `.md`; se descubren por el índice FTS de BBrain.

---

## 3. Modelo de memoria — el fact `.md`

Cada memoria es un `.md` **flat** dentro de `raws/facts/`, nombrado por
`<fecha>-<slug>.md` (o por id). La organización vive en el **frontmatter rico**, no
en la jerarquía de carpetas.

```markdown
---
id: 2026-06-22-auth-jwt-decision
type: decision            # decision|architecture|bugfix|pattern|config|discovery|learning
scope: project            # project|personal|global
project: bbrain
topic_key: architecture/auth-model   # opcional → upsert (reescribe este archivo)
tags: [auth, security]
links:
  - target: "[[postgres-vs-mysql]]"
    relation: supersedes        # relates|depends-on|conflicts-with|supersedes|scoped|compatible
    why: "Esta decisión reemplaza la elección previa de DB: ya no usamos sessions en tabla."
  - target: "[[session-model]]"
    relation: depends-on
    why: "El modelo de auth asume el session-model definido acá."
created_at: 2026-06-22T17:57:00Z
updated_at: 2026-06-22T17:57:00Z
revision_count: 1
---

# Usar JWT con refresh tokens para auth

**What:** ...
**Why:** ...
**Where:** ...
**Learned:** ...
```

### Reglas

- **Source of truth = este archivo.** El índice lo parsea; nunca al revés.
- **`topic_key` presente → upsert**: reescribe el mismo `.md` y sube `revision_count`.
  Sin `topic_key`, cada save crea un archivo nuevo.
- **Dedup** por hash normalizado (project + scope + type + title + content) en ventana
  rodante (como engram), para no duplicar saves idénticos seguidos.
- **Wikilinks razonados**: cada entrada de `links:` es una arista tipada con:
  - `target` — wikilink al otro fact (`[[slug]]`),
  - `relation` — tipo de relación (vocabulario portado de engram),
  - `why` — **obligatorio**: explicación de *por qué* están relacionados.
  El índice reconstruye un grafo tipado con razón desde estos `links:`.

---

## 4. Índice (derivado, desechable)

- **Motor: FTS5 puro** (SQLite embebido). Sin embeddings → mantiene single-binary
  zero-deps. Indexa facts + wiki: `title`, `content`, `type`, `scope`, `project`,
  `tags`, `topic_key`.
- **Reconstruible 100% desde los `.md`**:
  - `bbrain reindex` (manual).
  - Reindex automático al detectar cambios en disco (watcher) o al arrancar si el
    índice falta o está stale.
- Pensado para que editar un `.md` a mano, o que el LLM routine reescriba la wiki,
  se refleje sin pasos manuales.
- **Búsqueda en 3 capas** (como engram): `search` (FTS/BM25) → `context` (reciente) →
  drill-down por sesión.
- **Borrable sin pérdida.** Gitignored si el usuario versiona la memoria.

---

## 5. Correlación + capa wiki (LLM enchufable)

**Modelo de orquestación: BBrain orquesta, LLM enchufable** (equivalente al
`ENGRAM_AGENT_CLI` de engram). BBrain nunca embebe un proveedor; invoca un LLM CLI
configurable.

Flujo:

1. Tras escribir un fact, BBrain corre `FindCandidates` por FTS (léxico, barato) y
   deja correlaciones sugeridas.
2. El **LLM enchufable** hace el trabajo semántico en `bbrain wiki build` /
   `bbrain wiki lint`:
   - lee `raws/` (facts + user-raws),
   - escribe/actualiza páginas en `wiki/`,
   - **puebla los wikilinks razonados** (`target` + `relation` + `why`) en los `.md`,
   - actualiza `wiki/index.md` y appendea a `wiki/log.md`.
3. La routine se dispara desde el binario (`bbrain wiki build`) o desde una routine
   externa del usuario (cowork) — la elección es del usuario.
4. BBrain **busca tanto en `raws/` como en `wiki/`**.

---

## 6. Interfaces

### MCP (lo que usa el agente)

Set core portado de engram (~14 tools), adaptado a `.md`:

| Tool | Qué hace en BBrain |
|------|--------------------|
| `mem_save` | Escribe/upsert un fact `.md` en `raws/facts/` + actualiza índice + sugiere correlaciones (FTS) |
| `mem_search` | FTS sobre `raws/` + `wiki/`, 3 capas |
| `mem_context` | Memorias/sesión recientes (inyección al arrancar) |
| `mem_get` | Devuelve el `.md` completo por id |
| `mem_update` | Edita un fact `.md` existente |
| `mem_delete` | Borra el `.md` (archivo eliminado o tombstone en frontmatter) |
| `mem_link` | Crea/edita un wikilink razonado (`target` + `relation` + `why`) entre dos facts |
| `mem_session_start` / `mem_session_end` / `mem_session_summary` | Ciclo de sesión (episódico) |
| `mem_current_project` | Auto-detección de proyecto (portado de engram) |
| `wiki_build` / `wiki_lint` | Disparan el LLM routine: ingest raws → wiki, poblar links, append log |
| `wiki_search` | Búsqueda específica en `wiki/` |

### CLI

```
bbrain init             # TUI: elige ubicación, configura LLM, genera estructura
bbrain setup <agent>    # Integración por agente (hooks Claude Code / bloque gestionado)
bbrain reindex          # Reconstruye el índice FTS desde los .md
bbrain search <query>
bbrain save <title> <content>
bbrain wiki build|lint  # Dispara el LLM routine
bbrain mcp [--tools=…]   # MCP stdio server
bbrain tui              # Terminal UI
bbrain vault move <dst> # Reubica el brain a otra ruta (post-TUI; ver abajo)
bbrain doctor           # Diagnósticos
```

### TUI install

- Elegir **ubicación** del brain.
- Configurar el **LLM enchufable** (provider / CLI).
- Generar la estructura (`CLAUDE.md`, `raws/facts/`, `raws/user-raws/`,
  `wiki/index.md`, `wiki/log.md`).
- Correr el setup de integración por agente (ver abajo).
- Persistir un **puntero de ubicación** del brain activo (config), para que las
  tools/CLI lo encuentren sin depender de `BBRAIN_HOME` en cada invocación.

### Reubicar el vault (`bbrain vault move`) — posterior al TUI

Capacidad para **mover el brain a la ubicación que el usuario quiera** después de
instalado. Se construye **sobre el TUI** (depende del puntero de ubicación
persistente y del plumbing de integración por agente que introduce el install), por
eso va después:

- Mueve el directorio del brain a `<dst>` (con validación: destino vacío/inexistente,
  mismo filesystem o copia+verificación, no solapar con un brain existente).
- Actualiza el **puntero de ubicación** persistente.
- **Reindexa**: la columna `path` del FTS guarda rutas absolutas, así que un move las
  invalida; el reindex las regenera (limpio porque el índice es derivado/desechable).
- Refresca los **bloques de integración por agente / hooks** que embeben la ruta
  antigua (managed `BEGIN/END`), sin pisar lo demás.
- El TUI ofrece la misma acción ("mover vault") además del comando CLI.

### Integración con agentes (cómo el agente usa BBrain en su trabajo diario)

Esto es **distinto** del `CLAUDE.md` schema del brain. El objetivo es que el agente
pueda **leer y escribir memoria cross-session / cross-project vía MCP tools** desde
cualquier carpeta, y que sepa cuándo hacerlo (el "Memory Protocol"). Replicamos el
patrón de engram:

**Claude Code → hooks efímeros (no toca el CLAUDE.md global del usuario).**
Un plugin con `hooks.json`:

| Hook | Qué hace |
|------|----------|
| `SessionStart` (startup/clear/compact) | Asegura el binario corriendo, abre sesión, e **inyecta el Memory Protocol + contexto reciente por stdout** (`additionalContext`). Efímero, por sesión. |
| `UserPromptSubmit` | 1er mensaje: fuerza el `ToolSearch` que carga las MCP tools (deferred). Mensajes siguientes: *nudge* para guardar si pasó el umbral de tiempo. |
| `SubagentStop` / `Stop` | Cierre de sesión / captura final. |

Más un **Skill** "ALWAYS ACTIVE" con el protocolo de uso.

**Agentes sin sistema de hooks → bloque gestionado con marcadores idempotentes.**
`bbrain setup <agent>` inserta un bloque Memory Protocol en su archivo de
instrucciones global, delimitado para poder actualizarlo/quitarlo sin pisar lo demás:

```
<!-- BEGIN BBRAIN MEMORY PROTOCOL — managed by bbrain setup -->
...
<!-- END BBRAIN MEMORY PROTOCOL -->
```

Destinos por agente (igual que engram): `~/.gemini/GEMINI.md`,
`~/.codeium/windsurf/memories/global_rules.md`, `~/.qwen/QWEN.md`,
`~/.config/kilo/AGENTS.md`, archivo informativo para Cursor, etc.

**Regla de oro:** el `CLAUDE.md` del brain (schema) y la integración con el agente
(hooks / bloque gestionado / MCP tools) son **dos cosas separadas** y no se pisan.
BBrain nunca escribe en el `CLAUDE.md` global del usuario en Claude Code.

---

## 7. Reuso de engram vs. nuevo

| Reusamos (referencia / port desde engram) | Nuevo en BBrain |
|---|---|
| Esqueleto MCP server (stdio) | Capa `.md` como source of truth (read/write/parse + frontmatter) |
| TUI (Bubbletea) | Reindex desde `.md` + watcher |
| Project auto-detect | Grafo de wikilinks **razonados** (`relation` + `why`) en frontmatter |
| Formato de observación (What/Why/Where/Learned) | Capa `wiki/` + orquestación del LLM routine |
| LLM runner enchufable (`ENGRAM_AGENT_CLI`) | `CLAUDE.md` como schema generado del brain |
| Integración por agente: hooks Claude Code + bloque gestionado (BEGIN/END) | Brain único cross-project servido vía MCP desde cualquier carpeta |
| FTS5 + dedup + topic_key upsert (sobre índice derivado) | — |

Es **proyecto nuevo en Go**, con engram como referencia: portamos piezas cuando
conviene, no forkeamos.

---

## 8. Fuera de scope (MVP)

- **Sync** (chunks/manifest/cloud de engram) — no; local-first.
- **Git** — decisión del usuario; BBrain no se entera.
- **Embeddings / búsqueda semántica en el índice** — no; la correlación semántica la
  hace el LLM routine. (Posible extensión futura: índice híbrido configurable.)
- **Motor de recomendaciones / cowork** — proyecto futuro del usuario, aparte.

---

## 9. Decisiones clave (registro)

| Decisión | Elección |
|---|---|
| Source of truth | `.md` (raws + wiki); índice derivado y desechable |
| Modelo de memoria | LLM-wiki de Karpathy: `raws/facts` + `raws/user-raws` + `wiki/` |
| Base de código | Proyecto nuevo en Go, engram como referencia |
| Motor de correlación | FTS5 léxico + correlación semántica vía LLM routine |
| Layout de facts | Flat, frontmatter rico, nombre por `<fecha>-<slug>` |
| Schema / índices `.md` | `CLAUDE.md` (schema) + `wiki/index.md` + `wiki/log.md` |
| Orquestación LLM | BBrain orquesta, LLM enchufable (estilo `ENGRAM_AGENT_CLI`) |
| Wikilinks | Tipados y **razonados** (`target` + `relation` + `why` obligatorio) |
| Alcance de la memoria | Brain único, **cross-session y cross-project**; separación por `project`/`scope` en frontmatter |
| Ubicación del vault | Reubicable (`bbrain vault move`), **posterior al TUI**: puntero de ubicación persistente + reindex (paths absolutos) + refresco de integración por agente |
| `CLAUDE.md` del brain | Schema, solo para abrir un cowork dentro de la carpeta del brain; **no** es la integración con el agente |
| Integración con agentes | Claude Code: hooks efímeros (no toca CLAUDE.md global). Otros: bloque gestionado BEGIN/END. Acceso real vía MCP tools |
| Sync | Ninguno; instalación local, git opcional a cargo del usuario |
```
