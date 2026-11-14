package layout

import (
	"fmt"
	"strings"

	"agent-blame/internal/graph"
	"agent-blame/internal/model"
)

// Row is one line of the tree before styling: a node, or one line of an
// expanded node's detail block.
type Row struct {
	Node   *graph.Node
	Detail int // -1 for the node's own row
	Label  string
	Text   string
	Style  Style
}

// IsNode reports whether the row is a node's own row.
func (r Row) IsNode() bool { return r.Detail < 0 }

const maxHunkLines = 14
const maxOutputLines = 8
const maxPromptLines = 8

// Expandable reports whether a node has anything to show when opened.
func Expandable(n *graph.Node) bool {
	if n.Kind == graph.KindMain {
		return false
	}
	if len(n.Children) > 0 {
		return true
	}
	return n.Event != nil && n.IsEvent()
}

// Flatten lists the visible rows in display order.
func Flatten(g *graph.Graph, st *State) []Row {
	if g == nil || g.Root == nil {
		return nil
	}
	var rows []Row
	textW := st.textWidth()
	var walk func(n *graph.Node)
	walk = func(n *graph.Node) {
		rows = append(rows, Row{Node: n, Detail: -1})
		open := n.Kind == graph.KindMain || st.Expanded[n.ID]
		if !open {
			return
		}
		for i, d := range detailLines(n, textW-chevW-labelW-1, st) {
			d.Node, d.Detail = n, i
			rows = append(rows, d)
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(g.Root)
	return rows
}

const labelW = 7 // width of the kind column and of detail labels

// detailLines builds the block shown under an expanded node.
func detailLines(n *graph.Node, w int, st *State) []Row {
	if w < 10 {
		w = 10
	}
	var out []Row
	add := func(label, text string, style Style) {
		out = append(out, Row{Label: label, Text: text, Style: style})
	}
	wrapped := func(label, text string, style Style, max int) {
		lines := wrap(text, w, st.measure())
		for i, l := range lines {
			if i == max {
				add("", fmt.Sprintf("... %d more lines", len(lines)-max), StyleMuted)
				break
			}
			if i > 0 {
				label = ""
			}
			add(label, l, style)
		}
	}

	if n.Kind == model.KindPrompt {
		// Only repeat the prompt when the row had to cut it short.
		if st.measure()(flatText(n.Title)) > subjectWidth(st) {
			wrapped("full", n.Title, StyleMuted, maxPromptLines)
		}
		return out
	}
	e := n.Event
	if e == nil || !n.IsEvent() {
		return out
	}

	if e.Kind == model.KindRun {
		wrapped("cmd", e.Subject, StyleCtx, 4)
	} else if change := changeText(n); change != "" {
		wrapped("change", change, StyleInk, 3)
	}
	if e.Note.Why != "" {
		wrapped("why", e.Note.Why, StyleInk, 6)
	}
	switch e.Kind {
	case model.KindEdit:
		if e.Hunk != "" {
			lines := strings.Split(strings.TrimRight(e.Hunk, "\n"), "\n")
			for i, l := range lines {
				if i == maxHunkLines {
					add("", fmt.Sprintf("... %d more lines, press o for the full diff", len(lines)-maxHunkLines), StyleMuted)
					break
				}
				label := ""
				if i == 0 {
					label = "diff"
				}
				add(label, truncate(expandTabs(l), w, st.measure()), hunkStyle(l))
			}
		}
		add("commit", commitText(n), commitStyle(n))
		if e.Hunk != "" {
			add("open", "o this change   O whole file", StyleMuted)
		}
	case model.KindRun:
		if e.Output != "" {
			lines := strings.Split(e.Output, "\n")
			if len(lines) > maxOutputLines {
				lines = append([]string{fmt.Sprintf("... %d earlier lines", len(lines)-maxOutputLines)}, lines[len(lines)-maxOutputLines:]...)
			}
			for i, l := range lines {
				label := ""
				if i == 0 {
					label = "output"
				}
				add(label, truncate(expandTabs(l), w, st.measure()), StyleMuted)
			}
		}
		if e.MadeCommit != "" {
			add("commit", "made "+short(e.MadeCommit), StyleText(model.KindCommit))
		}
	case model.KindRead:
		if e.Outside {
			add("where", "outside this repository", StyleMuted)
		}
	}
	return out
}

func changeText(n *graph.Node) string {
	e := n.Event
	if e.Note.Change != "" {
		return e.Note.Change
	}
	switch e.Kind {
	case model.KindEdit:
		if e.OK != nil && !*e.OK {
			return "the edit failed and changed nothing"
		}
		if e.Created {
			return "created this file"
		}
		if e.Stat != nil {
			return fmt.Sprintf("added %d lines, removed %d", e.Stat.Add, e.Stat.Del)
		}
	case model.KindRead:
		return "read this file"
	}
	return ""
}

func commitText(n *graph.Node) string {
	e := n.Event
	switch {
	case e.Commit != nil:
		how := "exact: committed in this session"
		if e.Commit.How == model.HowInferred {
			how = "inferred: first later commit that touches this file"
		}
		return short(e.Commit.SHA) + "  " + how
	case e.Outside:
		return "outside this repository"
	case e.OK != nil && !*e.OK:
		return "nothing to commit"
	case n.Status == graph.StatusUncommitted:
		return "not committed yet"
	case n.Status == graph.StatusOrphan:
		return "not committed, and no longer in the working tree"
	}
	return ""
}

func commitStyle(n *graph.Node) Style {
	if n.Event.Commit != nil {
		return StyleText(model.KindCommit)
	}
	return StyleMuted
}

func hunkStyle(l string) Style {
	switch {
	case strings.HasPrefix(l, "@@"):
		return StyleMuted
	case strings.HasPrefix(l, "+"):
		return StyleAdd
	case strings.HasPrefix(l, "-"):
		return StyleDel
	}
	return StyleCtx
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

func expandTabs(s string) string { return strings.ReplaceAll(s, "\t", "    ") }

func flatText(s string) string { return strings.Join(strings.Fields(s), " ") }
