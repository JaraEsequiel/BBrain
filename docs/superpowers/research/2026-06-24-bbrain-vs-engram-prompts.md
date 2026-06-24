# BBrain vs Engram — Comparación de prompts de memoria

Análisis de en qué difiere lo que cada sistema inyecta en Claude y las implicaciones.

---

## Qué inyecta cada sistema

### BBrain

**1. CLAUDE.md block** (estático, permanente en el proyecto):
```
## BBrain memory
This project uses BBrain for durable memory (brain at <brainHome>). The bbrain MCP server exposes:
- mcp__bbrain__mem_save / mem_search / mem_get / mem_delete — save and recall facts.
- mcp__bbrain__mem_link / mem_why / mem_related / mem_candidates — the reasoned graph.
- mcp__bbrain__wiki_build / wiki_link / wiki_lint — distil and maintain the wiki.

Save durable decisions and learnings via mem_save; search with mem_search before answering.
The wiki LLM backend is $BBRAIN_AGENT_CLI -> <adapterPath>. Workflow: build -> link -> rebuild; wiki_lint --fix for consistency.
```

**2. SessionStart hook output** (dinámico, por sesión — `bbrain context`):
```
# BBrain memory context

## About you & pinned context
[cuerpo completo de los facts pinneados]

## Wiki index
[contenido de wiki/index.md]

## Recent facts
- [type] Title (project) — id <id>
- ...  (top 10 por updated_at)
```

**Skills disponibles:** `bbrain-recall` y `bbrain-remember` — on-demand, el usuario o Claude los invoca explícitamente.

---

### Engram

**1. SessionStart hook** (dinámico, por sesión):
- Bloque de protocolo obligatorio (~30 líneas con triggers, self-check, session close)
- Contexto de sesiones previas del servidor (`/context?project=...`)

**2. UserPromptSubmit hook** (por mensaje):
- Primer mensaje: `systemMessage` con ToolSearch forzado + instrucción de llamar `mem_context`
- Mensajes siguientes: nudge si han pasado >15 min sin guardar (debounce 900 s)

**Skill `engram-memory`:** ALWAYS ACTIVE — triggers detallados, formato de guardado, protocolo de cierre de sesión, manejo de compaction.

---

## Diferencias punto a punto

| Dimensión | BBrain | Engram |
|---|---|---|
| **Instrucciones de comportamiento** | 2 líneas ("save… recall…") | ~30 líneas de protocolo obligatorio con triggers detallados y self-check |
| **Triggers de guardado proactivo** | Ninguno explícito | 4 categorías enumeradas + self-check después de cada tarea |
| **Carga de tools** | Depende de que los tools no estén diferidos | Fuerza ToolSearch en el primer mensaje de cada sesión |
| **Nudge temporal** | No existe | Cada >15 min sin `mem_save`, inyecta recordatorio (debounced) |
| **Protocolo de cierre** | No existe | `mem_session_summary` obligatorio antes de "done" |
| **Post-compaction** | No existe | Instrucción explícita de guardar resumen antes de continuar |
| **Contexto dinámico** | Facts pinneados + wiki index + 10 recientes (como bullets) | Últimas observaciones del proyecto desde servidor HTTP |
| **Confirmaciones del usuario** | No cubre | Trigger explícito: "sounds good", "agreed", "no better X" → guardar |
| **Skill** | On-demand (`/bbrain-recall`, `/bbrain-remember`) | ALWAYS ACTIVE (cargado automáticamente) |

---

## Pros y contras

### BBrain

**Pros:**
- **Minimal por diseño.** La inyección es solo contexto, no instrucciones; no compite con el system prompt del usuario.
- **El output de `bbrain context` es el más rico estructuralmente:** facts pinneados con cuerpo completo, wiki index, y recientes — Engram solo muestra observaciones recientes.
- **Sin dependencia de servidor HTTP.** El hook es un binario estático que lee `.md` directamente; no hay race condition con un servidor que puede no estar levantado.
- **Ponytail-compatible:** no le dice al agente *cómo* trabajar, solo le muestra *qué existe*.

**Contras:**
- **Claude no guarda nada proactivamente sin ser pedido.** Las 2 líneas del CLAUDE.md no son suficientes para que Claude tome la iniciativa.
- **Tools diferidos no se cargan.** Sin ToolSearch forzado en el primer mensaje, `mcp__bbrain__mem_save` puede no estar disponible cuando Claude lo necesita.
- **Sin nudge temporal.** En sesiones largas, si el usuario no pide explícitamente guardar, nada se persiste.
- **Sin protocolo de cierre.** Al final de una sesión, el trabajo de la sesión se pierde si Claude no recibe la instrucción de resumir.
- **Sin cobertura de confirmaciones.** "Perfecto, hacemos eso" no dispara ningún guardado.

---

### Engram

**Pros:**
- **Claude guarda sin que el usuario pida.** El protocolo es explícito sobre cuándo actuar; el self-check después de cada tarea crea el hábito en el modelo.
- **Cobertura de edge cases:** confirmaciones, rechazos, preferencias, compaction, session close — todo está cubierto.
- **ToolSearch forzado garantiza que los tools estén disponibles** antes del primer mensaje.
- **El nudge temporal funciona como safety net** para sesiones largas donde el agente podría haberse olvidado.

**Contras:**
- **Dependencia de servidor HTTP levantado.** El hook de SessionStart y el nudge de UserPromptSubmit requieren que el servidor engram esté corriendo; si no está, el contexto no se inyecta.
- **El protocolo es verbose.** ~30 líneas de instrucciones obligatorias en cada sesión ocupan tokens del context window y pueden interferir con el system prompt del usuario.
- **El skill ALWAYS ACTIVE es opinionated:** asume que el agente siempre quiere guardar, lo cual puede producir `mem_save` en momentos no deseados o triviales.
- **El contexto inyectado es más pobre** que el de BBrain: solo observaciones recientes, sin wiki index ni facts pinneados con cuerpo completo.
- **El nudge temporal puede ser ruidoso** si el agente genuinamente no tiene nada que guardar — aunque tiene debounce, el recordatorio mismo consume tokens.

---

## Síntesis

El gap principal de BBrain no es el contexto (eso ya es mejor que Engram — pinneados + wiki + recientes), sino las **instrucciones de comportamiento**:

1. **Sin triggers proactivos** → Claude espera que el usuario pida guardar.
2. **Sin ToolSearch forzado** → los tools pueden no estar disponibles.
3. **Sin protocolo de cierre** → sesiones largas se pierden.

La solución mínima (ponytail) no es copiar el protocolo de 30 líneas de Engram, sino agregar al SessionStart hook unas ~8 líneas de instrucciones sobre cuándo llamar `mem_save`, más el ToolSearch forzado en el primer mensaje.
