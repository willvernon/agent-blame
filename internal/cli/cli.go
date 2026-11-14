// Package cli implements the agent-blame subcommands.
package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"agent-blame/internal/sources/git"
)

const usage = `agent-blame shows what coding agents did to this repository.

Usage:
  agent-blame                  open the tree for the repository in this directory
  agent-blame dump [--json] [--tree]
                               print events as text or JSON
  agent-blame render [--width N] [--height N] [--fold 1|2|3] [--ansi]
                               print one frame of the tree, without a terminal UI
  agent-blame sync             import new transcript records into the local store
  agent-blame hook claude-code read one hook payload on stdin and record it
  agent-blame install claude-code [--uninstall]
                               add or remove the capture hooks in Claude Code settings
  agent-blame doctor           report what was found and what is missing
  agent-blame version          print the version

Everything stays inside .git/agent-blame. Nothing is sent anywhere.
`

// Env is what a subcommand can touch, so tests can swap it.
type Env struct {
	Stdin   io.Reader
	Stdout  io.Writer
	Stderr  io.Writer
	Dir     string
	Version string
}

// Run executes one invocation and returns the exit code.
func Run(args []string, version string, stdin io.Reader, stdout, stderr io.Writer) int {
	dir, _ := os.Getwd()
	env := Env{Stdin: stdin, Stdout: stdout, Stderr: stderr, Dir: dir, Version: version}
	cmd, rest := "", args
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, rest = args[0], args[1:]
	}
	var err error
	switch cmd {
	case "":
		if len(rest) > 0 && (rest[0] == "--version" || rest[0] == "-v") {
			fmt.Fprintln(stdout, "agent-blame", version)
			return 0
		}
		if len(rest) > 0 && (rest[0] == "--help" || rest[0] == "-h") {
			fmt.Fprint(stdout, usage)
			return 0
		}
		err = runTUI(env, rest)
	case "dump":
		err = runDump(env, rest)
	case "render":
		err = runRender(env, rest)
	case "sync":
		err = runSync(env, rest)
	case "hook":
		return runHook(env, rest) // never fails the agent
	case "install":
		err = runInstall(env, rest)
	case "doctor":
		err = runDoctor(env, rest)
	case "version":
		fmt.Fprintln(stdout, "agent-blame", version)
		return 0
	case "help":
		fmt.Fprint(stdout, usage)
		return 0
	default:
		fmt.Fprintf(stderr, "agent-blame: unknown command %q\n\n%s", cmd, usage)
		return 2
	}
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		if errors.Is(err, git.ErrNotRepo) {
			fmt.Fprintln(stderr, "agent-blame: run this inside a git repository")
			return 1
		}
		fmt.Fprintln(stderr, "agent-blame:", err)
		return 1
	}
	return 0
}

func newFlags(name string, env Env) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	return fs
}
