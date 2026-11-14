// Command agent-blame shows what coding agents did to a repository as a
// live, branching tree of prompts, reads, edits, runs and commits.
package main

import (
	"os"

	"agent-blame/internal/cli"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	os.Exit(cli.Run(os.Args[1:], version, os.Stdin, os.Stdout, os.Stderr))
}
