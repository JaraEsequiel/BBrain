package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/JaraEsequiel/BBrain/internal/app"
	"github.com/JaraEsequiel/BBrain/internal/install"
	"github.com/JaraEsequiel/BBrain/internal/mcp"
	"github.com/JaraEsequiel/BBrain/internal/prompthook"
	"github.com/JaraEsequiel/BBrain/internal/store"
	"github.com/JaraEsequiel/BBrain/internal/watch"
	"github.com/mattn/go-isatty"
)

// version is the build version. Overridden at release time via
// -ldflags "-X main.version=<tag>"; "dev" for local builds.
var version = "dev"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// brainRoot resolves where the brain lives: $BBRAIN_HOME or ~/.bbrain/default.
func brainRoot() string {
	if v := os.Getenv("BBRAIN_HOME"); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".bbrain"
	}
	return home + "/.bbrain/default"
}

// run is the CLI entrypoint used by main(); it reads from os.Stdin.
func run(args []string, stdout, stderr io.Writer) int {
	return runWithIn(args, os.Stdin, stdout, stderr)
}

func runWithIn(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: bbrain <version|init|save|search|reindex|link|why|related|candidates|wiki|mem|install|uninstall|context|prompt-submit|watch|vault|mcp> [args]")
		return 2
	}
	switch args[0] {
	case "version":
		fmt.Fprintln(stdout, "bbrain "+version)
		return 0
	case "init":
		a := app.New(brainRoot())
		if err := a.Init(); err != nil {
			fmt.Fprintf(stderr, "init: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "initialized brain at %s\n", a.Brain.Root)
		return 0
	case "reindex":
		a := app.New(brainRoot())
		n, err := a.Reindex()
		if err != nil {
			fmt.Fprintf(stderr, "reindex: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "reindexed %d facts\n", n)
		return 0
	case "save":
		return cmdSave(args[1:], stdout, stderr)
	case "search":
		return cmdSearch(args[1:], stdout, stderr)
	case "link":
		return cmdLink(args[1:], stdout, stderr)
	case "why":
		return cmdWhy(args[1:], stdout, stderr)
	case "related":
		return cmdRelated(args[1:], stdout, stderr)
	case "candidates":
		return cmdCandidates(args[1:], stdout, stderr)
	case "wiki":
		return cmdWiki(args[1:], stdout, stderr)
	case "mem":
		return cmdMem(args[1:], stdout, stderr)
	case "install":
		return cmdInstall(args[1:], stdin, stdout, stderr)
	case "uninstall":
		return cmdUninstall(args[1:], stdout, stderr)
	case "watch":
		return cmdWatch(args[1:], stdout, stderr)
	case "vault":
		return cmdVault(args[1:], stdout, stderr)
	case "mcp":
		return cmdMCP(args[1:], stdin, stdout, stderr)
	case "context":
		return cmdContext(args[1:], stdout, stderr)
	case "prompt-submit":
		return cmdPromptSubmit(args[1:], stdin, stdout)
	default:
		fmt.Fprintf(stderr, "unknown command: %s\n", args[0])
		return 2
	}
}

func cmdSave(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("save", flag.ContinueOnError)
	fs.SetOutput(stderr)
	typ := fs.String("type", "discovery", "fact type")
	title := fs.String("title", "", "fact title (required)")
	body := fs.String("body", "", "fact body")
	project := fs.String("project", "", "project (required)")
	scope := fs.String("scope", "project", "scope")
	topic := fs.String("topic-key", "", "optional topic key for upsert")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *title == "" || *project == "" {
		fmt.Fprintln(stderr, "save: --title and --project are required")
		return 2
	}
	a := app.New(brainRoot())
	f, err := a.Save(store.SaveInput{
		Type: *typ, Title: *title, Body: *body,
		Project: *project, Scope: *scope, TopicKey: *topic,
	})
	if err != nil {
		fmt.Fprintf(stderr, "save: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "saved %s\n", f.ID)
	return 0
}

func cmdSearch(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("search", flag.ContinueOnError)
	fs.SetOutput(stderr)
	limit := fs.Int("limit", 20, "max results")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	query := ""
	for i, a := range fs.Args() {
		if i > 0 {
			query += " "
		}
		query += a
	}
	if query == "" {
		fmt.Fprintln(stderr, "search: provide a query")
		return 2
	}
	a := app.New(brainRoot())
	res, stale, err := a.Search(query, *limit)
	if err != nil {
		fmt.Fprintf(stderr, "search: %v\n", err)
		return 1
	}
	if stale {
		fmt.Fprintln(stderr, "warning: search index predates a schema change and hasn't been reindexed (run `bbrain reindex`) — results may be incomplete")
	}
	for _, r := range res {
		fmt.Fprintf(stdout, "%s\t%s\t%s\n", r.FactID, r.Type, r.Title)
	}
	return 0
}

func cmdLink(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("link", flag.ContinueOnError)
	fs.SetOutput(stderr)
	from := fs.String("from", "", "source fact id (required)")
	to := fs.String("to", "", "target fact id (required)")
	rel := fs.String("relation", "relates", "relation type (relates|depends-on|conflicts-with|supersedes|scoped|compatible)")
	why := fs.String("why", "", "why the two facts are related (required)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *from == "" || *to == "" || *why == "" {
		fmt.Fprintln(stderr, "link: --from, --to and --why are required")
		return 2
	}
	a := app.New(brainRoot())
	f, err := a.Link(*from, *to, *rel, *why)
	if err != nil {
		fmt.Fprintf(stderr, "link: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "linked %s -[%s]-> %s\n", f.ID, *rel, *to)
	return 0
}

func cmdWhy(args []string, stdout, stderr io.Writer) int {
	if len(args) != 2 {
		fmt.Fprintln(stderr, "why: usage: bbrain why <factA> <factB>")
		return 2
	}
	a := app.New(brainRoot())
	edges, err := a.Why(args[0], args[1])
	if err != nil {
		fmt.Fprintf(stderr, "why: %v\n", err)
		return 1
	}
	if len(edges) == 0 {
		fmt.Fprintf(stdout, "no direct link between %s and %s\n", args[0], args[1])
		return 0
	}
	for _, e := range edges {
		fmt.Fprintf(stdout, "%s -[%s]-> %s: %s\n", e.SrcID, e.Relation, e.DstID, e.Why)
	}
	return 0
}

func cmdRelated(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "related: usage: bbrain related <factID>")
		return 2
	}
	a := app.New(brainRoot())
	ns, err := a.Related(args[0])
	if err != nil {
		fmt.Fprintf(stderr, "related: %v\n", err)
		return 1
	}
	for _, n := range ns {
		arrow := "->"
		if n.Direction == "in" {
			arrow = "<-"
		}
		fmt.Fprintf(stdout, "%s %s [%s] %s\n", arrow, n.FactID, n.Relation, n.Why)
	}
	return 0
}

func cmdCandidates(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("candidates", flag.ContinueOnError)
	fs.SetOutput(stderr)
	limit := fs.Int("limit", 10, "max candidates")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "candidates: usage: bbrain candidates [--limit N] <factID>")
		return 2
	}
	a := app.New(brainRoot())
	res, err := a.Candidates(fs.Arg(0), *limit)
	if err != nil {
		fmt.Fprintf(stderr, "candidates: %v\n", err)
		return 1
	}
	for _, r := range res {
		fmt.Fprintf(stdout, "%s\t%s\t%s\n", r.FactID, r.Type, r.Title)
	}
	return 0
}

func cmdWiki(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "wiki: usage: bbrain wiki <build|link|lint> [args]")
		return 2
	}
	switch args[0] {
	case "build":
		return cmdWikiBuild(args[1:], stdout, stderr)
	case "link":
		return cmdWikiLink(args[1:], stdout, stderr)
	case "lint":
		return cmdWikiLint(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "wiki: unknown subcommand %q\n", args[0])
		return 2
	}
}

func cmdWikiBuild(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("wiki build", flag.ContinueOnError)
	fs.SetOutput(stderr)
	project := fs.String("project", "", "only distill facts in this project")
	scope := fs.String("scope", "", "only distill facts in this scope")
	cats := fs.String("categories", "", "extra wiki categories (comma-separated)")
	dryRun := fs.Bool("dry-run", false, "print what would be written without writing")
	home := fs.String("home", "", "brain home (default: resolved brain root)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	var extra []string
	if *cats != "" {
		for _, c := range strings.Split(*cats, ",") {
			if c = strings.TrimSpace(c); c != "" {
				extra = append(extra, c)
			}
		}
	}
	root := *home
	if root == "" {
		root = brainRoot()
	}
	a := app.New(root)
	// Loud signal for the classic misconfiguration: a wrong/unset home resolves to
	// a path with no brain, and the build then runs against an empty vault, exiting 0
	// silently (BBRAIN_HOME unset -> brainRoot() -> ~/.bbrain/default).
	if _, err := os.Stat(a.Brain.FactsDir()); os.IsNotExist(err) {
		fmt.Fprintf(stderr, "wiki build: warning: no brain at %q (facts dir missing); nothing will be distilled until --home/BBRAIN_HOME points at a real brain or `bbrain init` runs there\n", root)
	}
	res, err := a.WikiBuild(context.Background(), app.WikiBuildOptions{
		Project: *project, Scope: *scope, Categories: extra, DryRun: *dryRun,
	})
	if err != nil {
		fmt.Fprintf(stderr, "wiki build: %v\n", err)
		return 1
	}
	if res.DryRun {
		fmt.Fprintln(stdout, "[dry-run] would write:")
	}
	for _, w := range res.Written {
		fmt.Fprintln(stdout, w)
	}
	// Graceful degradation: Build skips batches that exhaust their retries and drops
	// pages that fail validation (the LLM hallucinated) instead of aborting. Exit-code
	// contract: success (0) if at least one page was written even when some batches or
	// pages were skipped — partial progress is real and the dropped facts/pages are
	// recovered on the next run. Failure (1) only if something was skipped/invalid AND
	// nothing was written, i.e. the build produced nothing useful.
	if len(res.Skipped) > 0 {
		var facts int
		for _, s := range res.Skipped {
			facts += len(s.FactIDs)
		}
		fmt.Fprintf(stderr, "wiki build: skipped %d batch(es), %d facts not distilled this run — re-run to retry\n",
			len(res.Skipped), facts)
		for _, s := range res.Skipped {
			fmt.Fprintf(stderr, "  batch %d (%d facts): %s\n", s.Index, len(s.FactIDs), s.Err)
		}
	}
	if len(res.InvalidPages) > 0 {
		fmt.Fprintf(stderr, "wiki build: dropped %d invalid page(s) — re-run to retry\n", len(res.InvalidPages))
		for _, ip := range res.InvalidPages {
			fmt.Fprintf(stderr, "  page %s (%s): %s\n", ip.Slug, ip.Category, ip.Err)
		}
	}
	if (len(res.Skipped) > 0 || len(res.InvalidPages) > 0) && len(res.Written) == 0 {
		return 1
	}
	return 0
}

func cmdWikiLink(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("wiki link", flag.ContinueOnError)
	fs.SetOutput(stderr)
	project := fs.String("project", "", "only link facts in this project")
	scope := fs.String("scope", "", "only link facts in this scope")
	limit := fs.Int("limit", 8, "max FTS candidates considered per fact")
	dryRun := fs.Bool("dry-run", false, "print proposed links without writing")
	home := fs.String("home", "", "brain home (default: resolved brain root)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	root := *home
	if root == "" {
		root = brainRoot()
	}
	a := app.New(root)
	// Loud signal for the classic misconfiguration: a wrong/unset home resolves to
	// a path with no brain, and linking then runs against an empty vault, exiting 0
	// silently (BBRAIN_HOME unset -> brainRoot() -> ~/.bbrain/default).
	if _, err := os.Stat(a.Brain.FactsDir()); os.IsNotExist(err) {
		fmt.Fprintf(stderr, "wiki link: warning: no brain at %q (facts dir missing); nothing will be linked until --home/BBRAIN_HOME points at a real brain or `bbrain init` runs there\n", root)
	}
	res, err := a.WikiLink(context.Background(), app.WikiLinkOptions{
		Project: *project, Scope: *scope, Limit: *limit, DryRun: *dryRun,
	})
	if err != nil {
		fmt.Fprintf(stderr, "wiki link: %v\n", err)
		return 1
	}
	if res.DryRun {
		fmt.Fprintln(stdout, "[dry-run] would write:")
	}
	for _, e := range res.Written {
		fmt.Fprintf(stdout, "%s -[%s]-> %s: %s\n", e.Src, e.Relation, e.Dst, e.Why)
	}
	if res.Skipped > 0 {
		fmt.Fprintf(stdout, "(skipped %d already-linked)\n", res.Skipped)
	}
	// Graceful degradation: WikiLink drops a fact whose LLM linking exhausts its
	// retries instead of aborting. Same exit-code contract as cmdWikiBuild: success
	// (0) if at least one link was written even when some facts were dropped —
	// partial progress is real and the dropped facts are recovered next run; also 0
	// when there was simply nothing to link. Failure (1) only if facts were dropped
	// AND nothing was written, i.e. the run produced nothing useful.
	if len(res.Failed) > 0 {
		fmt.Fprintf(stderr, "wiki link: dropped %d fact(s) after exhausting retries — re-run to retry\n", len(res.Failed))
		for _, f := range res.Failed {
			fmt.Fprintf(stderr, "  fact %s: %s\n", f.FactID, f.Err)
		}
	}
	if len(res.Failed) > 0 && len(res.Written) == 0 {
		return 1
	}
	return 0
}

func cmdWikiLint(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("wiki lint", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cats := fs.String("categories", "", "extra wiki categories (comma-separated)")
	fix := fs.Bool("fix", false, "apply mechanically-safe repairs")
	home := fs.String("home", "", "brain home (default: resolved brain root)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	var extra []string
	if *cats != "" {
		for _, c := range strings.Split(*cats, ",") {
			if c = strings.TrimSpace(c); c != "" {
				extra = append(extra, c)
			}
		}
	}
	root := *home
	if root == "" {
		root = brainRoot()
	}
	a := app.New(root)
	// Loud signal for the classic misconfiguration: a wrong/unset home resolves to
	// a path with no brain, and lint then runs against an empty vault, exiting 0
	// silently (BBRAIN_HOME unset -> brainRoot() -> ~/.bbrain/default).
	if _, err := os.Stat(a.Brain.FactsDir()); os.IsNotExist(err) {
		fmt.Fprintf(stderr, "wiki lint: warning: no brain at %q (facts dir missing); nothing will be linted until --home/BBRAIN_HOME points at a real brain or `bbrain init` runs there\n", root)
	}
	res, err := a.WikiLint(app.WikiLintOptions{Categories: extra, Fix: *fix})
	if err != nil {
		fmt.Fprintf(stderr, "wiki lint: %v\n", err)
		return 1
	}
	for _, is := range res.Fixed {
		fmt.Fprintf(stdout, "fixed: %s — %s\n", is.Kind, is.Message)
	}
	failures := 0
	for _, is := range res.Issues {
		if is.Info {
			fmt.Fprintf(stdout, "info: %s: %s — %s\n", is.Kind, is.Location, is.Message)
			continue
		}
		failures++
		fmt.Fprintf(stdout, "%s: %s — %s\n", is.Kind, is.Location, is.Message)
	}
	if failures > 0 {
		return 1
	}
	return 0
}

// stringSlice implements flag.Value for a repeatable string flag (e.g. --type
// used more than once), which the stdlib flag package does not provide.
type stringSlice []string

func (s *stringSlice) String() string     { return strings.Join(*s, ",") }
func (s *stringSlice) Set(v string) error { *s = append(*s, v); return nil }

// parseDur parses a duration for --older-than: an "Nd" suffix means N days
// (time.ParseDuration has no day unit); anything else falls through to
// time.ParseDuration ("720h", "30m", ...).
func parseDur(s string) (time.Duration, error) {
	if strings.HasSuffix(s, "d") {
		n, err := strconv.Atoi(strings.TrimSuffix(s, "d"))
		if err != nil {
			return 0, fmt.Errorf("invalid duration %q: %v", s, err)
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	return time.ParseDuration(s)
}

func cmdMem(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "mem: usage: bbrain mem <archive|unarchive> [args]")
		return 2
	}
	switch args[0] {
	case "archive":
		return cmdMemArchive(args[1:], stdout, stderr)
	case "unarchive":
		return cmdMemUnarchive(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "mem: unknown subcommand %q\n", args[0])
		return 2
	}
}

func cmdMemArchive(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("mem archive", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var types stringSlice
	fs.Var(&types, "type", "fact type to select (repeatable)")
	olderThan := fs.String("older-than", "", "select facts not updated in this long (e.g. 30d, 720h)")
	distilled := fs.Bool("distilled", false, "select only facts cited by >=1 wiki page")
	project := fs.String("project", "", "select only facts in this project")
	apply := fs.Bool("apply", false, "archive the candidates (default: dry-run, prints the plan)")
	home := fs.String("home", "", "brain home (default: resolved brain root)")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: bbrain mem archive [--type T]... [--older-than DUR] [--distilled] [--project P] [--apply] [--home DIR] [id...]")
		fmt.Fprintln(stderr, "  recommended combo: bbrain mem archive --distilled --type session-summary --older-than 30d")
		fmt.Fprintln(stderr, "  flags must precede any explicit id (Go's flag parsing stops at the first positional arg)")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	var dur time.Duration
	if *olderThan != "" {
		var err error
		dur, err = parseDur(*olderThan)
		if err != nil {
			fmt.Fprintf(stderr, "mem archive: --older-than: %v\n", err)
			return 2
		}
	}
	root := *home
	if root == "" {
		root = brainRoot()
	}
	a := app.New(root)
	// Loud signal for the classic misconfiguration: a wrong/unset home resolves to
	// a path with no brain, and the plan then runs against an empty vault, exiting 0
	// silently (BBRAIN_HOME unset -> brainRoot() -> ~/.bbrain/default).
	if _, err := os.Stat(a.Brain.FactsDir()); os.IsNotExist(err) {
		fmt.Fprintf(stderr, "mem archive: warning: no brain at %q (facts dir missing); nothing will be selected until --home/BBRAIN_HOME points at a real brain or `bbrain init` runs there\n", root)
	}

	candidates, err := a.PlanArchive(app.ArchiveFilter{
		Types: types, OlderThan: dur, Distilled: *distilled, Project: *project, IDs: fs.Args(),
	})
	if err != nil {
		fmt.Fprintf(stderr, "mem archive: %v\n", err)
		return 2
	}

	var toArchive []app.ArchiveCandidate
	for _, c := range candidates {
		if c.Skipped == "" {
			toArchive = append(toArchive, c)
		}
	}

	if !*apply {
		fmt.Fprintf(stdout, "[dry-run] would archive %d fact(s); run with --apply\n", len(toArchive))
		for _, c := range candidates {
			if c.Skipped != "" {
				fmt.Fprintf(stdout, "skip %s: %s\n", c.Fact.ID, c.Skipped)
				continue
			}
			fmt.Fprintf(stdout, "%s\t%s\t%s\n", c.Fact.ID, c.Fact.Type, strings.Join(c.Reasons, ","))
		}
		return 0
	}

	failed := 0
	for _, c := range candidates {
		if c.Skipped != "" {
			fmt.Fprintf(stdout, "skip %s: %s\n", c.Fact.ID, c.Skipped)
			continue
		}
		if _, err := a.Archive(c.Fact.ID); err != nil {
			fmt.Fprintf(stderr, "mem archive: %s: %v\n", c.Fact.ID, err)
			failed++
			continue
		}
		fmt.Fprintf(stdout, "archived %s\n", c.Fact.ID)
	}
	fmt.Fprintf(stdout, "archived %d fact(s)\n", len(toArchive)-failed)
	if failed > 0 {
		return 1
	}
	return 0
}

func cmdMemUnarchive(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("mem unarchive", flag.ContinueOnError)
	fs.SetOutput(stderr)
	home := fs.String("home", "", "brain home (default: resolved brain root)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	ids := fs.Args()
	if len(ids) == 0 {
		fmt.Fprintln(stderr, "mem unarchive: usage: bbrain mem unarchive [--home DIR] <id>...")
		return 2
	}
	root := *home
	if root == "" {
		root = brainRoot()
	}
	a := app.New(root)
	if _, err := os.Stat(a.Brain.FactsDir()); os.IsNotExist(err) {
		fmt.Fprintf(stderr, "mem unarchive: warning: no brain at %q (facts dir missing); nothing will be restored until --home/BBRAIN_HOME points at a real brain or `bbrain init` runs there\n", root)
	}

	failed := 0
	for _, id := range ids {
		if _, err := a.Unarchive(id); err != nil {
			fmt.Fprintf(stderr, "mem unarchive: %s: %v\n", id, err)
			failed++
			continue
		}
		fmt.Fprintf(stdout, "unarchived %s\n", id)
	}
	if failed > 0 {
		return 1
	}
	return 0
}

func cmdMCP(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("mcp", flag.ContinueOnError)
	fs.SetOutput(stderr)
	home := fs.String("home", "", "brain home (default: resolved brain root)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	root := *home
	if root == "" {
		root = brainRoot()
	}
	a := app.New(root)
	// Loud signal for the classic misconfiguration: a wrong/unset home resolves to
	// a path with no brain, and every tool then returns empty without any error.
	if _, err := os.Stat(a.Brain.FactsDir()); os.IsNotExist(err) {
		fmt.Fprintf(stderr, "mcp: warning: no brain at %q (facts dir missing); tools will return empty until --home/BBRAIN_HOME points at a real brain or `bbrain init` runs there\n", root)
	}
	srv := &mcp.Server{App: a, Tools: mcp.DefaultTools()}
	if err := srv.Serve(context.Background(), stdin, stdout); err != nil {
		fmt.Fprintf(stderr, "mcp: %v\n", err)
		return 1
	}
	return 0
}

func cmdWatch(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("watch", flag.ContinueOnError)
	fs.SetOutput(stderr)
	interval := fs.Int("interval", 2, "poll interval in seconds")
	once := fs.Bool("once", false, "check once and exit (no loop)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	a := app.New(brainRoot())
	factsDir := a.Brain.FactsDir()
	last := ""
	for {
		fp, err := watch.FactsFingerprint(factsDir)
		if err != nil {
			fmt.Fprintf(stderr, "watch: %v\n", err)
			return 1
		}
		if fp != last {
			n, err := a.Reindex()
			if err != nil {
				fmt.Fprintf(stderr, "watch: %v\n", err)
				return 1
			}
			fmt.Fprintf(stdout, "reindexed %d facts\n", n)
			last = fp
		}
		if *once {
			return 0
		}
		time.Sleep(time.Duration(*interval) * time.Second)
	}
}

func cmdVault(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "move" {
		fmt.Fprintln(stderr, "vault: usage: bbrain vault move [--project DIR] <dest>")
		return 2
	}
	fs := flag.NewFlagSet("vault move", flag.ContinueOnError)
	fs.SetOutput(stderr)
	project := fs.String("project", "", "also refresh this project's .mcp.json + CLAUDE.md at the new home")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	rest := fs.Args()
	if len(rest) != 1 {
		fmt.Fprintln(stderr, "vault move: exactly one destination is required (flags before <dest>)")
		return 2
	}
	a := app.New(brainRoot())
	newRoot, n, err := a.VaultMove(rest[0], app.VaultMoveOptions{ProjectDir: *project})
	if err != nil {
		fmt.Fprintf(stderr, "vault move: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "moved brain to %s (reindexed %d facts)\n", newRoot, n)
	fmt.Fprintf(stdout, "next: export BBRAIN_HOME=%s\n", newRoot)
	if *project != "" {
		fmt.Fprintf(stdout, "refreshed integration in %s\n", *project)
	}
	return 0
}

func cmdContext(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("context", flag.ContinueOnError)
	fs.SetOutput(stderr)
	home := fs.String("home", "", "brain home (default: resolved brain root)")
	project := fs.String("project", "", "only include facts in this project")
	limit := fs.Int("limit", 10, "max recent facts")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	root := *home
	if root == "" {
		root = brainRoot()
	}
	a := app.New(root)
	out, err := a.Context(*project, *limit)
	if err != nil {
		fmt.Fprintf(stderr, "context: %v\n", err)
		return 1
	}
	fmt.Fprint(stdout, out)
	return 0
}

func cmdPromptSubmit(args []string, stdin io.Reader, stdout io.Writer) int {
	fs := flag.NewFlagSet("prompt-submit", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	home := fs.String("home", "", "brain home (default: resolved brain root)")
	if err := fs.Parse(args); err != nil {
		// Never block the message on a flag error: emit a no-op and exit 0.
		io.WriteString(stdout, "{}")
		return 0
	}
	root := *home
	if root == "" {
		root = brainRoot()
	}
	prompthook.Run(stdin, stdout, root, time.Now())
	return 0
}

func defaultVault() string {
	if h, err := os.UserHomeDir(); err == nil {
		return filepath.Join(h, "bbrain")
	}
	return "bbrain"
}

func cmdInstall(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("install", flag.ContinueOnError)
	fs.SetOutput(stderr)
	vault := fs.String("vault", defaultVault(), "vault location L (memory + degraded CLAUDE.md)")
	agent := fs.String("agent", "claude-code", "code agent to integrate")
	scope := fs.String("scope", "", "install scope: user|project")
	model := fs.String("model", "claude-sonnet-4-6", "claude model for the LLM adapter")
	dry := fs.Bool("dry-run", false, "print actions without writing")
	nonInteractive := fs.Bool("non-interactive", false, "use flags only; no prompts")
	project := fs.String("project", "", "project directory (default: cwd)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	home, _ := os.UserHomeDir()
	cwd, _ := os.Getwd()
	projDir := *project
	if projDir == "" {
		projDir = cwd
	}
	o := install.Options{Vault: *vault, Agent: *agent, Scope: *scope, Model: *model,
		HomeDir: home, ProjectDir: projDir, DryRun: *dry}
	if !*nonInteractive {
		if !isatty.IsTerminal(os.Stdin.Fd()) || !isatty.IsTerminal(os.Stdout.Fd()) {
			fmt.Fprintln(stderr, "install: no interactive terminal; use --non-interactive with --vault/--scope")
			return 2
		}
		def := o
		if def.Scope == "" {
			def.Scope = "project"
		}
		resolved, err := install.Wizard(def)
		if err != nil {
			if errors.Is(err, install.ErrAborted) {
				fmt.Fprintln(stdout, "install: cancelled.")
				return 0
			}
			fmt.Fprintf(stderr, "install: %v\n", err)
			return 1
		}
		o.Vault, o.Agent, o.Scope = resolved.Vault, resolved.Agent, resolved.Scope
	}
	if o.Scope != "user" && o.Scope != "project" {
		fmt.Fprintln(stderr, "install: --scope must be user or project")
		return 2
	}
	actions, err := install.PlanInstall(o)
	if err != nil {
		fmt.Fprintf(stderr, "install: %v\n", err)
		return 1
	}
	if o.DryRun {
		fmt.Fprintln(stdout, "[dry-run] would do:")
		for _, a := range actions {
			fmt.Fprintf(stdout, "- %s — %s\n", a.Path, a.Summary)
		}
		return 0
	}
	if err := install.Apply(actions); err != nil {
		fmt.Fprintf(stderr, "install: %v\n", err)
		return 1
	}
	mem := filepath.Join(o.Vault, "memory")
	fmt.Fprintf(stdout, "installed BBrain (%s scope). Memory vault: %s\n", o.Scope, mem)
	fmt.Fprintf(stdout, "wiki backend: source %s\n", filepath.Join(mem, ".bbrain", "env.sh"))
	if n, err := app.New(mem).Reindex(); err != nil {
		fmt.Fprintf(stderr, "install: reindex failed: %v — run 'bbrain reindex' to migrate the index\n", err)
	} else {
		fmt.Fprintf(stdout, "reindexed %d facts\n", n)
	}
	return 0
}

func cmdUninstall(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("uninstall", flag.ContinueOnError)
	fs.SetOutput(stderr)
	vault := fs.String("vault", defaultVault(), "vault location (for --purge)")
	agent := fs.String("agent", "claude-code", "code agent")
	scope := fs.String("scope", "", "scope: user|project")
	project := fs.String("project", "", "project directory (default: cwd)")
	purge := fs.Bool("purge", false, "also delete the vault (DESTROYS memory)")
	dry := fs.Bool("dry-run", false, "print actions without writing")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *scope != "user" && *scope != "project" {
		fmt.Fprintln(stderr, "uninstall: --scope must be user or project")
		return 2
	}
	home, _ := os.UserHomeDir()
	cwd, _ := os.Getwd()
	projDir := *project
	if projDir == "" {
		projDir = cwd
	}
	o := install.Options{Vault: *vault, Agent: *agent, Scope: *scope, HomeDir: home, ProjectDir: projDir, DryRun: *dry, Purge: *purge}
	actions, err := install.PlanUninstall(o)
	if err != nil {
		fmt.Fprintf(stderr, "uninstall: %v\n", err)
		return 1
	}
	if o.DryRun {
		fmt.Fprintln(stdout, "[dry-run] would do:")
		for _, a := range actions {
			fmt.Fprintf(stdout, "- %s — %s\n", a.Path, a.Summary)
		}
		return 0
	}
	if err := install.Apply(actions); err != nil {
		fmt.Fprintf(stderr, "uninstall: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "uninstalled BBrain (%s scope).%s\n", o.Scope,
		map[bool]string{true: " Vault purged.", false: " Vault kept."}[o.Purge])
	return 0
}
