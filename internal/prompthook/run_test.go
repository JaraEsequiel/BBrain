package prompthook

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestDetectProjectPrefersEnv(t *testing.T) {
	t.Setenv("BBRAIN_PROJECT", "explicit")
	if got := detectProject("/home/u/whatever"); got != "explicit" {
		t.Fatalf("detectProject = %q; want explicit", got)
	}
}

func TestDetectProjectFromCwd(t *testing.T) {
	t.Setenv("BBRAIN_PROJECT", "")
	if got := detectProject("/home/u/BBrain"); got != "BBrain" {
		t.Fatalf("detectProject = %q; want BBrain", got)
	}
}

func TestRunFirstMessageForcesToolSearch(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	t.Setenv("BBRAIN_PROJECT", "P")

	var out bytes.Buffer
	Run(strings.NewReader(`{"session_id":"sess-1","cwd":"/x/P","prompt":"hi"}`), &out, t.TempDir(), time.Now())
	if !strings.Contains(out.String(), "FIRST ACTION") {
		t.Fatalf("first message must force ToolSearch; got %q", out.String())
	}

	// Second message of the same session, session still young → no injection.
	var out2 bytes.Buffer
	Run(strings.NewReader(`{"session_id":"sess-1","cwd":"/x/P","prompt":"more"}`), &out2, t.TempDir(), time.Now())
	if strings.TrimSpace(out2.String()) != "{}" {
		t.Fatalf("young session must be silent; got %q", out2.String())
	}
}

func TestRunBadJSONIsSafe(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	var out bytes.Buffer
	Run(strings.NewReader("not json"), &out, t.TempDir(), time.Now())
	if strings.TrimSpace(out.String()) == "" {
		t.Fatal("bad JSON must still emit valid JSON, not empty")
	}
	var v any
	if err := json.Unmarshal(out.Bytes(), &v); err != nil {
		t.Fatalf("Run must emit valid JSON, got %q: %v", out.String(), err)
	}
}
