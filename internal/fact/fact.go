// Package fact defines the on-disk Markdown memory format and its (de)serialization.
// The .md file is the source of truth; this package is the only place that knows
// how a Fact is laid out as frontmatter + an H1 title + a body.
package fact

import (
	"fmt"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// Link is a reasoned, typed wikilink between two facts.
type Link struct {
	Target   string `yaml:"target"`
	Relation string `yaml:"relation"`
	Why      string `yaml:"why"`
}

// Fact is one memory. Frontmatter fields carry the structured metadata; Title and
// Body are derived from the markdown content below the frontmatter and are never
// serialized into YAML (yaml:"-").
type Fact struct {
	ID            string   `yaml:"id"`
	Type          string   `yaml:"type"`
	Scope         string   `yaml:"scope"`
	Project       string   `yaml:"project"`
	TopicKey      string   `yaml:"topic_key,omitempty"`
	Tags          []string `yaml:"tags,omitempty"`
	Links         []Link   `yaml:"links,omitempty"`
	Pinned        bool     `yaml:"pinned,omitempty"`
	CreatedAt     string   `yaml:"created_at"`
	UpdatedAt     string   `yaml:"updated_at"`
	RevisionCount int      `yaml:"revision_count"`

	Title string `yaml:"-"`
	Body  string `yaml:"-"`
}

const delim = "---"

// Marshal renders a Fact as: frontmatter, then "# Title", then the body.
func Marshal(f Fact) string {
	fm, _ := yaml.Marshal(f) // Fact has no unmarshalable fields
	var sb strings.Builder
	sb.WriteString(delim + "\n")
	sb.Write(fm)
	sb.WriteString(delim + "\n\n")
	sb.WriteString("# " + f.Title + "\n\n")
	sb.WriteString(strings.TrimRight(f.Body, "\n"))
	sb.WriteString("\n")
	return sb.String()
}

// Parse is the inverse of Marshal. It requires leading frontmatter delimited by
// "---" lines, then reads the first "# " line as the title and the remainder as
// the body.
func Parse(s string) (Fact, error) {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	if !strings.HasPrefix(s, delim+"\n") {
		return Fact{}, fmt.Errorf("fact: missing opening frontmatter delimiter")
	}
	rest := s[len(delim)+1:]
	end := strings.Index(rest, "\n"+delim+"\n")
	if end < 0 {
		return Fact{}, fmt.Errorf("fact: missing closing frontmatter delimiter")
	}
	fmText := rest[:end]
	body := rest[end+len("\n"+delim+"\n"):]

	var f Fact
	if err := yaml.Unmarshal([]byte(fmText), &f); err != nil {
		return Fact{}, fmt.Errorf("fact: bad frontmatter yaml: %w", err)
	}

	body = strings.TrimLeft(body, "\n")
	if strings.HasPrefix(body, "# ") {
		nl := strings.IndexByte(body, '\n')
		if nl < 0 {
			f.Title = strings.TrimSpace(body[2:])
			f.Body = ""
			return f, nil
		}
		f.Title = strings.TrimSpace(body[2:nl])
		body = body[nl+1:]
	}
	f.Body = strings.Trim(body, "\n")
	return f, nil
}

// Slug converts a title into a filesystem-safe kebab-case slug.
func Slug(title string) string {
	var sb strings.Builder
	prevDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(title)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			sb.WriteRune(r)
			prevDash = false
		default:
			if !prevDash && sb.Len() > 0 {
				sb.WriteByte('-')
				prevDash = true
			}
		}
	}
	out := strings.Trim(sb.String(), "-")
	if out == "" {
		return "untitled"
	}
	return out
}

// NewID builds a stable id of the form "<date>-<slug>".
func NewID(date, title string) string {
	return date + "-" + Slug(title)
}

// Relations is the controlled vocabulary for reasoned wikilink relations,
// ported from engram. A link's relation must be one of these.
var Relations = []string{"relates", "depends-on", "conflicts-with", "supersedes", "scoped", "compatible"}

var idRE = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// ValidID reports whether id is a safe fact identifier: lowercase-alphanumeric
// segments joined by single hyphens (the shape NewID produces). It rejects path
// separators, "..", whitespace, and uppercase, so an id from untrusted input
// (e.g. an MCP tool argument) can never escape the facts directory.
func ValidID(id string) bool { return idRE.MatchString(id) }

// ValidRelation reports whether r is one of the allowed relation types.
func ValidRelation(r string) bool {
	for _, x := range Relations {
		if x == r {
			return true
		}
	}
	return false
}

// FormatTarget wraps a fact id as an on-disk wikilink target ("[[id]]").
func FormatTarget(id string) string { return "[[" + id + "]]" }

// LinkTargetID extracts the bare fact id from a wikilink target, stripping the
// surrounding [[ ]] and any whitespace. A target that is already a bare slug is
// returned unchanged.
func LinkTargetID(target string) string {
	t := strings.TrimSpace(target)
	t = strings.TrimPrefix(t, "[[")
	t = strings.TrimSuffix(t, "]]")
	return strings.TrimSpace(t)
}
