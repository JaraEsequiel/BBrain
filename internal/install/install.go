// Package install builds and applies the BBrain → Claude Code integration: an
// interactive TUI wizard, a scope-aware plan of filesystem/CLI actions, and an
// applier. Reversible.
package install

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	huh "charm.land/huh/v2"
	"github.com/JaraEsequiel/BBrain/internal/brain"
	"github.com/JaraEsequiel/BBrain/internal/setup"
	"github.com/natefinch/atomic"
)

// Options is a resolved install/uninstall configuration.
type Options struct {
	Vault      string // L (chosen vault location)
	Agent      string // "claude-code"
	Scope      string // "user" | "project"
	Model      string // claude model for the adapter
	HomeDir    string // ~ root (Claude Code user config parent); for tests
	ProjectDir string // cwd for project scope; for tests
	DryRun     bool
	Purge      bool // uninstall: also delete the vault
}

func (o Options) memoryDir() string  { return filepath.Join(o.Vault, "memory") }
func (o Options) adapterPath() string {
	return filepath.Join(o.memoryDir(), ".bbrain", "agents", "claude-code.sh")
}
func (o Options) claudeUserDir() string { return filepath.Join(o.HomeDir, ".claude") }

func (o Options) claudeMDPath() string {
	if o.Scope == "user" {
		return filepath.Join(o.claudeUserDir(), "CLAUDE.md")
	}
	return filepath.Join(o.ProjectDir, "CLAUDE.md")
}
func (o Options) settingsPath() string {
	if o.Scope == "user" {
		return filepath.Join(o.claudeUserDir(), "settings.json")
	}
	return filepath.Join(o.ProjectDir, ".claude", "settings.json")
}
func (o Options) skillsDir() string {
	if o.Scope == "user" {
		return filepath.Join(o.claudeUserDir(), "skills")
	}
	return filepath.Join(o.ProjectDir, ".claude", "skills")
}

// Action is one filesystem/CLI operation in a plan.
type Action struct {
	Kind        string     // mkbrain|write|merge-md|merge-mcp|merge-settings|mcp-cli|remove-md|remove-mcp|remove-settings|rmdir
	Path        string     // target (empty for mcp-cli)
	Summary     string
	Content     string     // new full content (for write/merge; shown on dry-run)
	Mode        os.FileMode
	Argv        []string   // for mcp-cli
	IgnoreError bool       // for mcp-cli: don't fail the apply if the command errors
}

// readMaybe reads path, treating "not exist" as empty (nil) but propagating any
// other error so an existing-but-unreadable file is never silently overwritten.
func readMaybe(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("install: read %s: %w", path, err)
	}
	return b, nil
}

// normalizeVault expands a leading ~ to the user's home directory and resolves
// the path to an absolute, cleaned form. Without this, a relative path or a
// literal "~" chosen in the wizard gets baked verbatim into env.sh, .mcp.json,
// and CLAUDE.md — breaking when Claude Code launches the MCP server from a
// different working directory. Idempotent on already-absolute paths.
func normalizeVault(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("install: vault path is empty")
	}
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("install: cannot expand ~: %w", err)
		}
		// Trim only the "~", not "~/", so the "~" alone case (→ home) works too.
		// filepath.Join treats the resulting "/sub" as a path component, not a
		// root, so Join(home, "/sub") == home/sub — do not "simplify" to "~/".
		path = filepath.Join(home, strings.TrimPrefix(path, "~"))
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("install: resolve vault path %q: %w", path, err)
	}
	return abs, nil
}

