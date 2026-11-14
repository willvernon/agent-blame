package cli

import (
	"errors"
	"os"

	"agent-blame/internal/load"
	"agent-blame/internal/sources/git"
	"agent-blame/internal/tui"
)

func runTUI(env Env, args []string) error {
	fs := newFlags("agent-blame", env)
	ascii := fs.Bool("ascii", false, "draw with ASCII glyphs")
	keepBG := fs.Bool("terminal-bg", false, "keep the terminal's own background instead of the paper theme")
	all := fs.Bool("all", false, "show searches, web fetches and other tools too")
	open := fs.String("open", "", "start on the first item whose title contains this text")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if !isTerminal(os.Stdout) {
		return errors.New("the tree needs a terminal; use agent-blame dump or agent-blame render instead")
	}
	repo, err := git.Open(env.Dir)
	if err != nil {
		return err
	}
	// Opening the tree creates the store, which is also what turns on
	// capture by the hook for this repository.
	l, err := load.New(repo, true)
	if err != nil {
		return err
	}
	return tui.Run(tui.Options{
		Repo: repo, Loader: l, PaperBG: !*keepBG && os.Getenv("NO_COLOR") == "",
		Glyphs: glyphs(*ascii), ShowOther: *all, Open: *open, OpenMatching: OpenMatching,
	})
}

func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}
