# BBrain — Plan 3: Wiki Layer (`wiki build`) + Pluggable LLM Runner — Design Spec

**Date:** 2026-06-23
**Status:** Approved (design phase)
**Author:** vex (esequieljara2002@gmail.com)
**Builds on:** Plan 1 (core engine), Plan 2 (reasoned wikilink graph). Part of the 1→6 roadmap (header of Plan 1). This is Plan 3.

---

## 1. Summary

BBrain's `wiki/` is the **distilled layer** of the Karpathy LLM-wiki pattern: a pluggable
LLM reads the raw layer (`raws/facts/` + `raws/user-raws/`) and writes human-navigable
pages that synthesize related facts. This plan delivers the first wiki routine end-to-end —
**`bbrain wiki build`** (generate/update wiki pages) — plus the **pluggable LLM runner**
that every future wiki routine reuses.

**Orchestration model: BBrain orchestrates; the LLM is a pure text→JSON function.** BBrain
reads the raws, builds a prompt, invokes a configurable LLM CLI (prompt on stdin → JSON on
stdout), **validates the response, and writes every file itself** (atomic). The LLM never
touches disk. This keeps the routine deterministic, testable with a fake runner, and safe —
and keeps `.md` the source of truth.

### Scope of this plan

**In scope:**
- `internal/llm` — the pluggable `Runner` (shell-out to `BBRAIN_AGENT_CLI`, JSON parsing,
  error sentinels, a fake for tests). The shared foundation for all wiki routines.
- `bbrain wiki build` — read raws → invoke LLM → write/update `wiki/` pages → regenerate
  `wiki/index.md` → append `wiki/log.md`.
- Wiki page format, `index.md` (derived), `log.md` (append-only).

**Deferred to Plan 3b (reuses the runner built here):**
- `bbrain wiki link` — populate reasoned wikilinks between facts (FindCandidates → LLM →
  `store.AddLink`, the primitive Plan 2 created for exactly this).
- `bbrain wiki lint` — consistency checks (dangling links, stale pages, missing `why`).

**Deferred to later plans (per roadmap):** MCP exposure of `wiki_build`/`wiki_lint`
(Plan 4); TUI config of the LLM CLI (Plan 5 — until then the runner is configured by env
var). CLI only, exactly as Plans 1–2 deferred MCP.

---

## 2. Trigger model

Two levels, separated by cost (spec §5):

