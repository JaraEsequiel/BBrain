package setup

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAdapterScript(t *testing.T) {
	s := AdapterScript("claude-sonnet-4-6")
	for _, want := range []string{"#!/bin/sh", "claude -p", "--append-system-prompt", "claude-sonnet-4-6", "BBRAIN_CLAUDE_MODEL", "python3", `re.search`} {
		if !strings.Contains(s, want) {
			t.Fatalf("adapter missing %q:\n%s", want, s)
		}
	}
}

func TestMergeMCPConfigInsertsAndPreserves(t *testing.T) {
	existing := []byte(`{"mcpServers":{"other":{"type":"stdio","command":"x"}},"someKey":1}`)
	out, err := MergeMCPConfig(existing, "/home/u/.bbrain/default")
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal(out, &root); err != nil {
		t.Fatalf("output not valid JSON: %v\n%s", err, out)
	}
	servers := root["mcpServers"].(map[string]any)
	if _, ok := servers["other"]; !ok {
		t.Fatal("merge dropped the pre-existing 'other' server")
	}
	if root["someKey"] == nil {
		t.Fatal("merge dropped top-level someKey")
	}
	bb := servers["bbrain"].(map[string]any)
	if bb["command"] != "bbrain" || bb["type"] != "stdio" {
		t.Fatalf("bbrain entry wrong: %v", bb)
	}
	env := bb["env"].(map[string]any)
	if env["BBRAIN_HOME"] != "/home/u/.bbrain/default" {
		t.Fatalf("BBRAIN_HOME wrong: %v", env)
	}
}

func TestMergeMCPConfigEmptyInput(t *testing.T) {
	out, err := MergeMCPConfig(nil, "/b")
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal(out, &root); err != nil {
		t.Fatal(err)
	}
	if _, ok := root["mcpServers"].(map[string]any)["bbrain"]; !ok {
		t.Fatalf("bbrain not added to empty config: %s", out)
	}
}

func TestUpsertManagedBlockInsertThenReplaceIdempotent(t *testing.T) {
	block := ClaudeMDBlock("/b", "/b/.bbrain/agents/claude-code.sh")
	// insert into a doc with existing content
	got := UpsertManagedBlock("# Project\n\nHello.\n", block)
	if !strings.Contains(got, "# Project") || strings.Count(got, BlockBegin) != 1 {
		t.Fatalf("insert wrong:\n%s", got)
	}
	// replacing yields exactly one block, and is idempotent
	again := UpsertManagedBlock(got, block)
	if again != got {
		t.Fatalf("upsert not idempotent:\n--first--\n%s\n--second--\n%s", got, again)
	}
	if strings.Count(again, BlockBegin) != 1 || strings.Count(again, BlockEnd) != 1 {
		t.Fatalf("duplicate markers:\n%s", again)
	}
}

func TestClaudeMDBlockMentionsToolsAndMarkers(t *testing.T) {
	b := ClaudeMDBlock("/b", "/adapter.sh")
	for _, want := range []string{BlockBegin, BlockEnd, "mcp__bbrain__mem_save", "mcp__bbrain__wiki_build", "/adapter.sh"} {
		if !strings.Contains(b, want) {
			t.Fatalf("block missing %q:\n%s", want, b)
		}
	}
}

func TestEnvExportLine(t *testing.T) {
	if got := EnvExportLine("/x/y.sh"); got != `export BBRAIN_AGENT_CLI='/x/y.sh'` {
		t.Fatalf("env line = %q", got)
	}
	// a single quote in the path is escaped, not break-out
	if got := EnvExportLine("/a'b"); got != `export BBRAIN_AGENT_CLI='/a'\''b'` {
		t.Fatalf("env line (quote) = %q", got)
	}
}

func TestAdapterScriptRejectsUnsafeModel(t *testing.T) {
	s := AdapterScript(`x"; rm -rf / #`)
	if strings.Contains(s, "rm -rf") {
		t.Fatalf("unsafe model interpolated into adapter:\n%s", s)
	}
	if !strings.Contains(s, "claude-sonnet-4-6") {
		t.Fatalf("expected fallback model:\n%s", s)
	}
}

func TestUpsertManagedBlockHalfOpenMarkers(t *testing.T) {
	block := ClaudeMDBlock("/b", "/a.sh")
	// only a BEGIN marker present (corrupt doc): result must have exactly one pair
	for _, corrupt := range []string{
		"# Doc\n" + BlockBegin + "\nstray\n",
		"# Doc\n" + BlockEnd + "\nstray\n",
		"# Doc\n" + BlockEnd + "\nmiddle\n" + BlockBegin + "\n",
	} {
		got := UpsertManagedBlock(corrupt, block)
		if strings.Count(got, BlockBegin) != 1 || strings.Count(got, BlockEnd) != 1 {
			t.Fatalf("half-open upsert left bad markers:\n%s", got)
		}
		// idempotent thereafter
		if again := UpsertManagedBlock(got, block); again != got {
			t.Fatalf("not idempotent after repair")
		}
	}
}