// PlanInstall computes the ordered actions for opts (pure: reads existing files to
// compute merges but writes nothing).
func PlanInstall(o Options) ([]Action, error) {
	nv, err := normalizeVault(o.Vault)
	if err != nil {
		return nil, err
	}
	o.Vault = nv
	mem := o.memoryDir()
	adapter := o.adapterPath()
	var acts []Action

	// 1. vault: brain + degraded CLAUDE.md + adapter + env.sh
	acts = append(acts,
		Action{Kind: "mkbrain", Path: mem, Summary: "create memory vault (BBRAIN_HOME)"},
		Action{Kind: "write", Path: filepath.Join(o.Vault, "CLAUDE.md"), Summary: "degraded-mode reader doc",
			Content: setup.DegradedClaudeMD(mem), Mode: 0o644},
		Action{Kind: "write", Path: adapter, Summary: "LLM agent adapter",
			Content: setup.AdapterScript(o.Model), Mode: 0o755},
		Action{Kind: "write", Path: filepath.Join(mem, ".bbrain", "env.sh"), Summary: "BBRAIN_AGENT_CLI export",
			Content: setup.EnvExportLine(adapter) + "\n", Mode: 0o644},
	)

	// 2. integration CLAUDE.md (managed block)
	cmPath := o.claudeMDPath()
	doc, err := readMaybe(cmPath)
	if err != nil {
		return nil, err
	}
	acts = append(acts, Action{Kind: "merge-md", Path: cmPath, Summary: "integration CLAUDE.md block",
		Content: setup.UpsertManagedBlock(string(doc), setup.ClaudeMDBlock(mem, adapter)), Mode: 0o644})

	// 3. MCP registration
	if o.Scope == "user" {
		acts = append(acts,
			Action{Kind: "mcp-cli", Summary: "drop any prior bbrain MCP (user)", IgnoreError: true,
				Argv: []string{"claude", "mcp", "remove", "-s", "user", "bbrain"}},
			Action{Kind: "mcp-cli", Summary: "register bbrain MCP (user scope)",
				Argv: []string{"claude", "mcp", "add", "-s", "user", "bbrain", "-e", "BBRAIN_HOME=" + mem, "--", "bbrain", "mcp", "--home", mem}})
	} else {
		mcpPath := filepath.Join(o.ProjectDir, ".mcp.json")
		existing, err := readMaybe(mcpPath)
		if err != nil {
			return nil, err
		}
		merged, err := setup.MergeMCPConfig(existing, mem)
		if err != nil {
			return nil, err
		}
		acts = append(acts, Action{Kind: "merge-mcp", Path: mcpPath, Summary: "register bbrain MCP (project)",
			Content: string(merged) + "\n", Mode: 0o644})
	}

	// 4. SessionStart hook
	setPath := o.settingsPath()
	setBytes, err := readMaybe(setPath)
	if err != nil {
		return nil, err
	}
	mergedSet, err := setup.MergeSettingsHook(setBytes, mem)
	if err != nil {
		return nil, err
	}
	acts = append(acts, Action{Kind: "merge-settings", Path: setPath, Summary: "SessionStart context hook",
		Content: string(mergedSet) + "\n", Mode: 0o644})

	// 5. skills
	skills := o.skillsDir()
	acts = append(acts,
		Action{Kind: "write", Path: filepath.Join(skills, "bbrain-recall", "SKILL.md"), Summary: "skill /bbrain-recall",
			Content: setup.RecallSkill(), Mode: 0o644},
		Action{Kind: "write", Path: filepath.Join(skills, "bbrain-remember", "SKILL.md"), Summary: "skill /bbrain-remember",
			Content: setup.RememberSkill(), Mode: 0o644},
	)
	return acts, nil
}

// PlanUninstall computes the reversal actions for opts.
func PlanUninstall(o Options) ([]Action, error) {
	nv, err := normalizeVault(o.Vault)
	if err != nil {
		return nil, err
	}
	o.Vault = nv
	var acts []Action
	// integration CLAUDE.md: strip the managed block (only if the file actually contains it)
	if doc, err := os.ReadFile(o.claudeMDPath()); err == nil && strings.Contains(string(doc), setup.BlockBegin) {
		acts = append(acts, Action{Kind: "remove-md", Path: o.claudeMDPath(), Summary: "remove integration CLAUDE.md block",
			Content: setup.RemoveManagedBlock(string(doc))})
	}
	// MCP
	if o.Scope == "user" {
		acts = append(acts, Action{Kind: "mcp-cli", Summary: "unregister bbrain MCP (user)",
			Argv: []string{"claude", "mcp", "remove", "-s", "user", "bbrain"}})
	} else {
		mcpPath := filepath.Join(o.ProjectDir, ".mcp.json")
		if existing, err := os.ReadFile(mcpPath); err == nil {
			cleaned, err := setup.RemoveMCPServer(existing)
			if err != nil {
				return nil, err
			}
			acts = append(acts, Action{Kind: "remove-mcp", Path: mcpPath, Summary: "remove bbrain from .mcp.json",
				Content: string(cleaned) + "\n"})
		}
	}
	// settings hook
	if existing, err := os.ReadFile(o.settingsPath()); err == nil {
		cleaned, err := setup.RemoveSettingsHook(existing)
		if err != nil {
			return nil, err
		}
		acts = append(acts, Action{Kind: "remove-settings", Path: o.settingsPath(), Summary: "remove SessionStart hook",
			Content: string(cleaned) + "\n"})
	}
	// skills
	acts = append(acts,
		Action{Kind: "rmdir", Path: filepath.Join(o.skillsDir(), "bbrain-recall"), Summary: "remove /bbrain-recall skill"},
		Action{Kind: "rmdir", Path: filepath.Join(o.skillsDir(), "bbrain-remember"), Summary: "remove /bbrain-remember skill"},
	)
	if o.Purge {
		acts = append(acts, Action{Kind: "rmdir", Path: o.Vault, Summary: "purge the vault (DELETES memory)"})
	}
	return acts, nil
}

