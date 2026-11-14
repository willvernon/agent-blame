package layout_test

import (
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"

	"agent-blame/internal/graph"
	"agent-blame/internal/sources/git"
	"agent-blame/internal/testutil"
	"agent-blame/internal/tui/layout"
)

func scenario(t *testing.T) (*graph.Graph, testutil.Scenario) {
	t.Helper()
	sc := testutil.BuildScenario(testutil.NewRepo(t))
	repo, err := git.Open(sc.Repo.Dir)
	if err != nil {
		t.Fatal(err)
	}
	return graph.Build(repo, sc.Events, graph.Options{}), sc
}

func state(sc testutil.Scenario, w, h int) *layout.State {
	return &layout.State{
		Width: w, Height: h, Now: sc.T0.Add(5 * time.Hour), Measure: lipgloss.Width,
		Repo: "~/src/app", Glyphs: layout.Unicode,
	}
}

// plain renders a frame with selection and scope marks, as render --marks does.
func plain(f layout.Frame) string {
	var b strings.Builder
	for _, l := range f.Lines() {
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
		b.WriteString(strings.TrimRight(l.Text(), " "))
		b.WriteByte('\n')
	}
	return b.String()
}

// stable replaces commit hashes, which differ per run, with a placeholder.
func stable(s string, g *graph.Graph) string {
	for sha := range g.Commits {
		s = strings.ReplaceAll(s, sha[:7], "<sha>  ")
	}
	return s
}

func TestDefaultViewGolden(t *testing.T) {
	g, sc := scenario(t)
	for _, w := range []int{100, 60} {
		st := state(sc, w, 30)
		layout.Defaults(g, st)
		f := layout.Build(g, st)
		layout.Follow(f.Rows, st)
		f = layout.Build(g, st)
		testutil.Golden(t, "default_"+itoa(w)+".txt", stable(plain(f), g))
	}
}

func TestOpenEditGolden(t *testing.T) {
	g, sc := scenario(t)
	st := state(sc, 100, 40)
	layout.Defaults(g, st)
	st.Expanded["p2"] = true
	st.Expanded["e4"] = true
	st.Selected = "e4"
	f := layout.Build(g, st)
	layout.Follow(f.Rows, st)
	f = layout.Build(g, st)
	testutil.Golden(t, "open_edit.txt", stable(plain(f), g))
}

func TestFoldLevelsAndASCII(t *testing.T) {
	g, sc := scenario(t)
	st := state(sc, 90, 30)
	st.Glyphs = layout.ASCII
	layout.Defaults(g, st)
	layout.Fold(g, st, 1)
	f := layout.Build(g, st)
	testutil.Golden(t, "fold1_ascii.txt", stable(plain(f), g))
	layout.Fold(g, st, 3)
	f = layout.Build(g, st)
	testutil.Golden(t, "fold3_ascii.txt", stable(plain(f), g))
}

func TestInvariants(t *testing.T) {
	g, sc := scenario(t)
	for _, w := range []int{60, 80, 100, 140} {
		for level := 1; level <= 3; level++ {
			st := state(sc, w, 200)
			layout.Defaults(g, st)
			layout.Fold(g, st, level)
			f := layout.Build(g, st)
			for i, l := range f.Lines() {
				if got := lipgloss.Width(l.Text()); got > w {
					t.Errorf("w=%d fold=%d line %d is %d cells wide: %q", w, level, i, got, l.Text())
				}
			}
			// every node row carries exactly one node glyph in the gutter
			for i, r := range f.Rows {
				if !r.IsNode() {
					continue
				}
				line := f.Body[i]
				glyphs := 0
				for _, s := range line.Spans[:line.TextFrom] {
					if strings.HasPrefix(string(s.Style), "node.") || strings.HasPrefix(string(s.Style), "halo.") {
						glyphs++
					}
				}
				if glyphs != 1 {
					t.Errorf("w=%d fold=%d row %d (%s) has %d node glyphs", w, level, i, r.Node.Title, glyphs)
				}
			}
		}
	}
}

func TestMoveOpenClose(t *testing.T) {
	g, sc := scenario(t)
	st := state(sc, 100, 40)
	layout.Defaults(g, st)
	if st.Selected != "e7" {
		t.Fatalf("default selection = %s, want the latest event e7", st.Selected)
	}
	rows := layout.Flatten(g, st)
	layout.Move(rows, st, -1)
	if st.Selected != "e6" {
		t.Errorf("after k: %s", st.Selected)
	}
	layout.Close(g, st) // e6 is closed, so this moves to the prompt
	if st.Selected != "p3" {
		t.Errorf("after h on a closed event: %s", st.Selected)
	}
	layout.Close(g, st) // the prompt is open, so this closes it
	if st.Expanded["p3"] {
		t.Error("h did not close the open prompt")
	}
	layout.Open(g, st)
	if !st.Expanded["p3"] {
		t.Error("l did not open the prompt")
	}
	layout.Open(g, st)
	if st.Selected != "e6" {
		t.Errorf("l on an open prompt should step in, got %s", st.Selected)
	}
}

func TestFollowLatestFromTheTail(t *testing.T) {
	g, sc := scenario(t)
	st := state(sc, 100, 40)
	layout.Defaults(g, st)
	if !layout.AtTail(g, st) {
		t.Fatal("default view should be at the tail")
	}
	layout.Fold(g, st, 1)
	st.Selected = g.Root.ID
	if layout.AtTail(g, st) {
		t.Fatal("selection on the root is not the tail")
	}
	layout.FollowLatest(g, st)
	if st.Selected != "e7" || !st.Expanded["p3"] || !st.Expanded["branch:rate-limit"] {
		t.Errorf("selected %s, expanded %v", st.Selected, st.Expanded)
	}
}

func itoa(n int) string {
	return strings.TrimSpace(strings.Repeat(" ", 0) + string(rune('0'+n/100%10)) + string(rune('0'+n/10%10)) + string(rune('0'+n%10)))
}