- **Per fact save → cheap, automatic, no LLM.** After a save, BBrain runs `FindCandidates`
  (lexical FTS — already built in Plan 2's `App.Candidates`). It only *surfaces* lexically
  similar facts; it writes nothing and calls no LLM. (Real exposure is via `mem_save` in
  Plan 4.)
- **On-demand / batch → expensive, explicit, LLM.** The semantic work runs only when the
  user (or an external routine: cron, hook, cowork) invokes `bbrain wiki build`. The user
  chooses when; BBrain does not auto-run the LLM.

Subcommands are separate so the user can chain independent LLM routines in any order — e.g.
first generate pages (`wiki build`), then populate relations (`wiki link`, Plan 3b).

---

## 3. Architecture

New packages follow the by-responsibility split of Plans 1–2 (`fact` → `brain` → `store` →
`index` → `app` → `cmd/bbrain`):

- **`internal/llm/runner.go`** — the pluggable LLM abstraction. A strict boundary: only
  `internal/wiki` (and, in Plan 3b, the link routine) imports it.
- **`internal/wiki/wiki.go`** — the build routine: assemble the prompt from raws + existing
  pages, call the `Runner`, validate the JSON response, write pages (atomic), regenerate
  `index.md`, append `log.md`. Owns the page/index/log formats and their parsing.
- **`internal/app/app.go`** — *modify:* add `WikiBuild(...)` wiring `store` (read facts) +
  `llm.Runner` + `wiki`. The `App` gains an injectable `Runner` field (default = the
  env-configured shell-out runner; overridden with a fake in tests — same pattern as
  `Store.Now`).
- **`cmd/bbrain/main.go`** — *modify:* add the `wiki build` subcommand.

`.md` stays the source of truth: wiki pages are real files; `index.md` is **derived** (BBrain
regenerates it by scanning `wiki/`).

---

## 4. The pluggable LLM runner (`internal/llm`)

```go
// Runner is the abstraction over an external LLM CLI. Implementations shell out
// to a configured command, send a prompt, and return the command's raw stdout.
type Runner interface {
    Run(ctx context.Context, prompt string) (string, error)
}
```

- **Configuration:** env var `BBRAIN_AGENT_CLI` holds the command to run (e.g. `claude -p`
  or a user wrapper). Unset → `ErrCLINotConfigured`. Command not found in PATH →
  `ErrCLINotInstalled`.
- **Invocation:** prompt delivered on **stdin**; response read from **stdout**. A
  configurable timeout (default 120s) → `ErrTimeout` on overrun.
- **Parsing:** the caller (`wiki`) expects **a single JSON object** on stdout (trimmed of
  surrounding whitespace). Unparseable → `ErrInvalidJSON`.
- **Testing:** a `fakeRunner` returns canonical JSON, so build tests never invoke a real
  LLM. The real shell-out runner is tested against a small script under `testdata/` that
  echoes canned JSON, plus the error paths. (Mirrors engram's `AgentRunner`/`Verdict`
  boundary.)

Error sentinels: `ErrCLINotConfigured`, `ErrCLINotInstalled`, `ErrTimeout`, `ErrInvalidJSON`.

---

## 5. `wiki build` flow

1. **Gather facts.** `store.ListFacts`, optionally filtered by `--project` / `--scope`,
   reduced to a compact digest (id, title, type, project, scope, tags, body).
2. **Gather existing pages.** Read the current content of every page under `wiki/` to pass
   as **reconciliation context** (see §7).
3. **Build one prompt** (facts digest + existing pages + instructions + the controlled
   category vocabulary + the expected JSON schema) and send it to the `Runner`.
4. **Parse** the JSON response: a list of pages, each
   `{slug, category, title, sources:[fact-ids], body, change_reason}`.
5. **Validate** each page (see §8). On any validation failure, **abort the whole build**
   (no partial writes).
6. **Compute the bucket** for each page from its `sources` (see §6) — BBrain derives it; the
   LLM does not.
7. **Write** each page via `atomic.WriteFile` at `wiki/<bucket>/<category>/<slug>.md`
   (upsert by path; content already reconciled). Pages the LLM did not return this run are
   left untouched (deletion/staleness is `wiki lint`, Plan 3b).
8. **Regenerate `wiki/index.md`** by scanning `wiki/`.
9. **Append `wiki/log.md`** with a timestamped entry: the command, each page written, and
   its `change_reason`.

---

## 6. Tree layout & bucket derivation

```
wiki/<bucket>/<category>/<slug>.md
        │          └── decisions | concepts | comparisons | people | preferences | entities  (controlled, extensible)
        └── projects/<project>  |  global
```

**BBrain derives `<bucket>` from the page's `sources`** (deterministic, validated — not the
LLM's choice):

- all sources belong to a **single project** (scope `project`) → `projects/<that-project>/`
- sources are **`scope: global`/`personal`**, or **span multiple projects** → `global/`

The LLM returns only `{category, slug, title, sources, body, change_reason}`; BBrain computes
the bucket. A page synthesizing two projects naturally lands in `global/` because its sources
cross projects.

### Example — two projects with opposing technical decisions

Raw layer (flat, all projects mixed; separation lives in frontmatter):

```
raws/facts/
├── 2026-06-20-shopapp-jwt-auth.md        # project: shopapp
├── 2026-06-20-shopapp-postgres.md        # project: shopapp
├── 2026-06-21-shopapp-react.md           # project: shopapp
├── 2026-06-22-datacli-sqlite.md          # project: datacli
├── 2026-06-22-datacli-apikey-auth.md     # project: datacli
├── 2026-06-23-prefer-single-binary.md    # scope: global
└── 2026-06-23-trunk-based-dev.md         # scope: personal
```

Distilled layer (generated by `wiki build`, bucketed by project):

```
wiki/
├── index.md                              # derived catalog (regenerated each build)
├── log.md
├── projects/
│   ├── shopapp/
│   │   ├── decisions/  (auth-model.md, data-store.md)   # Postgres + JWT
│   │   ├── concepts/   (architecture.md)
│   │   └── people/     (jane-pm.md)                     # person seen only in shopapp
│   └── datacli/
│       └── decisions/  (auth-model.md, data-store.md)   # SQLite + API-key
└── global/
    ├── people/         (maria-mentora.md)               # person across projects → global/
    └── preferences/    (dependencies.md, workflow.md)
```

Both projects have `auth-model.md` and `data-store.md` under different project folders — no
collision, each reflecting its own tech decision.

### Category vocabulary

Controlled but extensible. Default set passed to the LLM in the prompt and validated against:
`decisions | concepts | comparisons | people | preferences | entities`. Extend via
`--categories` (CLI flag) / config. A controlled vocabulary prevents drift (`person` vs
`people` vs `contact`) and keeps `index.md` clean, while remaining open to new kinds (orgs,
tools, glossary…) without code changes.

---

## 7. Page format & reconciliation

**Page** — `wiki/<bucket>/<category>/<slug>.md`:

```markdown
---
title: Auth model
category: decisions
sources:
  - 2026-06-20-shopapp-jwt-auth
generated_at: 2026-06-23T16:00:00Z
---

# Auth model

shopapp uses JWT with refresh tokens. See [[2026-06-20-shopapp-jwt-auth]]…
```

**LLM JSON response:**

```json
{
  "pages": [
    {
      "slug": "auth-model",
      "category": "decisions",
      "title": "Auth model",
      "sources": ["2026-06-20-shopapp-jwt-auth"],
      "body": "# Auth model\n\n...",
      "change_reason": "created"
    }
  ]
}
```

**Reconciliation (existing pages).** A re-build does **not** blind-overwrite or skip existing
pages. When a page already exists — including manual edits — BBrain passes its current
content to the LLM as context (step 2 of the flow). The LLM returns the **reconciled** content
(incorporating manual edits + new facts) plus a `change_reason` explaining *what changed and
why*. BBrain then writes that version. Manual edits are preserved **by incorporation**, not
by exclusion. The `change_reason` is recorded in `log.md`.

**`wiki/index.md`** (derived; BBrain regenerates by scanning `wiki/`):

```markdown
# Wiki Index
<!-- Generated by `bbrain wiki build` — do not edit by hand; regenerated each build. -->

## projects/shopapp
- [Auth model](projects/shopapp/decisions/auth-model.md) — decisions — 1 source
- [Architecture](projects/shopapp/concepts/architecture.md) — concepts — 3 sources

## global
- [Dependencies](global/preferences/dependencies.md) — preferences — 1 source
```

**`wiki/log.md`** (append-only):

```markdown
## 2026-06-23T16:00:00Z — wiki build
- wrote projects/shopapp/decisions/auth-model.md (1 source): created
- wrote global/preferences/dependencies.md (1 source): reconciled manual edit with new single-binary preference
```

---

## 8. CLI & validation

```
bbrain wiki build [--project P] [--scope S] [--categories a,b,c] [--dry-run]
```

- `--project` / `--scope` — filter which facts enter the digest (default: all).
- `--categories` — extend the controlled category vocabulary for this run.
- `--dry-run` — run the full pipeline (including the LLM) but **print** the pages and log
  entries it would write, without touching disk. Useful for inspecting prompt/response
  without mutating.
- `BBRAIN_AGENT_CLI` unset → clear error, exit 1.

**Validation (BBrain validates the LLM output, because BBrain is what writes):**

- `slug` and `category` match a safe charset (`[a-z0-9-]+`); `category` is in the active
  vocabulary.
- The resolved path is **contained within `wiki/`** (reject `..`, absolute paths, traversal).
- `sources` reference **existing facts**.
- Required fields are non-empty.

On any validation failure the build **aborts** (no partial, inconsistent state is written).
Writes use `atomic.WriteFile`.

---

## 9. Testing strategy

- **`internal/llm`** — the real shell-out runner against a `testdata/` script that emits
  canonical JSON; plus error paths (not configured, not installed, invalid JSON, timeout).
- **`internal/wiki`** — the build routine with a `fakeRunner` returning canonical JSON:
  assert pages written with correct frontmatter/content, `index.md` regenerated, `log.md`
  appended, validation rejects bad slugs/categories/sources/traversal, the bucket is derived
  correctly (single-project vs cross-project/global), and **an existing page's content is
  included in the prompt** the runner receives (reconciliation).
- **`internal/app`** — `WikiBuild` wiring with an injected `fakeRunner`.
- **`cmd/bbrain`** — e2e: `BBRAIN_AGENT_CLI` pointing at a test script, run `wiki build`,
  assert the resulting files on disk.

---

## 10. Key decisions (record)

| Decision | Choice |
|---|---|
| Plan 3 scope | Pluggable LLM runner + `wiki build` (pages) + `index.md` + `log.md`. `wiki link` + `wiki lint` → Plan 3b. |
| Command structure | Separate subcommands (`wiki build`, future `wiki link`, `wiki lint`) so LLM routines chain independently |
| Trigger model | Cheap FTS `FindCandidates` per save (no LLM); heavy LLM only on explicit `wiki build` (or external routine) |
| LLM contract | BBrain orchestrates; LLM is a pure text→JSON function; **BBrain validates and writes** every file |
| Runner config | env `BBRAIN_AGENT_CLI`; prompt on stdin; one JSON object on stdout; configurable timeout; fake for tests |
| Page format | Frontmatter (title, category, sources, generated_at) + body; LLM returns `{slug, category, title, sources, body, change_reason}` |
| Re-build semantics | Reconciliation: feed existing page content to the LLM as context; it returns reconciled content + `change_reason`; manual edits preserved by incorporation |
| Tree layout | `wiki/<bucket>/<category>/<slug>.md`; bucket derived by BBrain from sources (`projects/<p>` or `global`) |
| Category vocabulary | Controlled + extensible (`decisions|concepts|comparisons|people|preferences|entities`, extend via `--categories`) |
| `index.md` | Derived — BBrain regenerates by scanning `wiki/`; never authored by the LLM |
| `log.md` | Append-only: timestamp + command + per-page `change_reason` |
| Validation failure | Abort the build (no partial writes); safe slug/category, path containment in `wiki/`, sources must exist |
| Out of scope | MCP `wiki_*` (Plan 4); TUI LLM config (Plan 5); page deletion/staleness (= `wiki lint`, Plan 3b) |
