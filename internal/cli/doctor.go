package cli

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"charm.land/lipgloss/v2"

	"agent-blame/internal/load"
	"agent-blame/internal/sources/claudecode"
	"agent-blame/internal/sources/git"
	"agent-blame/internal/store"
	"agent-blame/internal/tui/layout"
	"agent-blame/internal/viewer"
)

func runDoctor(env Env, args []string) error {
	fs := newFlags("doctor", env)
	if err := fs.Parse(args); err != nil {
		return err
	}
	w := env.Stdout
	say := func(label, format string, a ...any) { fmt.Fprintf(w, "%-12s %s\n", label, fmt.Sprintf(format, a...)) }

	say("version", "%s", env.Version)
	repo, err := git.Open(env.Dir)
	if err != nil {
		say("repository", "none: %v", err)
		return nil
	}
	say("repository", "%s (default branch %s)", claudecode.ShortenHome(repo.Root), repo.DefaultBranch())
	if len(repo.Worktrees) > 1 {
		say("worktrees", "%d", len(repo.Worktrees))
	}

	storeDir := store.Path(repo.CommonDir)
	if store.Exists(repo.CommonDir) {
		files, hooks := 0, 0
		entries, _ := os.ReadDir(filepath.Join(storeDir, "events"))
		for _, e := range entries {
			files++
			if strings.HasPrefix(e.Name(), "hook-") {
				hooks += countLines(filepath.Join(storeDir, "events", e.Name()))
			}
		}
		say("store", "%s, %d event files, %d hook events", claudecode.ShortenHome(storeDir), files, hooks)
		if n := countLines(filepath.Join(storeDir, "errors.log")); n > 0 {
			say("errors", "%d lines in %s", n, claudecode.ShortenHome(filepath.Join(storeDir, "errors.log")))
		}
	} else {
		say("store", "not created yet. Open agent-blame or run agent-blame sync here to create it and turn on capture.")
	}

	l, err := load.New(repo, false)
	if err != nil {
		return err
	}
	res, err := l.Load(false)
	if err != nil {
		say("transcripts", "error: %v", err)
	} else {
		unparsed, partial := 0, 0
		for _, t := range res.Transcripts {
			unparsed += t.Stats.Unparsed
			if t.Stats.Partial {
				partial++
			}
		}
		say("transcripts", "%d sessions for this repository in %s", len(res.Transcripts), claudecode.ShortenHome(claudecode.ProjectsDir()))
		say("events", "%d (%d unreadable transcript lines, %d sessions mid-write)", len(res.Events), unparsed, partial)
	}

	if v, err := viewer.Find(); err == nil {
		say("diff viewer", "%s", v.Name())
	} else {
		say("diff viewer", "%v", err)
	}
	if settings := userSettings(); HooksInstalled(settings) {
		say("hooks", "installed in %s", claudecode.ShortenHome(settings))
	} else {
		say("hooks", "not installed. Run agent-blame install claude-code for live capture of HEAD and outcomes.")
	}

	g := layout.Unicode
	row := []string{g.Main, g.PR, g.Prompt, g.Read, g.Edit, g.Run, g.Commit, g.V, g.VDash, g.Tee, g.H, g.Open, g.Close}
	var wide []string
	for _, s := range row {
		if lipgloss.Width(s) != 1 {
			wide = append(wide, s)
		}
	}
	say("glyphs", "%s", strings.Join(row, " "))
	if len(wide) > 0 {
		say("", "these measure wider than one cell: %s. Set AGENT_BLAME_GLYPHS=ascii.", strings.Join(wide, " "))
	} else {
		say("", "each should sit in one cell. If they look misaligned, set AGENT_BLAME_GLYPHS=ascii.")
	}
	say("terminal", "TERM=%s COLORTERM=%s", os.Getenv("TERM"), os.Getenv("COLORTERM"))
	return nil
}

func countLines(path string) int {
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()
	n := 0
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		n++
	}
	return n
}
