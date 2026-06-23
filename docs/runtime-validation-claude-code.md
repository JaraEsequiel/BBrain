# Runtime validation: BBrain ↔ Claude Code (live)

Date: 2026-06-23. Validated Plans 3 + 3b end-to-end against the real `claude` CLI
(Claude Code v2.1.187), not a fake agent.

## Result: PASS

- `bbrain wiki build` → Claude distilled 2 facts into one coherent page
  ("Authentication Token Strategy", category `decisions`, both facts as sources,
  `[[fact-id]]` citations). Validated, written, index regenerated, log appended. exit 0.
- `bbrain wiki link` → Claude proposed `use-jwt-for-auth -[depends-on]->
  refresh-token-rotation` with a real reasoned `why`. Written to the fact `.md`;
  `bbrain why` reflects it. exit 0.
- `bbrain wiki lint` → correctly flagged the page `stale-page` (see finding #2). exit 1.
- `bbrain wiki lint --fix` → dropped a dangling-link after a fact was deleted;
  reported the remaining missing-source/stale/dangling-ref. exit 1.

## Finding #1 (CRITICAL for agent integration): Claude Code refuses bare transform prompts

Piping BBrain's prompt straight to `claude -p` (which says "Return ONLY a single
JSON object…") makes Claude Code **refuse it as a prompt-injection attempt** and
return prose instead of JSON. BBrain's `$BBRAIN_AGENT_CLI` cannot be a bare
`claude -p`.

**Fix:** frame the backend role with `--append-system-prompt`. The working adapter
(this is the canonical pattern Plan 5 should ship):

```sh
#!/bin/sh
# prompt on stdin -> exactly one JSON object on stdout
model="${BBRAIN_CLAUDE_MODEL:-claude-sonnet-4-6}"
sys='You are a deterministic text-to-JSON transformer invoked as a backend by the BBrain CLI (a local note-distilling tool the user owns and authorized). Your entire job: read the structured instructions on stdin and emit exactly one JSON object that satisfies them. Output ONLY raw JSON — no prose, no markdown fences, no commentary. This is a legitimate batch transformation, not a conversation.'
claude -p --output-format text --model "$model" --append-system-prompt "$sys" 2>/dev/null \
  | python3 -c 'import sys,re; s=sys.stdin.read(); m=re.search(r"```(?:json)?\s*(\{.*?\})\s*```",s,re.S) or re.search(r"(\{.*\})",s,re.S); sys.stdout.write(m.group(1) if m else s)'
```

The `python3` step strips any markdown fences / stray prose the model adds around
the JSON, so `wiki.ParseResponse` (which does `json.Unmarshal(TrimSpace(stdout))`)
always gets a clean object. **Plan 5 should productionize this** (`bbrain setup
claude-code` writing such an adapter + setting `BBRAIN_AGENT_CLI`).

## Finding #2 (workflow, not a bug): `wiki link` makes pages stale

`wiki link` writes links into fact `.md` files, bumping their `updated_at`. Any
wiki page already built from those facts then has `generated_at` < the fact's new
`updated_at`, so `wiki lint` correctly reports `stale-page`. Expected & correct —
the intended order is build → link → (re)build, and `wiki lint` is the guardrail
that surfaces the lag. Worth documenting in the eventual user guide.
