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
	// args must carry --home so the brain is found even when env is dropped.
	args := bb["args"].([]any)
	var gotHome bool
	for i, a := range args {
		if a == "--home" && i+1 < len(args) && args[i+1] == "/home/u/.bbrain/default" {
			gotHome = true
		}
	}
	if !gotHome {
		t.Fatalf("args missing --home /home/u/.bbrain/default: %v", args)
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
	for _, want := range []string{BlockBegin, BlockEnd, "mcp__bbrain__mem_save", "mcp__bbrain__wiki_build", "/adapter.sh", "ToolSearch", "SESSION CLOSE", "POST-COMPACTION"} {
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

func TestDegradedClaudeMD(t *testing.T) {
	s := DegradedClaudeMD("/vault/memory")
	for _, want := range []string{"memory/raws/facts", "frontmatter", "[[fact-id]]", "/vault/memory", "wiki/index.md"} {
		if !strings.Contains(s, want) {
			t.Fatalf("degraded doc missing %q:\n%s", want, s)
		}
	}
}

func TestSessionStartHookAndMerge(t *testing.T) {
	// merge into a settings.json that already has an unrelated hook + key
	existing := []byte(`{"hooks":{"PreToolUse":[{"matcher":"Bash"}]},"env":{"X":"1"}}`)
	out, err := MergeSettingsHook(existing, "/v/memory")
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal(out, &root); err != nil {
		t.Fatalf("not valid JSON: %v\n%s", err, out)
	}
	hooks := root["hooks"].(map[string]any)
	if _, ok := hooks["PreToolUse"]; !ok {
		t.Fatal("merge dropped the unrelated PreToolUse hook")
	}
	if root["env"] == nil {
		t.Fatal("merge dropped top-level env")
	}
	ss := hooks["SessionStart"].([]any)
	if len(ss) != 1 {
		t.Fatalf("want 1 SessionStart entry, got %d", len(ss))
	}
	// the hook command is bbrain context --home /v/memory
	js, _ := json.Marshal(ss)
	for _, want := range []string{`"bbrain"`, `"context"`, `"--home"`, `/v/memory`, "compact", "clear"} {
		if !strings.Contains(string(js), want) {
			t.Fatalf("hook missing %q:\n%s", want, js)
		}
	}
	// idempotent: merging again yields exactly one SessionStart entry
	out2, _ := MergeSettingsHook(out, "/v/memory")
	json.Unmarshal(out2, &root)
	if n := len(root["hooks"].(map[string]any)["SessionStart"].([]any)); n != 1 {
		t.Fatalf("merge not idempotent: %d SessionStart entries", n)
	}
	// removal strips ours, keeps the unrelated one
	rem, err := RemoveSettingsHook(out2)
	if err != nil {
		t.Fatal(err)
	}
	json.Unmarshal(rem, &root)
	h := root["hooks"].(map[string]any)
	if _, ok := h["SessionStart"]; ok {
		t.Fatalf("RemoveSettingsHook left SessionStart:\n%s", rem)
	}
	if _, ok := h["PreToolUse"]; !ok {
		t.Fatal("RemoveSettingsHook dropped the unrelated hook")
	}
}

func TestRemoveMCPServer(t *testing.T) {
	existing := []byte(`{"mcpServers":{"bbrain":{"type":"stdio"},"other":{"type":"stdio"}}}`)
	out, err := RemoveMCPServer(existing)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "bbrain") {
		t.Fatalf("bbrain not removed:\n%s", out)
	}
	if !strings.Contains(string(out), "other") {
		t.Fatalf("removal dropped the other server:\n%s", out)
	}
}

func TestSkillsAndRemoveBlock(t *testing.T) {
	if r := RecallSkill(); !strings.Contains(r, "description:") || !strings.Contains(r, "mcp__bbrain__mem_search") {
		t.Fatalf("recall skill:\n%s", r)
	}
	if r := RememberSkill(); !strings.Contains(r, "mcp__bbrain__mem_save") {
		t.Fatalf("remember skill:\n%s", r)
	}
	doc := "# Top\n\n" + ClaudeMDBlock("/m", "/a.sh") + "\n"
	out := RemoveManagedBlock(doc)
	if strings.Contains(out, BlockBegin) || strings.Contains(out, BlockEnd) {
		t.Fatalf("managed block not removed:\n%s", out)
	}
	if !strings.Contains(out, "# Top") {
		t.Fatalf("RemoveManagedBlock dropped user content:\n%s", out)
	}
}

func TestRemoveSettingsHookNoHooksKeyReturnsCanonicalJSON(t *testing.T) {
	out, err := RemoveSettingsHook([]byte(`{"env":{"X":"1"}}`))
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal(out, &root); err != nil {
		t.Fatalf("not valid JSON: %v\n%s", err, out)
	}
	if root["env"] == nil {
		t.Fatalf("dropped env: %s", out)
	}
	// canonical (indented) output, not the raw input
	if !strings.Contains(string(out), "\n  ") {
		t.Fatalf("not canonical/indented JSON: %s", out)
	}
}

func TestMergeSettingsInstallsBothHooks(t *testing.T) {
	out, err := MergeSettingsHook(nil, "/mem")
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, want := range []string{"SessionStart", "UserPromptSubmit", "prompt-submit", "context"} {
		if !strings.Contains(s, want) {
			t.Fatalf("merged settings missing %q:\n%s", want, s)
		}
	}
	// Idempotent: a second merge must not duplicate the UserPromptSubmit entry.
	out2, err := MergeSettingsHook(out, "/mem")
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(out2), "prompt-submit"); n != 1 {
		t.Fatalf("re-merge produced %d prompt-submit entries; want 1:\n%s", n, out2)
	}
	if n := strings.Count(string(out2), "context"); n != 1 {
		t.Fatalf("re-merge produced %d context (SessionStart) entries; want 1:\n%s", n, out2)
	}
}

func TestRemoveSettingsStripsBothHooksKeepsForeign(t *testing.T) {
	seed := []byte(`{"hooks":{"UserPromptSubmit":[{"hooks":[{"command":"other","args":["keepme"]}]}]}}`)
	merged, err := MergeSettingsHook(seed, "/mem")
	if err != nil {
		t.Fatal(err)
	}
	out, err := RemoveSettingsHook(merged)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if strings.Contains(s, "prompt-submit") || strings.Contains(s, `"context"`) {
		t.Fatalf("remove left BBrain hooks:\n%s", s)
	}
	if !strings.Contains(s, "keepme") {
		t.Fatalf("remove dropped a foreign hook:\n%s", s)
	}
}
