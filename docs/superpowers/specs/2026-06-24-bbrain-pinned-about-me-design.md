# BBrain — Pinned context & about-me

**Date:** 2026-06-24
**Status:** Approved (design)

## Problem

BBrain surfaces memory to an agent two ways: lexical `mem_search` (on demand) and
the `bbrain context` digest injected at every `SessionStart` (wiki index + the N
most-recent facts as one-line bullets). Neither guarantees that a given fact is
*always* present. Some context — chiefly **who the user is and how the agent
should work with them** — must be in front of the agent every session, regardless
of recency or whether a search happened to match it.

There is no first-class way to say "this fact is always-on context". As a side
effect, the digest also drops globally-scoped facts when filtered by project (see
Bug below).

## Goal

1. Add a generic **`pinned`** primitive: any fact can declare itself always-on
   context, injected with its **full body** at the top of the session digest.
2. Ship **about-me** as the first user of that primitive — a single, idempotent
   fact carrying the user's identity, preferences, and interaction style. No
   special-cased code path; about-me is just a pinned fact.

## Non-goals (YAGNI)

- No dedicated `bbrain about` command and no enumerated/magic `type`. Authoring is
  `mem_save` (agent) or hand-editing the `.md` (user).
- No forced section template for the about-me body; it is free markdown.
- No per-project *about-me*. (Per-project *pinned* facts fall out naturally from
  the scope rule below, but the about-me itself is global.)

## Design

### 1. The `pinned` primitive

Add a boolean field to the fact frontmatter, defaulting to false.

- `internal/fact/fact.go` — new field on `Fact`:
  `Pinned bool \`yaml:"pinned,omitempty"\``. `omitempty` keeps existing files
  byte-stable (the key only appears when true). No migration: absent key parses as
  `false`.
- `internal/mcp/tools.go` — `mem_save` input schema gains
  `"pinned": {"type":"boolean"}` (optional); the save path threads it onto the
  `Fact`, and the tool's JSON output reflects it.
- `Parse`/`Marshal` round-trip `Pinned` (covered by a unit test).

A pinned fact declares: *this belongs in every session's context, in full, without
depending on recency or search.*

### 2. Digest: a Pinned section, full body, on top

`App.Context(project, limit)` changes from one section to two memory sections:

```
# BBrain memory context

## About you & pinned context     <- NEW, first
<full body of each pinned fact, separated, most-recently-updated first>

## Wiki index                     <- unchanged
...

## Recent facts                   <- unchanged format (one-line bullets)
<recent facts, EXCLUDING any already shown in the pinned section>
```

Rules for the pinned section:

- **Selection:** a fact is pinned-eligible when `Pinned == true` **and** it passes
  the project visibility rule below.
- **Project visibility:** a pinned fact with no `project` (global) is shown for
  *every* project filter; a pinned fact *with* a `project` is shown only when it
  matches the requested project. (This also fixes the Bug below for the pinned
  path.)
- **Rendering:** full `Body`, not a bullet. Each pinned fact gets a small heading
  (its `Title`) so multiple pinned facts stay legible. Ordered by `UpdatedAt`
  descending.
- **Dedup:** facts shown in the pinned section are removed from the "Recent facts"
  candidate set so they never appear twice.
- **Limit:** `limit` continues to bound the "Recent facts" section only. Pinned
  facts are always shown in full and are **not** subject to `limit` (pinning is an
  explicit, bounded-by-the-user act; if the user pins 50 facts that is their
  choice).

#### Bug noted in passing

Today `Context()` skips any fact whose `Project` differs from the filter, including
global facts where `Project == ""` (`"" != "BBrain"` → skipped). The new project
visibility rule corrects this for pinned facts. Non-pinned global facts in "Recent
facts" remain out of scope for this change to keep the diff focused; revisit
separately if it bites.

### 3. about-me as the first pinned fact

No magic type. The about-me is a normal fact:

```yaml
type: about-me
scope: global
pinned: true
topic_key: profile/about-me   # upsert: rewrites the same file in place
```

- `topic_key` makes saves idempotent — re-saving rewrites the one
  `raws/facts/<...>-about-me.md` instead of creating duplicates.
- Body is free markdown: identity, preferences, interaction style.
- Authored via `mem_save` or by hand-editing the file; both work because the `.md`
  is the source of truth.

## Components & boundaries

| Unit | Change | Depends on |
|------|--------|-----------|
| `internal/fact` | add `Pinned` field; round-trip in Parse/Marshal | yaml |
| `internal/mcp/tools.go` | `mem_save` schema + save-path threading + output | fact, app |
| `internal/app` `Context()` | two-section digest, pinned selection/visibility/dedup | store, fact |

The `pinned` field is the only cross-cutting addition; everything else reads it.

## Testing

- `fact`: round-trip a `Fact{Pinned:true}` through Marshal→Parse; assert a
  `pinned:false` fact omits the key on disk (byte-stability).
- `app.Context`:
  - a global pinned fact appears (full body) under any project filter;
  - a project-scoped pinned fact appears only under its project;
  - a pinned fact is **not** duplicated in "Recent facts";
  - empty brain / no pinned facts still renders a valid digest (no empty headings
    that mislead).
- `mcp`: `mem_save` with `pinned:true` persists the flag and echoes it in output.

## Open questions

- Section heading wording ("About you & pinned context") — cosmetic, easy to
  change.
- Whether to later offer a starter about-me template/seed — deferred; out of scope.