// Apply executes a plan.
func Apply(actions []Action) error {
	for _, a := range actions {
		switch a.Kind {
		case "mkbrain":
			if err := brain.New(a.Path).Init(); err != nil {
				return fmt.Errorf("install: init vault %s: %w", a.Path, err)
			}
		case "write":
			if err := os.MkdirAll(filepath.Dir(a.Path), 0o755); err != nil {
				return err
			}
			mode := a.Mode
			if mode == 0 {
				mode = 0o644
			}
			if err := os.WriteFile(a.Path, []byte(a.Content), mode); err != nil {
				return fmt.Errorf("install: write %s: %w", a.Path, err)
			}
		case "merge-md", "merge-mcp", "merge-settings", "remove-md", "remove-mcp", "remove-settings":
			if err := os.MkdirAll(filepath.Dir(a.Path), 0o755); err != nil {
				return err
			}
			if err := atomic.WriteFile(a.Path, strings.NewReader(a.Content)); err != nil {
				return fmt.Errorf("install: write %s: %w", a.Path, err)
			}
		case "mcp-cli":
			out, err := exec.Command(a.Argv[0], a.Argv[1:]...).CombinedOutput()
			if err != nil && !a.IgnoreError {
				return fmt.Errorf("install: %v: %w (%s)", a.Argv, err, strings.TrimSpace(string(out)))
			}
		case "rmdir":
			if clean := filepath.Clean(a.Path); clean == "" || clean == "/" || clean == "." {
				return fmt.Errorf("install: refusing to remove unsafe path %q", a.Path)
			}
			if err := os.RemoveAll(a.Path); err != nil {
				return err
			}
		}
	}
	return nil
}

// ErrAborted is returned by Wizard when the user cancels the TUI (Ctrl-C or
// declining the final confirmation), so the caller can exit cleanly.
var ErrAborted = errors.New("install: cancelled by user")

// Wizard runs the interactive TUI — arrow-key scope select, a validated vault
// input, and a final confirm — and returns resolved Options. def supplies the
// shown defaults. It drives the real terminal (os.Stdin/os.Stdout); the caller
// must ensure a TTY before invoking. The returned Vault is the raw text the
// user accepted; PlanInstall normalizes it.
func Wizard(def Options) (Options, error) {
	o := def
	o.Agent = "claude-code" // the only supported agent; never asked

	scope := def.Scope
	if scope != "user" && scope != "project" {
		scope = "project"
	}
	vault := def.Vault
	confirmed := true

	form := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Scope de instalación").
				Description("Dónde se registra BBrain en Claude Code").
				Options(
					huh.NewOption("project — este repositorio", "project"),
					huh.NewOption("user — global (todos los proyectos)", "user"),
				).
				Value(&scope),
			huh.NewInput().
				Title("Ubicación del vault").
				Description("Carpeta de la memoria (~ y rutas relativas se resuelven a absolutas)").
				Value(&vault).
				Validate(func(s string) error {
					_, err := normalizeVault(s)
					return err
				}),
		),
		huh.NewGroup(
			huh.NewConfirm().
				Title("¿Instalar BBrain con esta configuración?").
				Affirmative("Sí, instalar").
				Negative("Cancelar").
				Value(&confirmed),
		),
	)

	if err := form.Run(); err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			return o, ErrAborted
		}
		return o, err
	}
	if !confirmed {
		return o, ErrAborted
	}
	o.Scope = scope
	o.Vault = vault
	return o, nil
}
