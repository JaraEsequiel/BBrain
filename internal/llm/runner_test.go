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
