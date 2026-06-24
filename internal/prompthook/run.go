package prompthook

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/JaraEsequiel/BBrain/internal/brain"
	"github.com/JaraEsequiel/BBrain/internal/index"
)

type hookInput struct {
	SessionID string `json:"session_id"`
	Cwd       string `json:"cwd"`
	Prompt    string `json:"prompt"`
}

// Run reads the UserPromptSubmit payload from r, decides whether to inject a
// systemMessage, and writes the JSON result to w. It never blocks the user's
// message: any failure degrades to "{}". home is the brain root; now is injected
// for testability.
func Run(r io.Reader, w io.Writer, home string, now time.Time) {
	out := "{}"
	defer func() { io.WriteString(w, out) }()

	data, err := io.ReadAll(r)
	if err != nil {
		return
	}
	var in hookInput
	_ = json.Unmarshal(data, &in) // bad JSON → zero values → safe defaults

	project := detectProject(in.Cwd)
	key := sessionKey(in.SessionID, project)
	toolsFile := filepath.Join(os.TempDir(), "bbrain-claude-"+key+"-tools-loaded")
	nudgeFile := filepath.Join(os.TempDir(), "bbrain-claude-"+key+"-last-nudge")

	// First message: the marker does not exist yet. Create it and force tools.
	fi, statErr := os.Stat(toolsFile)
	if statErr != nil {
		_ = os.WriteFile(toolsFile, nil, 0o644)
		out = encode(decide(DecideInput{FirstMessage: true}).Message)
		return
	}

	di := DecideInput{SessionAge: now.Sub(fi.ModTime())}

	if b, err := os.ReadFile(nudgeFile); err == nil {
		if epoch, perr := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64); perr == nil {
			di.SinceLastNudge = now.Sub(time.Unix(epoch, 0))
			di.HasLastNudge = true
		}
	}

	if ts, ok := lastSave(home, project); ok {
		if t, perr := time.Parse(time.RFC3339, ts); perr == nil {
			di.SinceLastSave = now.Sub(t)
			di.HasLastSave = true
		}
	}

	res := decide(di)
	if res.DidNudge {
		_ = os.WriteFile(nudgeFile, []byte(strconv.FormatInt(now.Unix(), 10)), 0o644)
	}
	out = encode(res.Message)
}

// detectProject mirrors mem_current_project: BBRAIN_PROJECT wins, else the cwd's
// basename.
func detectProject(cwd string) string {
	if p := os.Getenv("BBRAIN_PROJECT"); p != "" {
		return p
	}
	if cwd == "" {
		return ""
	}
	return filepath.Base(cwd)
}

// sessionKey is a filesystem-safe key: the session id if present, else a
// project+pid fallback. Non [a-zA-Z0-9_-] runes become '_'.
func sessionKey(sessionID, project string) string {
	raw := sessionID
	if raw == "" {
		raw = project + "-" + strconv.Itoa(os.Getpid())
	}
	var b strings.Builder
	for _, r := range raw {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}

// lastSave reads the project's most recent updated_at from the derived index in
// read-only spirit. Any error (db busy, column absent on a not-yet-reindexed
// brain) is treated as "unknown" so the hook stays silent.
func lastSave(home, project string) (string, bool) {
	ix, err := index.Open(brain.New(home).IndexPath())
	if err != nil {
		return "", false
	}
	defer ix.Close()
	ts, ok, err := ix.LastSavedAt(project)
	if err != nil {
		return "", false
	}
	return ts, ok
}

// encode wraps a message as the hook's JSON output. Empty message → "{}".
func encode(msg string) string {
	if msg == "" {
		return "{}"
	}
	b, err := json.Marshal(map[string]string{"systemMessage": msg})
	if err != nil {
		return "{}"
	}
	return string(b)
}
