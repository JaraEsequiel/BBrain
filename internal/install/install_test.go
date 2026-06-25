package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func projOpts(t *testing.T) Options {
	t.Helper()
	root := t.TempDir()
	return Options{
		Vault:      filepath.Join(root, "vault"),
		Agent:      "claude-code",
		Scope:      "project",
		Model:      "claude-sonnet-4-6",
		HomeDir:    filepath.Join(root, "home"),
		ProjectDir: filepath.Join(root, "proj"),
	}
}

func TestPlanInstallProjectScopeActions(t *testing.T) {
	o := projOpts(t)
	acts, err := PlanInstall(o)
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]int{}
	paths := []string{}
	for _, a := range acts {
		kinds[a.Kind]++
		paths = append(paths, a.Path)
	}
	if kinds["mkbrain"] != 1 || kinds["merge-mcp"] != 1 || kinds["merge-settings"] != 1 {
		t.Fatalf("unexpected action kinds: %v", kinds)
	}
	joined := strings.Join(paths, "\n")
	for _, want := range []string{
		filepath.Join(o.Vault, "CLAUDE.md"),
		filepath.Join(o.Vault, "memory"),
		filepath.Join(o.ProjectDir, ".mcp.json"),
		filepath.Join(o.ProjectDir, "CLAUDE.md"),
		filepath.Join(o.ProjectDir, ".claude", "settings.json"),
		filepath.Join(o.ProjectDir, ".claude", "skills", "bbrain-recall", "SKILL.md"),
		filepath.Join(o.ProjectDir, ".claude", "skills", "bbrain-remember", "SKILL.md"),
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("plan missing action for %s:\n%s", want, joined)
		}
	}
}

func TestPlanInstallUserScopeUsesMCPCLI(t *testing.T) {
	o := projOpts(t)
	o.Scope = "user"
	acts, err := PlanInstall(o)
	if err != nil {
		t.Fatal(err)
	}
	var sawAdd bool
	for _, a := range acts {
		if a.Kind == "mcp-cli" && len(a.Argv) > 2 && a.Argv[2] == "add" {
			sawAdd = true
			mem := filepath.Join(o.Vault, "memory")
			if strings.Join(a.Argv, " ") != "claude mcp add -s user bbrain -e BBRAIN_HOME="+mem+" -- bbrain mcp --home "+mem {
				t.Fatalf("mcp-cli add argv = %v", a.Argv)
			}
		}
		// user-scope CLAUDE.md/settings go under HomeDir/.claude
		if a.Kind == "merge-settings" && !strings.Contains(a.Path, filepath.Join(o.HomeDir, ".claude")) {
			t.Fatalf("user-scope settings path = %s", a.Path)
		}
	}
	if !sawAdd {
		t.Fatal("user scope must register MCP via the claude CLI add command")
	}
}

