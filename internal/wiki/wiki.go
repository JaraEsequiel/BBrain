// Package wiki builds and maintains the distilled wiki/ layer from raws/ via a
// pluggable LLM. BBrain orchestrates: it prompts the LLM, then validates and
// writes every file itself, so .md stays the source of truth.
package wiki

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/natefinch/atomic"
	"gopkg.in/yaml.v3"

	"github.com/JaraEsequiel/BBrain/internal/fact"
	"github.com/JaraEsequiel/BBrain/internal/llm"
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

// defaultBatchSize bounds how many facts go into one LLM call. Cost is
// dominated not by input size but by the agentic backend's fixed per-call
// overhead (~50s before it emits a token) plus the time to generate the output
// pages — so the lever that matters is bounding OUTPUT per call, not squeezing
// input. Measured against the real vault: 30 full facts per batch timed out at
// the runner's budget; 5 facts distilled in ~42s, 10 in ~60s. 10 keeps each
// call's output small enough to finish well inside DistillTimeout (300s) while
// still amortising the fixed overhead over a useful chunk of facts.
const defaultBatchSize = 10

// BuildOptions configures one wiki build.
type BuildOptions struct {
	WikiDir    string
	Facts      []fact.Fact // already filtered by project/scope
	Categories []string    // active category vocabulary
	Runner     llm.Runner
	Now        func() time.Time
	DryRun     bool
	BatchSize  int // facts per LLM call; 0 => defaultBatchSize
}

// BuildResult reports what a build wrote.
type BuildResult struct {
	Written  []string // slash relpaths
	LogEntry string
	DryRun   bool
}

// BuildPrompt assembles the LLM prompt: instructions, the category vocabulary,
// the expected JSON schema, the facts digest, and the existing pages (so the LLM
// can reconcile manual edits with new facts).
func BuildPrompt(facts []fact.Fact, existing []pageOnDisk, categories []string) string {
	var sb strings.Builder
	sb.WriteString("You are BBrain's wiki distiller. Read the raw facts below and produce distilled wiki pages.\n")
	sb.WriteString("Return ONLY a single JSON object: {\"pages\":[{\"slug\",\"category\",\"title\",\"sources\",\"body\",\"change_reason\"}]}.\n")
	sb.WriteString("- slug: kebab-case [a-z0-9-].\n")
	sb.WriteString("- category: one of: " + strings.Join(categories, ", ") + ".\n")
	sb.WriteString("- sources: fact ids this page distills (must be ids from the facts below).\n")
	sb.WriteString("- body: distilled markdown; cite facts as [[fact-id]].\n")
	sb.WriteString("- change_reason: short note of what changed and why.\n")
	sb.WriteString("If an existing page below should change, return it again with a reconciled body.\n\n")

	sb.WriteString("## Facts\n")
	for _, f := range facts {
		sb.WriteString("### " + f.ID + "\n")
		sb.WriteString(fmt.Sprintf("title: %s | type: %s | project: %s | scope: %s | tags: %s\n",
			f.Title, f.Type, f.Project, f.Scope, strings.Join(f.Tags, ",")))
		sb.WriteString(strings.TrimSpace(f.Body) + "\n\n")
	}

	sb.WriteString("## Existing wiki pages\n")
	if len(existing) == 0 {
		sb.WriteString("(none)\n")
	}
	for _, p := range existing {
		sb.WriteString("### " + p.RelPath + "\n")
		sb.WriteString(p.Content + "\n\n")
	}
	return sb.String()
}

// chunkFacts splits facts into consecutive slices of at most size. A nil/empty
// input yields one empty batch so a fact-less build still runs the reconcile
// pass (matching the old single-call behaviour on an empty vault).
func chunkFacts(facts []fact.Fact, size int) [][]fact.Fact {
	if len(facts) == 0 {
		return [][]fact.Fact{nil}
	}
	var out [][]fact.Fact
	for i := 0; i < len(facts); i += size {
		end := i + size
		if end > len(facts) {
			end = len(facts)
		}
		out = append(out, facts[i:end])
	}
	return out
}

