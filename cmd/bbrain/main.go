package main

import (
	"fmt"
	"io"
	"os"
)

const version = "0.1.0-dev"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run dispatches a subcommand and returns a process exit code. It is the
// testable core of main: all I/O goes through the provided writers.
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: bbrain <command> [args]")
		return 2
	}
	switch args[0] {
	case "version":
		fmt.Fprintln(stdout, "bbrain "+version)
		return 0
	default:
		fmt.Fprintf(stderr, "unknown command: %s\n", args[0])
		return 2
	}
}
