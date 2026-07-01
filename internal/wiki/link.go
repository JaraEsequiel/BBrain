package wiki

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/JaraEsequiel/BBrain/internal/fact"
	"github.com/JaraEsequiel/BBrain/internal/llm"
)

// Candidate is a fact offered to the LLM as a possible link target for a source
// fact. It carries just enough context (title/type/project + a short snippet) to
// judge relatedness without sending whole bodies.
type Candidate struct {
	ID      string
	Title   string
	Type    string
	Project string
	Snippet string
}

// ProposedLink is one reasoned link the LLM proposes from the source fact.
type ProposedLink struct {
	Dst      string `json:"dst"`
	Relation string `json:"relation"`
	Why      string `json:"why"`
}

// FactProposals groups one source fact's validated proposals.
type FactProposals struct {
	Src   string
	Links []ProposedLink
}

// Edge is a link actually written (or, in dry-run, that would be written).
type Edge struct {
	Src      string
	Dst      string
	Relation string
	Why      string
}

// FailedLink records a source fact whose per-fact LLM linking (Run +
// ParseLinkResponse + ValidateProposals) exhausted maxBatchAttempts and was
// skipped so the rest of the link run could proceed. Its links are not written
// this run; they are recovered on the next link (WikiLink is idempotent —
// Candidates already excludes already-linked facts). This is content-level
// degradation (the backend produced bad output for THIS fact), distinct from
// infrastructure failures (candidate/store IO) which still abort the run.
type FailedLink struct {
	FactID string
	Err    string // last error that caused the fact to be given up
}

// LinkResult reports what a wiki link run wrote.
type LinkResult struct {
	Written []Edge
	Skipped int          // links already present, not re-written (idempotency)
	Failed  []FailedLink // facts dropped after exhausting retries (graceful degradation)
	DryRun  bool
}

// LinkOptions configures the per-fact LLM linking loop.
type LinkOptions struct {
	Facts      []fact.Fact
	Candidates map[string][]Candidate // fact id -> its candidate facts
	Runner     llm.Runner
}

type linkResponse struct {
	Links []ProposedLink `json:"links"`
}

// BuildLinkPrompt assembles the prompt for one source fact: instructions, the
// relation vocabulary, the JSON schema, the source fact, and its candidates.
func BuildLinkPrompt(src fact.Fact, candidates []Candidate, relations []string) string {
	var sb strings.Builder
	sb.WriteString("You are BBrain's link reasoner. Decide which candidate facts are genuinely related to the source fact, and how.\n")
	sb.WriteString("Return ONLY a single JSON object: {\"links\":[{\"dst\",\"relation\",\"why\"}]}.\n")
	sb.WriteString("- dst: the id of a candidate fact below (never invent ids).\n")
	sb.WriteString("- relation: one of: " + strings.Join(relations, ", ") + ".\n")
	sb.WriteString("- why: one sentence explaining the relationship.\n")
	sb.WriteString("Only include genuinely related candidates; return an empty list if none apply.\n\n")

	sb.WriteString("## Source fact\n")
	sb.WriteString("### " + src.ID + "\n")
	sb.WriteString(fmt.Sprintf("title: %s | type: %s | project: %s | scope: %s | tags: %s\n",
		src.Title, src.Type, src.Project, src.Scope, strings.Join(src.Tags, ",")))
	sb.WriteString(strings.TrimSpace(src.Body) + "\n\n")

	sb.WriteString("## Candidate facts\n")
	for _, c := range candidates {
		sb.WriteString("### " + c.ID + "\n")
		sb.WriteString(fmt.Sprintf("title: %s | type: %s | project: %s\n", c.Title, c.Type, c.Project))
		if c.Snippet != "" {
			sb.WriteString(c.Snippet + "\n")
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

// ParseLinkResponse parses the LLM stdout into the proposed links.
func ParseLinkResponse(stdout string) ([]ProposedLink, error) {
	var r linkResponse
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &r); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidJSON, err)
	}
	return r.Links, nil
}

// ValidateProposals rejects anything unsafe before BBrain writes links: relation
// must be in the controlled vocabulary, dst must be one of the source's
// candidates (never an invented id), no self-links, why is mandatory, and no
// duplicate {dst,relation} within the same source's proposals.
func ValidateProposals(src fact.Fact, props []ProposedLink, candidateIDs map[string]bool) error {
	seen := map[string]bool{}
	for _, p := range props {
		if !fact.ValidRelation(p.Relation) {
			return fmt.Errorf("wiki: fact %q proposes invalid relation %q", src.ID, p.Relation)
		}
		if p.Dst == src.ID {
			return fmt.Errorf("wiki: fact %q proposes a self-link", src.ID)
		}
		if !candidateIDs[p.Dst] {
			return fmt.Errorf("wiki: fact %q proposes non-candidate target %q", src.ID, p.Dst)
		}
		if strings.TrimSpace(p.Why) == "" {
			return fmt.Errorf("wiki: fact %q link to %q has empty why", src.ID, p.Dst)
		}
		key := p.Dst + "\x00" + p.Relation
		if seen[key] {
			return fmt.Errorf("wiki: fact %q has a duplicate proposal %q/%q", src.ID, p.Dst, p.Relation)
		}
		seen[key] = true
	}
	return nil
}

// Link runs the per-fact linking pass: for each fact with candidates, prompt the
// LLM, parse, and validate. It writes nothing (writes go through the app layer).
//
// Same skip-on-exhaust posture as Build: the per-fact operation (Run +
// ParseLinkResponse + ValidateProposals) is retried up to maxBatchAttempts on
// any error — the agentic backend is non-deterministic, so a flaky call almost
// always parses/validates on retry (no backoff; the backend already takes
// ~60s/call). A fact that exhausts its retries is NOT fatal: it is dropped,
// recorded in the returned failed list, and the loop moves on so a single flaky
// fact can't discard every other fact's links. Its links are recovered on the
// next run (WikiLink is idempotent).
func Link(ctx context.Context, opts LinkOptions) ([]FactProposals, []FailedLink, error) {
	var out []FactProposals
	var failed []FailedLink
	for _, f := range opts.Facts {
		cands := opts.Candidates[f.ID]
		if len(cands) == 0 {
			continue // nothing to link against; skip the LLM call (not a failure)
		}
		candIDs := make(map[string]bool, len(cands))
		for _, c := range cands {
			candIDs[c.ID] = true
		}
		prompt := BuildLinkPrompt(f, cands, fact.Relations)
		var props []ProposedLink
		var lastErr error
		for attempt := 1; attempt <= maxBatchAttempts; attempt++ {
			stdout, err := opts.Runner.Run(ctx, prompt)
			if err != nil {
				lastErr = err
				continue
			}
			props, lastErr = ParseLinkResponse(stdout)
			if lastErr != nil {
				continue
			}
			lastErr = ValidateProposals(f, props, candIDs)
			if lastErr == nil {
				break
			}
		}
		if lastErr != nil {
			failed = append(failed, FailedLink{FactID: f.ID, Err: lastErr.Error()})
			continue
		}
		if len(props) > 0 {
			out = append(out, FactProposals{Src: f.ID, Links: props})
		}
	}
	return out, failed, nil
}
