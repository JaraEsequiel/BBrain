// Package wiki builds and maintains the distilled wiki/ layer from raws/ via a
// pluggable LLM. BBrain orchestrates: it prompts the LLM, then validates and
// writes every file itself, so .md stays the source of truth.
package wiki

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	"bbrain/internal/fact"
)

// Page is one wiki page produced by the LLM.
type Page struct {
	Slug         string   `json:"slug"`
	Category     string   `json:"category"`
	Title        string   `json:"title"`
	Sources      []string `json:"sources"`
	Body         string   `json:"body"`
	ChangeReason string   `json:"change_reason"`
}

type response struct {
	Pages []Page `json:"pages"`
}

// PageMeta is the frontmatter of a wiki page on disk.
type PageMeta struct {
	Title       string   `yaml:"title"`
	Category    string   `yaml:"category"`
	Sources     []string `yaml:"sources"`
	GeneratedAt string   `yaml:"generated_at"`
}

// DefaultCategories is the controlled (extensible) category vocabulary.
var DefaultCategories = []string{"decisions", "concepts", "comparisons", "people", "preferences", "entities"}

// ErrInvalidJSON is returned when the LLM stdout is not the expected JSON object.
var ErrInvalidJSON = errors.New("wiki: LLM returned malformed JSON")

const delim = "---"

var slugRE = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

// ParseResponse parses the LLM stdout into the list of pages.
func ParseResponse(stdout string) ([]Page, error) {
	var r response
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &r); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidJSON, err)
	}
	return r.Pages, nil
}

// RenderPage renders a Page to its on-disk markdown: frontmatter then body.
func RenderPage(p Page, generatedAt string) string {
	meta := PageMeta{Title: p.Title, Category: p.Category, Sources: p.Sources, GeneratedAt: generatedAt}
	fm, _ := yaml.Marshal(meta)
	var sb strings.Builder
	sb.WriteString(delim + "\n")
	sb.Write(fm)
	sb.WriteString(delim + "\n\n")
	sb.WriteString(strings.TrimRight(p.Body, "\n"))
	sb.WriteString("\n")
	return sb.String()
}

// ParsePageMeta reads the frontmatter of a wiki page file's content.
func ParsePageMeta(content string) (PageMeta, error) {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	if !strings.HasPrefix(content, delim+"\n") {
		return PageMeta{}, fmt.Errorf("wiki: page missing opening frontmatter delimiter")
	}
	rest := content[len(delim)+1:]
	end := strings.Index(rest, "\n"+delim+"\n")
	if end < 0 {
		return PageMeta{}, fmt.Errorf("wiki: page missing closing frontmatter delimiter")
	}
	var m PageMeta
	if err := yaml.Unmarshal([]byte(rest[:end]), &m); err != nil {
		return PageMeta{}, fmt.Errorf("wiki: bad page frontmatter: %w", err)
	}
	return m, nil
}

// DeriveBucket computes a page's bucket from its sources: all sources scope
// "project" within a single project -> "projects/<project>"; otherwise
// (global/personal sources, or sources spanning projects) -> "global". The
// project segment is run through fact.Slug so it is always a safe path segment.
// All sources are assumed to exist in byID (ValidatePage enforces that).
func DeriveBucket(sources []string, byID map[string]fact.Fact) string {
	projects := map[string]bool{}
	for _, id := range sources {
		f := byID[id]
		if f.Scope == "project" && f.Project != "" {
			projects[fact.Slug(f.Project)] = true
		} else {
			return "global"
		}
	}
	if len(projects) == 1 {
		for p := range projects {
			return "projects/" + p
		}
	}
	return "global"
}

// PagePath is the absolute path of a page under wikiDir.
func PagePath(wikiDir, bucket, category, slug string) string {
	return filepath.Join(wikiDir, filepath.FromSlash(bucket), category, slug+".md")
}

// ValidatePage rejects anything unsafe or dangling before BBrain writes the file.
func ValidatePage(p Page, validCategories map[string]bool, byID map[string]fact.Fact) error {
	if !slugRE.MatchString(p.Slug) {
		return fmt.Errorf("wiki: invalid slug %q (want [a-z0-9-])", p.Slug)
	}
	if !validCategories[p.Category] {
		return fmt.Errorf("wiki: unknown category %q", p.Category)
	}
	if strings.TrimSpace(p.Title) == "" {
		return fmt.Errorf("wiki: page %q has empty title", p.Slug)
	}
	if strings.TrimSpace(p.Body) == "" {
		return fmt.Errorf("wiki: page %q has empty body", p.Slug)
	}
	if len(p.Sources) == 0 {
		return fmt.Errorf("wiki: page %q has no sources", p.Slug)
	}
	for _, id := range p.Sources {
		if _, ok := byID[id]; !ok {
			return fmt.Errorf("wiki: page %q cites unknown fact %q", p.Slug, id)
		}
	}
	return nil
}
