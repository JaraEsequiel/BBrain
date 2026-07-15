package index

// Acceptance suite for BBRAIN-10 (title+snippet in mem_related/mem_why). One
// test per TC-n.m from the accepted plan
// (.dev-tools/plans/BBRAIN-8/BBRAIN-10-graph-snippet.md), named
// TestAcceptance_AC<N>_TC<n.m>_... — a separate file per ticket (mirrors
// acceptance_bbrain9_test.go) so AC numbers don't collide across tickets.
//
// This file exercises only the public Index surface (Open, IndexFact,
// IndexLinks, Neighbors, Why) — black-box, not a duplicate of index_test.go's
// fine-grained unit tests.

import (
	"testing"

	"github.com/JaraEsequiel/BBrain/internal/fact"
)

func TestAcceptance_AC1_TC1_1_NeighborIncludesLinkedFactTitle(t *testing.T) {
	ix := openMem(t)
	must(t, ix.IndexFact(sampleFact("a", "Fact A", "x", "decision", "bbrain"), "/x/a.md"))
	must(t, ix.IndexFact(sampleFact("b", "Fact B", "y", "decision", "bbrain"), "/x/b.md"))
	fa := sampleFact("a", "Fact A", "x", "decision", "bbrain")
	fa.Links = []fact.Link{{Target: "[[b]]", Relation: "depends-on", Why: "needs b"}}
	must(t, ix.IndexLinks(fa))

	ns, err := ix.Neighbors("a")
	if err != nil {
		t.Fatalf("Neighbors: %v", err)
	}
	if len(ns) != 1 || ns[0].Title != "Fact B" {
		t.Fatalf("Neighbors(a) = %+v, want title Fact B", ns)
	}
}

func TestAcceptance_AC1_TC1_2_DanglingNeighborTitleIsEmptyNotError(t *testing.T) {
	ix := openMem(t)
	fa := sampleFact("a", "Fact A", "x", "decision", "bbrain")
	fa.Links = []fact.Link{{Target: "[[b]]", Relation: "depends-on", Why: "needs b"}}
	must(t, ix.IndexLinks(fa))

	ns, err := ix.Neighbors("a")
	if err != nil {
		t.Fatalf("Neighbors: %v", err)
	}
	if len(ns) != 1 || ns[0].Title != "" {
		t.Fatalf("Neighbors(a) = %+v, want empty title for dangling neighbor", ns)
	}
}

func TestAcceptance_AC2_TC2_1_NeighborIncludesLinkedFactSnippet(t *testing.T) {
	ix := openMem(t)
	must(t, ix.IndexFact(sampleFact("a", "Fact A", "x", "decision", "bbrain"), "/x/a.md"))
	must(t, ix.IndexFact(sampleFact("b", "Fact B", "Body of fact B.",
		"decision", "bbrain"), "/x/b.md"))
	fa := sampleFact("a", "Fact A", "x", "decision", "bbrain")
	fa.Links = []fact.Link{{Target: "[[b]]", Relation: "depends-on", Why: "needs b"}}
	must(t, ix.IndexLinks(fa))

	ns, err := ix.Neighbors("a")
	if err != nil {
		t.Fatalf("Neighbors: %v", err)
	}
	if len(ns) != 1 || ns[0].Snippet != "Body of fact B." {
		t.Fatalf("Neighbors(a) = %+v, want snippet 'Body of fact B.'", ns)
	}
}

func TestAcceptance_AC2_TC2_2_ShortNeighborBodySnippetIsFullWhitespaceCollapsed(t *testing.T) {
	ix := openMem(t)
	must(t, ix.IndexFact(sampleFact("a", "Fact A", "x", "decision", "bbrain"), "/x/a.md"))
	must(t, ix.IndexFact(sampleFact("b", "Fact B", "Tiny  body\n\n  here.",
		"decision", "bbrain"), "/x/b.md"))
	fa := sampleFact("a", "Fact A", "x", "decision", "bbrain")
	fa.Links = []fact.Link{{Target: "[[b]]", Relation: "depends-on", Why: "needs b"}}
	must(t, ix.IndexLinks(fa))

	ns, err := ix.Neighbors("a")
	if err != nil {
		t.Fatalf("Neighbors: %v", err)
	}
	if len(ns) != 1 || ns[0].Snippet != "Tiny body here." {
		t.Fatalf("Neighbors(a) = %+v, want whitespace-collapsed snippet", ns)
	}
}

