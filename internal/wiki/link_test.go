package wiki

import (
	"context"
	"strings"
	"testing"

	"github.com/JaraEsequiel/BBrain/internal/fact"
)

type linkFakeRunner struct {
	srcID, dstID string
	calls        int
}

// Run emits a link proposal only for the source fact's prompt; every other
// fact's prompt (where it appears as a candidate, not the source) yields none.
func (f *linkFakeRunner) Run(ctx context.Context, prompt string) (string, error) {
	f.calls++
	if strings.Contains(prompt, "## Source fact\n### "+f.srcID+"\n") {
		return `{"links":[{"dst":"` + f.dstID + `","relation":"relates","why":"both about jwt"}]}`, nil
	}
	return `{"links":[]}`, nil
}

func TestBuildLinkPromptContainsSourceCandidatesAndSchema(t *testing.T) {
	src := fact.Fact{ID: "f-src", Title: "JWT access", Type: "decision", Project: "shopapp", Body: "access token body"}
	cands := []Candidate{{ID: "f-cand", Title: "JWT refresh", Type: "decision", Project: "shopapp", Snippet: "refresh token snippet"}}
	p := BuildLinkPrompt(src, cands, fact.Relations)
	for _, want := range []string{"## Source fact", "f-src", "access token body", "## Candidate facts", "f-cand", "refresh token snippet", "relates, depends-on", "json", "dst"} {
		if !strings.Contains(strings.ToLower(p), strings.ToLower(want)) {
			t.Fatalf("prompt missing %q:\n%s", want, p)
		}
	}
}

func TestParseLinkResponse(t *testing.T) {
	props, err := ParseLinkResponse(`  {"links":[{"dst":"f2","relation":"relates","why":"x"}]}  `)
	must(t, err)
	if len(props) != 1 || props[0].Dst != "f2" || props[0].Relation != "relates" {
		t.Fatalf("props = %+v", props)
	}
}

func TestParseLinkResponseInvalid(t *testing.T) {
	if _, err := ParseLinkResponse("not json"); err == nil {
		t.Fatal("want error on malformed JSON")
	}
}

func TestValidateProposals(t *testing.T) {
	src := fact.Fact{ID: "s"}
	cands := map[string]bool{"a": true, "b": true}
	good := []ProposedLink{{Dst: "a", Relation: "relates", Why: "ok"}}
	must(t, ValidateProposals(src, good, cands))

	bad := [][]ProposedLink{
		{{Dst: "a", Relation: "nope", Why: "x"}},        // invalid relation
		{{Dst: "s", Relation: "relates", Why: "x"}},     // self-link
		{{Dst: "zzz", Relation: "relates", Why: "x"}},   // non-candidate
		{{Dst: "a", Relation: "relates", Why: " "}},     // empty why
		{{Dst: "a", Relation: "relates", Why: "x"}, {Dst: "a", Relation: "relates", Why: "y"}}, // intra dup
	}
	for i, props := range bad {
		if err := ValidateProposals(src, props, cands); err == nil {
			t.Fatalf("bad proposal set %d accepted", i)
		}
	}
}

func TestLinkLoopSkipsFactsWithNoCandidatesAndValidates(t *testing.T) {
	facts := []fact.Fact{
		{ID: "f-src", Title: "JWT access", Project: "shopapp", Body: "a"},
		{ID: "f-cand", Title: "JWT refresh", Project: "shopapp", Body: "b"},
		{ID: "f-lonely", Title: "Unrelated", Project: "shopapp", Body: "c"},
	}
	fr := &linkFakeRunner{srcID: "f-src", dstID: "f-cand"}
	opts := LinkOptions{
		Facts: facts,
		Candidates: map[string][]Candidate{
			"f-src":  {{ID: "f-cand", Title: "JWT refresh", Project: "shopapp"}},
			"f-cand": {{ID: "f-src", Title: "JWT access", Project: "shopapp"}},
			// f-lonely has no candidates -> no LLM call
		},
		Runner: fr,
	}
	out, err := Link(context.Background(), opts)
	must(t, err)
	if len(out) != 1 || out[0].Src != "f-src" || len(out[0].Links) != 1 || out[0].Links[0].Dst != "f-cand" {
		t.Fatalf("proposals = %+v", out)
	}
	if fr.calls != 2 { // f-src and f-cand had candidates and were called; f-lonely had none and was skipped
		t.Fatalf("runner calls = %d, want 2", fr.calls)
	}
}

func TestLinkLoopAbortsOnInvalidProposal(t *testing.T) {
	facts := []fact.Fact{{ID: "f-src", Title: "JWT", Project: "p", Body: "a"}}
	// Runner returns a dst that is not in f-src's candidate set -> validation aborts.
	fr := &staticRunner{out: `{"links":[{"dst":"not-a-candidate","relation":"relates","why":"x"}]}`}
	opts := LinkOptions{
		Facts:      facts,
		Candidates: map[string][]Candidate{"f-src": {{ID: "f-cand"}}},
		Runner:     fr,
	}
	if _, err := Link(context.Background(), opts); err == nil {
		t.Fatal("Link should abort on non-candidate dst")
	}
}

type staticRunner struct{ out string }

func (s *staticRunner) Run(ctx context.Context, prompt string) (string, error) { return s.out, nil }
