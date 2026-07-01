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
	out, failed, err := Link(context.Background(), opts)
	must(t, err)
	if len(failed) != 0 {
		t.Fatalf("unexpected failed: %+v", failed)
	}
	if len(out) != 1 || out[0].Src != "f-src" || len(out[0].Links) != 1 || out[0].Links[0].Dst != "f-cand" {
		t.Fatalf("proposals = %+v", out)
	}
	if fr.calls != 2 { // f-src and f-cand had candidates and were called; f-lonely had none and was skipped
		t.Fatalf("runner calls = %d, want 2", fr.calls)
	}
}

// A fact whose proposal never validates (non-candidate dst on every attempt) is
// dropped after exhausting retries and reported in the failed list, WITHOUT
// aborting the run or returning a Go error. Replaces the old fail-fast
// expectation: content-level badness for one fact is now graceful degradation,
// not a hard stop (mirrors Build's skip-on-exhaust for a bad batch).
func TestLinkLoopSkipsFactThatNeverValidates(t *testing.T) {
	facts := []fact.Fact{{ID: "f-src", Title: "JWT", Project: "p", Body: "a"}}
	fr := &staticRunner{out: `{"links":[{"dst":"not-a-candidate","relation":"relates","why":"x"}]}`}
	opts := LinkOptions{
		Facts:      facts,
		Candidates: map[string][]Candidate{"f-src": {{ID: "f-cand"}}},
		Runner:     fr,
	}
	out, failed, err := Link(context.Background(), opts)
	must(t, err) // no Go error: the fact is skipped, not fatal
	if len(out) != 0 {
		t.Fatalf("want no proposals, got %+v", out)
	}
	if len(failed) != 1 || failed[0].FactID != "f-src" {
		t.Fatalf("want f-src reported as failed, got %+v", failed)
	}
	if fr.calls != maxBatchAttempts { // retried up to the cap before giving up
		t.Fatalf("runner calls = %d, want %d", fr.calls, maxBatchAttempts)
	}
}

// A fact whose first attempt is malformed but whose retry is valid recovers: its
// links are produced and the runner is called twice for that fact. Fails against
// the old fail-fast Link (one malformed response aborted everything).
func TestLinkLoopRetriesTransientFailure(t *testing.T) {
	fr := &flakyLinkRunner{good: `{"links":[{"dst":"f-cand","relation":"relates","why":"both jwt"}]}`}
	opts := LinkOptions{
		Facts:      []fact.Fact{{ID: "f-src", Title: "JWT", Project: "p", Body: "a"}},
		Candidates: map[string][]Candidate{"f-src": {{ID: "f-cand"}}},
		Runner:     fr,
	}
	out, failed, err := Link(context.Background(), opts)
	must(t, err)
	if len(failed) != 0 {
		t.Fatalf("unexpected failed: %+v", failed)
	}
	if len(out) != 1 || out[0].Src != "f-src" || len(out[0].Links) != 1 || out[0].Links[0].Dst != "f-cand" {
		t.Fatalf("proposals = %+v", out)
	}
	if fr.calls != 2 { // 1st malformed, 2nd valid
		t.Fatalf("runner calls = %d, want 2", fr.calls)
	}
}

// One always-failing fact plus one healthy fact: the run COMPLETES, the good
// fact's links are produced, and the bad fact is reported in failed — no Go
// error, no all-or-nothing abort.
func TestLinkLoopBadFactDoesNotSinkGoodFact(t *testing.T) {
	fr := &perFactLinkRunner{badSrc: "f-bad", goodSrc: "f-good", goodDst: "f-cand"}
	opts := LinkOptions{
		Facts: []fact.Fact{{ID: "f-bad", Body: "a"}, {ID: "f-good", Body: "b"}},
		Candidates: map[string][]Candidate{
			"f-bad":  {{ID: "f-cand"}},
			"f-good": {{ID: "f-cand"}},
		},
		Runner: fr,
	}
	out, failed, err := Link(context.Background(), opts)
	must(t, err)
	if len(out) != 1 || out[0].Src != "f-good" || out[0].Links[0].Dst != "f-cand" {
		t.Fatalf("want only f-good's links, got %+v", out)
	}
	if len(failed) != 1 || failed[0].FactID != "f-bad" {
		t.Fatalf("want f-bad reported, got %+v", failed)
	}
}

// Every fact fails: 0 proposals, all reported, still no Go error.
func TestLinkLoopAllFactsFail(t *testing.T) {
	fr := &staticRunner{out: "not json"}
	opts := LinkOptions{
		Facts: []fact.Fact{{ID: "f1", Body: "a"}, {ID: "f2", Body: "b"}},
		Candidates: map[string][]Candidate{
			"f1": {{ID: "c1"}},
			"f2": {{ID: "c2"}},
		},
		Runner: fr,
	}
	out, failed, err := Link(context.Background(), opts)
	must(t, err)
	if len(out) != 0 {
		t.Fatalf("want no proposals, got %+v", out)
	}
	if len(failed) != 2 {
		t.Fatalf("want both facts reported, got %+v", failed)
	}
}

type staticRunner struct {
	out   string
	calls int
}

func (s *staticRunner) Run(ctx context.Context, prompt string) (string, error) {
	s.calls++
	return s.out, nil
}

// flakyLinkRunner returns malformed JSON on the first call and good on the rest.
type flakyLinkRunner struct {
	good  string
	calls int
}

func (f *flakyLinkRunner) Run(ctx context.Context, prompt string) (string, error) {
	f.calls++
	if f.calls == 1 {
		return "not json", nil
	}
	return f.good, nil
}

// perFactLinkRunner emits a valid proposal for goodSrc's prompt and malformed
// JSON for badSrc's prompt (identified by the "## Source fact" section).
type perFactLinkRunner struct {
	badSrc, goodSrc, goodDst string
}

func (p *perFactLinkRunner) Run(ctx context.Context, prompt string) (string, error) {
	if strings.Contains(prompt, "## Source fact\n### "+p.goodSrc+"\n") {
		return `{"links":[{"dst":"` + p.goodDst + `","relation":"relates","why":"x"}]}`, nil
	}
	return "not json", nil // badSrc (and anything else) is malformed
}
