package index

// Acceptance suite for BBRAIN-9 (snippet field in search results). One test per
// TC-n.m from the accepted plan
// (.dev-tools/plans/BBRAIN-8/BBRAIN-9-search-snippet.md), named
// TestAcceptance_AC<N>_TC<n.m>_... — a separate file per ticket (not appended to
// acceptance_test.go, which is BBRAIN-2's) so AC numbers don't collide across
// tickets and each file's header stays accurate to what it covers.
//
// This file exercises only the public Index surface (Open, Search, SearchAny) —
// black-box, not a duplicate of index_test.go's fine-grained unit tests.

import (
	"strings"
	"testing"
)

func TestAcceptance_AC1_TC1_1_SearchReturnsSnippetContainingTerm(t *testing.T) {
	ix := openMem(t)
	must(t, ix.IndexFact(sampleFact("f1", "Archive fact",
		"This is a long body about how to archive old notes and keep the index tidy for later retrieval.",
		"decision", "bbrain"), "/x/f1.md"))

	res, err := ix.Search("archive", 10, "", "")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(res) != 1 || !strings.Contains(strings.ToLower(res[0].Snippet), "archive") {
		t.Fatalf("Search(archive) = %+v, want 1 result with snippet containing 'archive'", res)
	}
}

func TestAcceptance_AC1_TC1_2_SnippetPopulatedEvenWhenTermOnlyInTitle(t *testing.T) {
	ix := openMem(t)
	must(t, ix.IndexFact(sampleFact("f1", "Archive strategy",
		"This body never mentions the special word at all, just filler text padding it out.",
		"decision", "bbrain"), "/x/f1.md"))

	res, err := ix.Search("archive", 10, "", "")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(res) != 1 || res[0].Snippet == "" {
		t.Fatalf("Search(archive) = %+v, want 1 result with a non-empty snippet", res)
	}
}

func TestAcceptance_AC2_TC2_1_LongBodySnippetTruncatedAtWordBoundary(t *testing.T) {
	ix := openMem(t)
	body := "This body repeats the marker word transformation many times to guarantee the " +
		"snippet needs truncation for the query term marker across a long enough span of text " +
		"that the token budget forces a cut somewhere before the body actually ends for real."
	must(t, ix.IndexFact(sampleFact("f1", "Long fact", body, "decision", "bbrain"), "/x/f1.md"))

	res, err := ix.Search("marker", 10, "", "")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(res) != 1 {
		t.Fatalf("want 1 result, got %d", len(res))
	}
	bodyWords := make(map[string]bool)
	for _, w := range strings.Fields(body) {
		bodyWords[strings.Trim(w, ".,")] = true
	}
	for _, w := range strings.Fields(strings.TrimSuffix(res[0].Snippet, "...")) {
		w = strings.Trim(w, ".,")
		if w != "" && !bodyWords[w] {
			t.Fatalf("snippet %q contains a mid-word fragment %q", res[0].Snippet, w)
		}
	}
}

func TestAcceptance_AC2_TC2_2_LongBodySnippetNeverMidWord_RepeatabilityCheck(t *testing.T) {
	ix := openMem(t)
	body := strings.Repeat("wordlet marker transformation phrase segment content unit ", 15)
	must(t, ix.IndexFact(sampleFact("f1", "Repeated fact", body, "decision", "bbrain"), "/x/f1.md"))

	res, err := ix.Search("marker", 10, "", "")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(res) != 1 || res[0].Snippet == "" {
		t.Fatalf("Search(marker) = %+v, want 1 result with non-empty snippet", res)
	}
	if strings.HasSuffix(strings.TrimSuffix(res[0].Snippet, "..."), " ") {
		t.Fatalf("snippet %q has a trailing space before the ellipsis, suspicious truncation", res[0].Snippet)
	}
}

func TestAcceptance_AC3_TC3_1_ShortBodyReturnedInFullWhitespaceCollapsed(t *testing.T) {
	ix := openMem(t)
	must(t, ix.IndexFact(sampleFact("f1", "Short fact", "Tiny  body\n\n  here.",
		"decision", "bbrain"), "/x/f1.md"))

	res, err := ix.Search("tiny", 10, "", "")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(res) != 1 || res[0].Snippet != "Tiny body here." {
		t.Fatalf("Search(tiny) = %+v, want snippet == whitespace-collapsed full body", res)
	}
}

func TestAcceptance_AC3_TC3_2_EmptyBodyReturnsEmptySnippetNoError(t *testing.T) {
	ix := openMem(t)
	must(t, ix.IndexFact(sampleFact("f1", "Empty body fact", "",
		"decision", "bbrain"), "/x/f1.md"))

	res, err := ix.Search("\"Empty body fact\"", 10, "", "")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(res) != 1 || res[0].Snippet != "" {
		t.Fatalf("Search(title-only match on empty body) = %+v, want snippet == \"\"", res)
	}
}

func TestAcceptance_AC4_TC4_1_ZeroMatchesReturnsEmptyListNoError(t *testing.T) {
	ix := openMem(t)
	must(t, ix.IndexFact(sampleFact("f1", "Fact", "unrelated content",
		"decision", "bbrain"), "/x/f1.md"))

	res, err := ix.Search("nonexistentterm", 10, "", "")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(res) != 0 {
		t.Fatalf("want 0 results, got %d", len(res))
	}
}

func TestAcceptance_AC4_TC4_2_EmptyQueryReturnsEmptyListNoError(t *testing.T) {
	ix := openMem(t)
	must(t, ix.IndexFact(sampleFact("f1", "Fact", "some content",
		"decision", "bbrain"), "/x/f1.md"))

	res, err := ix.Search("", 10, "", "")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(res) != 0 {
		t.Fatalf("want 0 results for empty query, got %d", len(res))
	}
}

func TestAcceptance_AC5_TC5_1_SearchAnyIncludesSnippet(t *testing.T) {
	ix := openMem(t)
	must(t, ix.IndexFact(sampleFact("f1", "Fact one",
		"discusses caching strategies for the index", "decision", "bbrain"), "/x/f1.md"))

	res, err := ix.SearchAny("caching strategies", 10, "", "")
	if err != nil {
		t.Fatalf("SearchAny: %v", err)
	}
	if len(res) != 1 || res[0].Snippet == "" {
		t.Fatalf("SearchAny(...) = %+v, want 1 result with non-empty snippet", res)
	}
}

func TestAcceptance_AC5_TC5_2_SearchAnyZeroCandidatesReturnsEmptyNoError(t *testing.T) {
	ix := openMem(t)
	must(t, ix.IndexFact(sampleFact("f1", "Lonely fact", "nothing relates to this",
		"decision", "bbrain"), "/x/f1.md"))

	res, err := ix.SearchAny("completely unrelated terms", 10, "", "")
	if err != nil {
		t.Fatalf("SearchAny: %v", err)
	}
	if len(res) != 0 {
		t.Fatalf("want 0 results, got %d", len(res))
	}
}
