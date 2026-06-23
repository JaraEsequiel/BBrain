package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"bbrain/internal/app"
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

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: bbrain <version|init|save|search|reindex> [args]")
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
