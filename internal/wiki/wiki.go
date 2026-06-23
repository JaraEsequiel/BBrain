// Package wiki builds and maintains the distilled wiki/ layer from raws/ via a
// pluggable LLM. BBrain orchestrates: it prompts the LLM, then validates and
// writes every file itself, so .md stays the source of truth.
package wiki

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/natefinch/atomic"
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

// reservedPages are the top-level wiki files that are not distilled pages.
var reservedPages = map[string]bool{"index.md": true, "log.md": true}

// pageOnDisk is a discovered wiki page file.
type pageOnDisk struct {
	RelPath string // slash path relative to wikiDir, e.g. "projects/shopapp/decisions/auth.md"
	Content string
}

// readPages walks wikiDir and returns every distilled page (excluding index.md
// and log.md) in lexical order. A missing wikiDir yields (nil, nil).
func readPages(wikiDir string) ([]pageOnDisk, error) {
	var out []pageOnDisk
	err := filepath.WalkDir(wikiDir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".md") {
			return nil
		}
		rel, _ := filepath.Rel(wikiDir, p)
		rel = filepath.ToSlash(rel)
		if reservedPages[rel] {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		out = append(out, pageOnDisk{RelPath: rel, Content: string(b)})
		return nil
	})
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return out, nil
}

// RegenerateIndex rewrites wiki/index.md as a catalog of every page, grouped by
// bucket. It is derived: BBrain reconstructs it by scanning wiki/.
func RegenerateIndex(wikiDir string) error {
	pages, err := readPages(wikiDir)
	if err != nil {
		return err
	}
	byBucket := map[string][]string{}
	var buckets []string
	lines := map[string]string{}
	for _, pg := range pages {
		meta, err := ParsePageMeta(pg.Content)
		if err != nil {
			return fmt.Errorf("index: %s: %w", pg.RelPath, err)
		}
		dir := path.Dir(pg.RelPath)    // projects/shopapp/decisions  | global/people
		category := path.Base(dir)     // decisions                   | people
		bucket := path.Dir(dir)        // projects/shopapp            | global
		if _, seen := byBucket[bucket]; !seen {
			buckets = append(buckets, bucket)
		}
		noun := "sources"
		if len(meta.Sources) == 1 {
			noun = "source"
		}
		lines[pg.RelPath] = fmt.Sprintf("- [%s](%s) — %s — %d %s", meta.Title, pg.RelPath, category, len(meta.Sources), noun)
		byBucket[bucket] = append(byBucket[bucket], pg.RelPath)
	}
	sort.Strings(buckets)

	var sb strings.Builder
	sb.WriteString("# Wiki Index\n")
	sb.WriteString("<!-- Generated by `bbrain wiki build` — do not edit by hand; regenerated each build. -->\n")
	for _, b := range buckets {
		sb.WriteString("\n## " + b + "\n")
		keys := byBucket[b]
		sort.Strings(keys)
		for _, k := range keys {
			sb.WriteString(lines[k] + "\n")
		}
	}
	return atomic.WriteFile(filepath.Join(wikiDir, "index.md"), strings.NewReader(sb.String()))
}

// AppendLog appends entry (a full block) to wiki/log.md, creating it if needed.
func AppendLog(wikiDir, entry string) error {
	f, err := os.OpenFile(filepath.Join(wikiDir, "log.md"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(entry)
	return err
}
