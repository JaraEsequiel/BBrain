// Package brain locates and initializes a BBrain "brain" on disk: a single
// directory that holds raws/ (source-of-truth memories) and wiki/ (distilled),
// plus a derived index under .bbrain/.
package brain

import (
	"os"
	"path/filepath"
)

// Brain is a brain rooted at a directory.
type Brain struct {
	Root string
}

// New returns a Brain rooted at root.
func New(root string) Brain { return Brain{Root: root} }

func (b Brain) FactsDir() string    { return filepath.Join(b.Root, "raws", "facts") }
func (b Brain) UserRawsDir() string { return filepath.Join(b.Root, "raws", "user-raws") }
func (b Brain) WikiDir() string     { return filepath.Join(b.Root, "wiki") }
func (b Brain) IndexPath() string   { return filepath.Join(b.Root, ".bbrain", "index.db") }

// Init creates the brain structure. It is idempotent: directories are created
// with MkdirAll, and seed files are written only when they do not already exist,
// so re-running never clobbers user edits.
func (b Brain) Init() error {
	dirs := []string{
		b.FactsDir(),
		b.UserRawsDir(),
		b.WikiDir(),
		filepath.Join(b.Root, ".bbrain"),
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}

	seeds := map[string]string{
		filepath.Join(b.Root, "CLAUDE.md"):        claudeSchema,
		filepath.Join(b.WikiDir(), "index.md"):    "# Wiki Index\n\n_Pages will be cataloged here._\n",
		filepath.Join(b.WikiDir(), "log.md"):      "# Wiki Log\n\n_Append-only record of ingests, queries, and lints._\n",
	}
	for path, content := range seeds {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
	}
	return nil
}

const claudeSchema = `# BBrain Brain — Schema

This directory is a **BBrain brain**: cross-session, cross-project memory.

## Structure
- ` + "`raws/facts/`" + ` — atomic memories as ` + "`.md`" + ` (source of truth). Flat layout;
  metadata lives in YAML frontmatter (type, scope, project, topic_key, tags, links).
- ` + "`raws/user-raws/`" + ` — your own raw notes.
- ` + "`wiki/`" + ` — distilled pages maintained by an LLM routine; ` + "`index.md`" + ` catalogs
  pages, ` + "`log.md`" + ` records ingests/queries/lints.
- ` + "`.bbrain/`" + ` — derived FTS index. Disposable; rebuild with ` + "`bbrain reindex`" + `.

## Conventions
- A fact's frontmatter ` + "`links:`" + ` are reasoned wikilinks: each has ` + "`target`" + `,
  ` + "`relation`" + ` (relates|depends-on|conflicts-with|supersedes|scoped|compatible) and a
  required ` + "`why`" + `.
- ` + "`topic_key`" + ` (family/description) makes a save an upsert: it rewrites the same file.

This file is only for opening a cowork directly inside this folder. Agents use
BBrain through its MCP tools, not this file.
`