func TestApplyAndUninstallProjectScope(t *testing.T) {
	o := projOpts(t)
	acts, err := PlanInstall(o)
	must(t, err)
	must(t, Apply(acts))

	// vault brain + degraded CLAUDE.md
	if _, err := os.Stat(filepath.Join(o.Vault, "memory", "raws", "facts")); err != nil {
		t.Fatalf("memory brain not created: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(o.Vault, "CLAUDE.md")); !strings.Contains(string(b), "manual access") {
		t.Fatal("degraded CLAUDE.md missing")
	}
	// project integration
	if b, _ := os.ReadFile(filepath.Join(o.ProjectDir, ".mcp.json")); !strings.Contains(string(b), "bbrain") {
		t.Fatal(".mcp.json missing bbrain")
	}
	if b, _ := os.ReadFile(filepath.Join(o.ProjectDir, "CLAUDE.md")); !strings.Contains(string(b), "BBRAIN:BEGIN") {
		t.Fatal("integration CLAUDE.md block missing")
	}
	if b, _ := os.ReadFile(filepath.Join(o.ProjectDir, ".claude", "settings.json")); !strings.Contains(string(b), "SessionStart") {
		t.Fatal("SessionStart hook missing")
	}
	if _, err := os.Stat(filepath.Join(o.ProjectDir, ".claude", "skills", "bbrain-recall", "SKILL.md")); err != nil {
		t.Fatalf("recall skill missing: %v", err)
	}
	// idempotent re-apply
	acts2, _ := PlanInstall(o)
	must(t, Apply(acts2))
	if b, _ := os.ReadFile(filepath.Join(o.ProjectDir, "CLAUDE.md")); strings.Count(string(b), "BBRAIN:BEGIN") != 1 {
		t.Fatal("re-apply duplicated the managed block")
	}

	// uninstall reverses (vault kept)
	uacts, err := PlanUninstall(o)
	must(t, err)
	must(t, Apply(uacts))
	if b, _ := os.ReadFile(filepath.Join(o.ProjectDir, "CLAUDE.md")); strings.Contains(string(b), "BBRAIN:BEGIN") {
		t.Fatal("uninstall left the managed block")
	}
	if b, _ := os.ReadFile(filepath.Join(o.ProjectDir, ".mcp.json")); strings.Contains(string(b), "bbrain") {
		t.Fatal("uninstall left bbrain in .mcp.json")
	}
	if _, err := os.Stat(filepath.Join(o.ProjectDir, ".claude", "skills", "bbrain-recall")); !os.IsNotExist(err) {
		t.Fatal("uninstall left the recall skill")
	}
	if _, err := os.Stat(filepath.Join(o.Vault, "memory")); err != nil {
		t.Fatal("uninstall without --purge deleted the vault")
	}
}

func TestPlanInstallUserScopeIsIdempotentViaRemoveThenAdd(t *testing.T) {
	o := projOpts(t)
	o.Scope = "user"
	acts, err := PlanInstall(o)
	if err != nil {
		t.Fatal(err)
	}
	var removeIdx, addIdx = -1, -1
	for i, a := range acts {
		if a.Kind != "mcp-cli" {
			continue
		}
		if len(a.Argv) > 2 && a.Argv[2] == "remove" {
			removeIdx = i
			if !a.IgnoreError {
				t.Fatal("the pre-remove must set IgnoreError")
			}
		}
		if len(a.Argv) > 2 && a.Argv[2] == "add" {
			addIdx = i
		}
	}
	if removeIdx < 0 || addIdx < 0 || removeIdx > addIdx {
		t.Fatalf("user scope must emit remove before add: remove=%d add=%d", removeIdx, addIdx)
	}
}

func TestWizardParsesStdin(t *testing.T) {
	in := strings.NewReader("/my/vault\n\nuser\n") // vault, agent(blank→default), scope
	def := Options{Vault: "/default", Agent: "claude-code", Scope: "project"}
	var out strings.Builder
	got, err := Wizard(in, &out, def)
	must(t, err)
	if got.Vault != "/my/vault" || got.Agent != "claude-code" || got.Scope != "user" {
		t.Fatalf("wizard result = %+v", got)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestNormalizeVault(t *testing.T) {
	home, err := os.UserHomeDir()
	must(t, err)
	cwd, err := os.Getwd()
	must(t, err)
	cases := []struct{ in, want string }{
		{"~", home},
		{"~/brain", filepath.Join(home, "brain")},
		{"/abs/vault", "/abs/vault"},
		{"rel/vault", filepath.Join(cwd, "rel", "vault")},
		{"  ~/spaced  ", filepath.Join(home, "spaced")},
	}
	for _, c := range cases {
		got, err := normalizeVault(c.in)
		must(t, err)
		if got != c.want {
			t.Errorf("normalizeVault(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	if _, err := normalizeVault("   "); err == nil {
		t.Error("normalizeVault(empty) should error")
	}
}

func TestPlanInstallNormalizesVault(t *testing.T) {
	o := projOpts(t)
	o.Vault = "relative/vault" // not absolute: must be normalized before baking into actions
	cwd, err := os.Getwd()
	must(t, err)
	wantMem := filepath.Join(cwd, "relative", "vault", "memory")
	acts, err := PlanInstall(o)
	must(t, err)
	var found bool
	for _, a := range acts {
		if a.Kind == "mkbrain" && a.Path == wantMem {
			found = true
		}
	}
	if !found {
		t.Fatalf("PlanInstall did not normalize relative vault into action paths (want mkbrain %s)", wantMem)
	}
}
