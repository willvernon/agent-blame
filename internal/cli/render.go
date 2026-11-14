package cli

import (
	"fmt"
	"os"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"agent-blame/internal/graph"
	"agent-blame/internal/load"
	"agent-blame/internal/sources/claudecode"
	"agent-blame/internal/sources/git"
	"agent-blame/internal/tui"
	"agent-blame/internal/tui/layout"
)

func runRender(env Env, args []string) error {
	fs := newFlags("render", env)
	width := fs.Int("width", 100, "frame width in columns")
	height := fs.Int("height", 40, "frame height in lines")
	fold := fs.Int("fold", 0, "fold everything to 1 (branches), 2 (prompts) or 3 (events); 0 keeps the default view")
	open := fs.String("open", "", "select the first item whose title contains this text, and open it")
	ansi := fs.Bool("ansi", false, "print with colors")
	marks := fs.Bool("marks", false, "prefix each line with > selected, ~ inside the open scope, . faded")
	ascii := fs.Bool("ascii", false, "draw with ASCII glyphs")
	if err := fs.Parse(args); err != nil {
		return err
	}
	repo, err := git.Open(env.Dir)
	if err != nil {
		return err
	}
	l, err := load.New(repo, false)
	if err != nil {
		return err
	}
	res, err := l.Load(false)
	if err != nil {
		return err
	}
	g := graph.Build(repo, res.Events, graph.Options{})
	st := &layout.State{
		Width: *width, Height: *height, Now: time.Now(), Measure: lipgloss.Width,
		Repo: claudecode.ShortenHome(repo.Root), Glyphs: glyphs(*ascii),
	}
	layout.Defaults(g, st)
	if *fold > 0 {
		layout.Fold(g, st, *fold)
	}
	if *open != "" {
		if !OpenMatching(g, st, *open) {
			return fmt.Errorf("nothing matches %q", *open)
		}
	}
	frame := layout.Build(g, st)
	layout.Follow(frame.Rows, st)
	frame = layout.Build(g, st)
	if *ansi {
		fmt.Fprintln(env.Stdout, tui.NewTheme(true).Render(frame.Lines(), *width))
		return nil
	}
	fmt.Fprint(env.Stdout, PlainFrame(frame, *marks))
	return nil
}

// OpenMatching selects and opens the first node whose title contains text,
// opening its ancestors too.
func OpenMatching(g *graph.Graph, st *layout.State, text string) bool {
	var hit *graph.Node
	g.Walk(func(n *graph.Node, _ int) bool {
		if hit == nil && strings.Contains(n.Title, text) {
			hit = n
		}
		return hit == nil
	})
	if hit == nil {
		return false
	}
	for p := hit; p != nil; p = p.Parent {
		if p != hit || layout.Expandable(p) {
			st.Expanded[p.ID] = true
		}
	}
	st.Selected = hit.ID
	return true
}

// PlainFrame prints a frame without styles, trailing spaces trimmed.
func PlainFrame(f layout.Frame, marks bool) string {
	var b strings.Builder
	for _, l := range f.Lines() {
		if marks {
			switch {
			case l.Selected:
				b.WriteString("> ")
			case l.Tint != "":
				b.WriteString("~ ")
			case l.Faded:
				b.WriteString(". ")
			default:
				b.WriteString("  ")
			}
		}
		b.WriteString(strings.TrimRight(l.Text(), " "))
		b.WriteByte('\n')
	}
	return b.String()
}

func glyphs(ascii bool) layout.Glyphs {
	if ascii || os.Getenv("AGENT_BLAME_GLYPHS") == "ascii" {
		return layout.ASCII
	}
	return layout.Unicode
}
