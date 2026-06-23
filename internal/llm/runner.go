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
