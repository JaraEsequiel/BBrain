// Package prompthook implements `bbrain prompt-submit`, the Claude Code
// UserPromptSubmit hook: it forces tool loading on the first message of a
// session and nudges to save when the current project has gone stale.
package prompthook

import "time"

const (
	sessionMinAge = 5 * time.Minute  // below this, the session is too new to nudge
	saveStale     = 15 * time.Minute // no save in this long → eligible to nudge
	nudgeCooldown = 15 * time.Minute // do not repeat a nudge within this window
)

const toolSearchMsg = "FIRST ACTION — before responding, run this ToolSearch once to load BBrain's memory tools (they are deferred and not yet callable):\nselect:mcp__bbrain__mem_save,mcp__bbrain__mem_search,mcp__bbrain__mem_get,mcp__bbrain__mem_delete,mcp__bbrain__mem_link,mcp__bbrain__mem_why,mcp__bbrain__mem_related,mcp__bbrain__mem_candidates,mcp__bbrain__mem_current_project,mcp__bbrain__wiki_build,mcp__bbrain__wiki_link,mcp__bbrain__wiki_lint"

const nudgeMsg = "MEMORY CHECK — over 15 minutes since your last save to this project. If anything since then is worth remembering (a decision, discovery, fixed bug, or fact about the user), call mem_save now. If nothing is, ignore this and continue."

// DecideInput is everything the IO layer resolved; decide() does no IO.
type DecideInput struct {
	FirstMessage   bool
	SessionAge     time.Duration
	SinceLastSave  time.Duration
	HasLastSave    bool
	SinceLastNudge time.Duration
	HasLastNudge   bool
}

// DecideOutput is the systemMessage to emit (empty = inject nothing) and whether
// the caller must record a fresh nudge timestamp.
type DecideOutput struct {
	Message  string
	DidNudge bool
}

// decide applies the policy in cheapest-gate-first order. Gates 1-3 require no
// brain read, so the caller can skip resolving SinceLastSave until they pass.
func decide(in DecideInput) DecideOutput {
	if in.FirstMessage {
		return DecideOutput{Message: toolSearchMsg}
	}
	if in.SessionAge < sessionMinAge {
		return DecideOutput{}
	}
	if in.HasLastNudge && in.SinceLastNudge < nudgeCooldown {
		return DecideOutput{}
	}
	if !in.HasLastSave {
		return DecideOutput{}
	}
	if in.SinceLastSave <= saveStale {
		return DecideOutput{}
	}
	return DecideOutput{Message: nudgeMsg, DidNudge: true}
}
