// Package llm is BBrain's boundary to an external, pluggable LLM CLI. BBrain
// orchestrates; the LLM is a pure text->text function invoked by shelling out to
// the command in $BBRAIN_AGENT_CLI (prompt on stdin, response on stdout). Only
// internal/wiki and internal/app import this package.
package llm

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Runner sends a prompt to an external LLM CLI and returns its raw stdout.
type Runner interface {
	Run(ctx context.Context, prompt string) (string, error)
}

var (
	// ErrCLINotConfigured is returned when BBRAIN_AGENT_CLI is empty.
	ErrCLINotConfigured = errors.New("llm: BBRAIN_AGENT_CLI is not set")
	// ErrCLINotInstalled is returned when the configured command is not in PATH.
	ErrCLINotInstalled = errors.New("llm: agent CLI command not found in PATH")
	// ErrTimeout is returned when the CLI call exceeds the runner's timeout.
	ErrTimeout = errors.New("llm: agent CLI call timed out")
)

// DefaultTimeout bounds a single CLI call.
const DefaultTimeout = 120 * time.Second

// DistillTimeout bounds a single wiki distillation call. Wiki build/link drive
// an agentic backend (e.g. `claude -p`) whose per-call latency floor is ~50s of
// fixed overhead before it emits a token; distilling a batch of facts into
// several markdown pages then adds seconds of generation on top. The 120s
// interactive DefaultTimeout is too tight for that — batches timed out at 120s
// against a real vault — so distillation gets a larger, non-interactive budget.
const DistillTimeout = 300 * time.Second

// CLIRunner runs Command (default: $BBRAIN_AGENT_CLI), piping the prompt to the
// process's stdin and reading the response from its stdout.
type CLIRunner struct {
	Command string        // full command line, e.g. "claude -p"
	Timeout time.Duration // 0 means DefaultTimeout
}

// NewCLIRunner builds a CLIRunner from $BBRAIN_AGENT_CLI with DefaultTimeout.
func NewCLIRunner() *CLIRunner {
	return &CLIRunner{Command: os.Getenv("BBRAIN_AGENT_CLI"), Timeout: DefaultTimeout}
}

// NewCLIRunnerFor builds a CLIRunner whose command is resolved with AgentCLI,
// so the agent CLI works even when the process inherited no BBRAIN_AGENT_CLI
// (the common case for an MCP server launched without a sourced shell profile).
func NewCLIRunnerFor(home string) *CLIRunner {
	return &CLIRunner{Command: AgentCLI(home), Timeout: DefaultTimeout}
}

// NewCLIRunnerForTimeout is NewCLIRunnerFor with a caller-chosen timeout — used
// for long, non-interactive operations like wiki distillation (DistillTimeout).
func NewCLIRunnerForTimeout(home string, d time.Duration) *CLIRunner {
	r := NewCLIRunnerFor(home)
	r.Timeout = d
	return r
}

// AgentCLI resolves the agent CLI command line. It prefers the BBRAIN_AGENT_CLI
// environment variable; when that is empty, it falls back to the value exported
// in <home>/.bbrain/env.sh — the file `bbrain install` writes on every machine —
// so the wiki LLM backend survives an environment drop. Returns "" when neither
// source provides a command.
func AgentCLI(home string) string {
	if v := os.Getenv("BBRAIN_AGENT_CLI"); v != "" {
		return v
	}
	return agentCLIFromEnvFile(filepath.Join(home, ".bbrain", "env.sh"))
}

// agentCLIFromEnvFile parses `export BBRAIN_AGENT_CLI='...'` (as written by
// setup.EnvExportLine) out of an env.sh. Returns "" if the file is unreadable or
// has no such line.
func agentCLIFromEnvFile(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimPrefix(strings.TrimSpace(line), "export ")
		v, ok := strings.CutPrefix(line, "BBRAIN_AGENT_CLI=")
		if !ok {
			continue
		}
		return shellSingleUnquote(strings.TrimSpace(v))
	}
	return ""
}

// shellSingleUnquote reverses setup.shellSingleQuote: a single-quoted shell
// string where an embedded ' is written as '\''. Non-single-quoted input is
// returned unchanged.
func shellSingleUnquote(s string) string {
	if len(s) < 2 || s[0] != '\'' || s[len(s)-1] != '\'' {
		return s
	}
	return strings.ReplaceAll(s[1:len(s)-1], `'\''`, `'`)
}

// Run shells out to the configured command. The Command is split on whitespace
// (no shell quoting), so wrap complex invocations in a script.
func (r *CLIRunner) Run(ctx context.Context, prompt string) (string, error) {
	fields := strings.Fields(r.Command)
	if len(fields) == 0 {
		return "", ErrCLINotConfigured
	}
	if _, err := exec.LookPath(fields[0]); err != nil {
		return "", ErrCLINotInstalled
	}
	timeout := r.Timeout
	if timeout == 0 {
		timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, fields[0], fields[1:]...)
	cmd.Stdin = strings.NewReader(prompt)
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return "", ErrTimeout
	}
	if err != nil {
		return "", fmt.Errorf("llm: agent CLI failed: %w (stderr: %s)", err, strings.TrimSpace(errBuf.String()))
	}
	return out.String(), nil
}
