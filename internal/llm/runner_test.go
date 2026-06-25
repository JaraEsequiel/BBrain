package llm

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeScript(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "agent.sh")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestCLIRunnerPassesStdinReturnsStdout(t *testing.T) {
	// The script prefixes "GOT:" then echoes stdin, proving the prompt was piped
	// in and stdout was captured.
	r := &CLIRunner{Command: writeScript(t, `printf 'GOT:'; cat`)}
	out, err := r.Run(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out != "GOT:hello" {
		t.Fatalf("out = %q, want GOT:hello", out)
	}
}

func TestCLIRunnerNotConfigured(t *testing.T) {
	r := &CLIRunner{Command: ""}
	if _, err := r.Run(context.Background(), "x"); err != ErrCLINotConfigured {
		t.Fatalf("err = %v, want ErrCLINotConfigured", err)
	}
}

func TestCLIRunnerNotInstalled(t *testing.T) {
	r := &CLIRunner{Command: "bbrain-no-such-binary-xyz"}
	if _, err := r.Run(context.Background(), "x"); err != ErrCLINotInstalled {
		t.Fatalf("err = %v, want ErrCLINotInstalled", err)
	}
}

func TestCLIRunnerTimeout(t *testing.T) {
	r := &CLIRunner{Command: writeScript(t, `sleep 5`), Timeout: 50 * time.Millisecond}
	if _, err := r.Run(context.Background(), "x"); err != ErrTimeout {
		t.Fatalf("err = %v, want ErrTimeout", err)
	}
}

func TestNewCLIRunnerReadsEnv(t *testing.T) {
	t.Setenv("BBRAIN_AGENT_CLI", "claude -p")
	r := NewCLIRunner()
	if r.Command != "claude -p" || r.Timeout != DefaultTimeout {
		t.Fatalf("runner = %+v", r)
	}
}

// writeEnvFile writes a setup-style <home>/.bbrain/env.sh and returns home.
func writeEnvFile(t *testing.T, line string) string {
	t.Helper()
	home := t.TempDir()
	dir := filepath.Join(home, ".bbrain")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "env.sh"), []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return home
}

func TestAgentCLIPrefersEnv(t *testing.T) {
	t.Setenv("BBRAIN_AGENT_CLI", "claude -p")
	home := writeEnvFile(t, `export BBRAIN_AGENT_CLI='/should/not/win'`)
	if got := AgentCLI(home); got != "claude -p" {
		t.Fatalf("AgentCLI = %q, want env value to win", got)
	}
}

func TestAgentCLIFallsBackToEnvFile(t *testing.T) {
	t.Setenv("BBRAIN_AGENT_CLI", "") // simulate MCP server started without a sourced profile
	home := writeEnvFile(t, `export BBRAIN_AGENT_CLI='/home/x/.bbrain/agents/claude-code.sh'`)
	if got := AgentCLI(home); got != "/home/x/.bbrain/agents/claude-code.sh" {
		t.Fatalf("AgentCLI = %q, want value parsed from env.sh", got)
	}
}

func TestAgentCLIEmptyWhenNeither(t *testing.T) {
	t.Setenv("BBRAIN_AGENT_CLI", "")
	if got := AgentCLI(t.TempDir()); got != "" {
		t.Fatalf("AgentCLI = %q, want empty", got)
	}
}