// mergePage folds a later batch's page (add) into an earlier one (dst) that
// resolved to the same on-disk key. Sources: union with dedup, first-seen order.
// Body: both bodies preserved, appended in batch order with a separator. Title
// and Category keep the first batch's value; ChangeReason is joined.
func mergePage(dst *Page, add Page) {
	seen := map[string]bool{}
	for _, s := range dst.Sources {
		seen[s] = true
	}
	for _, s := range add.Sources {
		if !seen[s] {
			seen[s] = true
			dst.Sources = append(dst.Sources, s)
		}
	}
	if strings.TrimSpace(add.Body) != "" {
		dst.Body = strings.TrimRight(dst.Body, "\n") + "\n\n" + strings.TrimLeft(add.Body, "\n")
	}
	if r := strings.TrimSpace(add.ChangeReason); r != "" {
		if strings.TrimSpace(dst.ChangeReason) == "" {
			dst.ChangeReason = r
		} else {
			dst.ChangeReason += "; " + r
		}
	}
}

// Build runs one wiki build: prompt the LLM, validate every page, then write
// pages, regenerate the index, and append the log. On any validation error
// nothing is written.
func Build(ctx context.Context, opts BuildOptions) (BuildResult, error) {
	byID := map[string]fact.Fact{}
	for _, f := range opts.Facts {
		byID[f.ID] = f
	}
	existing, err := readPages(opts.WikiDir)
	if err != nil {
		return BuildResult{}, err
	}

	batchSize := opts.BatchSize
	if batchSize <= 0 {
		batchSize = defaultBatchSize
	}
	batches := chunkFacts(opts.Facts, batchSize)

	// Merge pages across batches. Two pages collide only on the same on-disk key
	// (bucket, category, slug); bucket depends on byID, built above. Order is
	// preserved by keys so writes and the log stay deterministic.
	merged := map[string]*Page{}
	var keys []string
	for i, batch := range batches {
		// existing is read once and passed to every batch so each can reconcile.
		stdout, err := opts.Runner.Run(ctx, BuildPrompt(batch, existing, opts.Categories))
		if err != nil {
			return BuildResult{}, fmt.Errorf("wiki: batch %d/%d failed: %w", i+1, len(batches), err)
		}
		pages, err := ParseResponse(stdout)
		if err != nil {
			return BuildResult{}, fmt.Errorf("wiki: batch %d/%d parse failed: %w", i+1, len(batches), err)
		}
		for _, p := range pages {
			p := p
			key := DeriveBucket(p.Sources, byID) + "\x00" + p.Category + "\x00" + p.Slug
			if cur, ok := merged[key]; ok {
				mergePage(cur, p)
				continue
			}
			merged[key] = &p
			keys = append(keys, key)
		}
	}
	pages := make([]Page, 0, len(keys))
	for _, k := range keys {
		pages = append(pages, *merged[k])
	}
	valid := map[string]bool{}
	for _, c := range opts.Categories {
		valid[c] = true
	}
	for _, p := range pages {
		if err := ValidatePage(p, valid, byID); err != nil {
			return BuildResult{}, err
		}
	}

	now := opts.Now().UTC().Format(time.RFC3339)
	var written []string
	var logb strings.Builder
	logb.WriteString("\n## " + now + " — wiki build\n")
	for _, p := range pages {
		bucket := DeriveBucket(p.Sources, byID)
		rel := path.Join(bucket, p.Category, p.Slug+".md")
		written = append(written, rel)
		reason := strings.TrimSpace(p.ChangeReason)
		if reason == "" {
			reason = "updated"
		}
		noun := "sources"
		if len(p.Sources) == 1 {
			noun = "source"
		}
		logb.WriteString(fmt.Sprintf("- wrote %s (%d %s): %s\n", rel, len(p.Sources), noun, reason))
	}
	res := BuildResult{Written: written, LogEntry: logb.String(), DryRun: opts.DryRun}
	if opts.DryRun {
		return res, nil
	}

	for _, p := range pages {
		bucket := DeriveBucket(p.Sources, byID)
		dst := PagePath(opts.WikiDir, bucket, p.Category, p.Slug)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return BuildResult{}, err
		}
		if err := atomic.WriteFile(dst, strings.NewReader(RenderPage(p, now))); err != nil {
			return BuildResult{}, err
		}
	}
	if err := RegenerateIndex(opts.WikiDir); err != nil {
		return BuildResult{}, err
	}
	if err := AppendLog(opts.WikiDir, res.LogEntry); err != nil {
		return BuildResult{}, err
	}
	return res, nil
}