func TestAcceptance_AC3_TC3_1_WhyReturnsBothSidesTitleAndSnippet(t *testing.T) {
	ix := openMem(t)
	must(t, ix.IndexFact(sampleFact("a", "Fact A", "Body A.", "decision", "bbrain"), "/x/a.md"))
	must(t, ix.IndexFact(sampleFact("b", "Fact B", "Body B.", "decision", "bbrain"), "/x/b.md"))
	fa := sampleFact("a", "Fact A", "x", "decision", "bbrain")
	fa.Links = []fact.Link{{Target: "[[b]]", Relation: "depends-on", Why: "needs b"}}
	must(t, ix.IndexLinks(fa))

	edges, err := ix.Why("a", "b")
	if err != nil {
		t.Fatalf("Why: %v", err)
	}
	if len(edges) != 1 || edges[0].SrcTitle != "Fact A" || edges[0].DstTitle != "Fact B" ||
		edges[0].SrcSnippet != "Body A." || edges[0].DstSnippet != "Body B." {
		t.Fatalf("Why(a,b) = %+v, want both sides populated", edges)
	}
}

func TestAcceptance_AC3_TC3_2_WhyWithNoDirectLinkReturnsEmptyNoError(t *testing.T) {
	ix := openMem(t)
	must(t, ix.IndexFact(sampleFact("a", "Fact A", "x", "decision", "bbrain"), "/x/a.md"))
	must(t, ix.IndexFact(sampleFact("b", "Fact B", "y", "decision", "bbrain"), "/x/b.md"))

	edges, err := ix.Why("a", "b")
	if err != nil {
		t.Fatalf("Why: %v", err)
	}
	if len(edges) != 0 {
		t.Fatalf("Why(a,b) with no link = %+v, want empty", edges)
	}
}

func TestAcceptance_AC4_TC4_1_ZeroNeighborsReturnsEmptyListNoError(t *testing.T) {
	ix := openMem(t)
	must(t, ix.IndexFact(sampleFact("a", "Fact A", "x", "decision", "bbrain"), "/x/a.md"))

	ns, err := ix.Neighbors("a")
	if err != nil {
		t.Fatalf("Neighbors: %v", err)
	}
	if len(ns) != 0 {
		t.Fatalf("Neighbors(a) with no links = %+v, want empty", ns)
	}
}

func TestAcceptance_AC4_TC4_2_NonexistentFactIDReturnsEmptyListNoError(t *testing.T) {
	ix := openMem(t)

	ns, err := ix.Neighbors("does-not-exist")
	if err != nil {
		t.Fatalf("Neighbors: %v", err)
	}
	if len(ns) != 0 {
		t.Fatalf("Neighbors(nonexistent) = %+v, want empty", ns)
	}
}

func TestAcceptance_AC5_TC5_1_DanglingLinkReturnsRowWithEmptyTitleSnippet(t *testing.T) {
	ix := openMem(t)
	fa := sampleFact("a", "Fact A", "x", "decision", "bbrain")
	fa.Links = []fact.Link{{Target: "[[b]]", Relation: "depends-on", Why: "needs b"}}
	must(t, ix.IndexLinks(fa))

	ns, err := ix.Neighbors("a")
	if err != nil {
		t.Fatalf("Neighbors: %v", err)
	}
	if len(ns) != 1 || ns[0].FactID != "b" || ns[0].Title != "" || ns[0].Snippet != "" {
		t.Fatalf("Neighbors(a) = %+v, want dangling row with fact_id set, title/snippet empty", ns)
	}
}

func TestAcceptance_AC5_TC5_2_DanglingLinkCallReturnsNoError(t *testing.T) {
	ix := openMem(t)
	fa := sampleFact("a", "Fact A", "x", "decision", "bbrain")
	fa.Links = []fact.Link{{Target: "[[b]]", Relation: "depends-on", Why: "needs b"}}
	must(t, ix.IndexLinks(fa))

	if _, err := ix.Neighbors("a"); err != nil {
		t.Fatalf("Neighbors on dangling link returned an error, want nil: %v", err)
	}
	if _, err := ix.Why("a", "b"); err != nil {
		t.Fatalf("Why on dangling link returned an error, want nil: %v", err)
	}
}
