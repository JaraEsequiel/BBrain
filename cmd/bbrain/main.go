package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"bbrain/internal/app"
	"bbrain/internal/mcp"
	"bbrain/internal/store"
)

const version = "0.1.0-dev"

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
		fmt.Fprintln(stderr, "usage: bbrain <version|init|save|search|reindex|link|why|related|candidates|wiki|mcp> [args]")
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
	case "mcp":
		return cmdMCP(args[1:], stdin, stdout, stderr)
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
	res, err := a.Search(query, *limit)
	if err != nil {
		fmt.Fprintf(stderr, "search: %v\n", err)
		return 1
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
	a := app.New(brainRoot())
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
	return 0
}

func cmdWikiLink(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("wiki link", flag.ContinueOnError)
	fs.SetOutput(stderr)
	project := fs.String("project", "", "only link facts in this project")
	scope := fs.String("scope", "", "only link facts in this scope")
	limit := fs.Int("limit", 8, "max FTS candidates considered per fact")
	dryRun := fs.Bool("dry-run", false, "print proposed links without writing")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	a := app.New(brainRoot())
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
	return 0
}

func cmdWikiLint(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("wiki lint", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cats := fs.String("categories", "", "extra wiki categories (comma-separated)")
	fix := fs.Bool("fix", false, "apply mechanically-safe repairs")
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
	a := app.New(brainRoot())
	res, err := a.WikiLint(app.WikiLintOptions{Categories: extra, Fix: *fix})
	if err != nil {
		fmt.Fprintf(stderr, "wiki lint: %v\n", err)
		return 1
	}
	for _, is := range res.Fixed {
		fmt.Fprintf(stdout, "fixed: %s — %s\n", is.Kind, is.Message)
	}
	for _, is := range res.Issues {
		fmt.Fprintf(stdout, "%s: %s — %s\n", is.Kind, is.Location, is.Message)
	}
	if len(res.Issues) > 0 {
		return 1
	}
	return 0
}

func cmdMCP(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	a := app.New(brainRoot())
	srv := &mcp.Server{App: a, Tools: mcp.DefaultTools()}
	if err := srv.Serve(context.Background(), stdin, stdout); err != nil {
		fmt.Fprintf(stderr, "mcp: %v\n", err)
		return 1
	}
	return 0
}
