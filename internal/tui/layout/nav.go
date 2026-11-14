package layout

import (
	"agent-blame/internal/graph"
	"agent-blame/internal/model"
)

// Defaults opens every branch and the most recent prompt, and selects that
// prompt's latest event, so the first screen shows what happened last.
func Defaults(g *graph.Graph, st *State) {
	st.Expanded = map[string]bool{}
	if g == nil || g.Root == nil {
		return
	}
	var latest *graph.Node
	g.Walk(func(n *graph.Node, _ int) bool {
		switch n.Kind {
		case graph.KindPR:
			st.Expanded[n.ID] = true
		case model.KindPrompt:
			if latest == nil || !n.Time.Before(latest.Time) {
				latest = n
			}
		}
		return true
	})
	st.Selected = g.Root.ID
	if latest == nil {
		return
	}
	st.Selected = latest.ID
	if len(latest.Children) > 0 {
		st.Expanded[latest.ID] = true
		st.Selected = latest.Children[len(latest.Children)-1].ID
	}
}

// AtTail reports whether the selection is on the last row, the position
// that means "follow new activity".
func AtTail(g *graph.Graph, st *State) bool {
	if g == nil || g.Root == nil {
		return true
	}
	rows := Flatten(g, st)
	idx := nodeRows(rows)
	return len(idx) == 0 || rows[idx[len(idx)-1]].Node.ID == st.Selected
}

// FollowLatest opens the newest prompt and its branch and selects its last
// item, leaving everything else as it was.
func FollowLatest(g *graph.Graph, st *State) {
	var latest *graph.Node
	g.Walk(func(n *graph.Node, _ int) bool {
		if n.Kind == model.KindPrompt && (latest == nil || !n.Time.Before(latest.Time)) {
			latest = n
		}
		return true
	})
	if latest == nil {
		return
	}
	for p := latest.Parent; p != nil; p = p.Parent {
		if p.Kind != graph.KindMain {
			st.Expanded[p.ID] = true
		}
	}
	st.Selected = latest.ID
	if len(latest.Children) > 0 {
		st.Expanded[latest.ID] = true
		st.Selected = latest.Children[len(latest.Children)-1].ID
	}
}

// Fold sets every item to one depth: 1 shows branches, 2 prompts, 3 events.
func Fold(g *graph.Graph, st *State, level int) {
	st.Expanded = map[string]bool{}
	g.Walk(func(n *graph.Node, _ int) bool {
		switch {
		case n.Kind == graph.KindPR && level >= 2:
			st.Expanded[n.ID] = true
		case n.Kind == model.KindPrompt && level >= 3:
			st.Expanded[n.ID] = true
		}
		return true
	})
	// keep the selection on a visible row
	if n := g.ByID[st.Selected]; n != nil {
		for p := n.Parent; p != nil; p = p.Parent {
			if p.Kind != graph.KindMain && !st.Expanded[p.ID] {
				st.Selected = p.ID
			}
		}
	}
}

// nodeRows lists the indexes of node rows, the rows the selection can be on.
func nodeRows(rows []Row) []int {
	var out []int
	for i, r := range rows {
		if r.IsNode() {
			out = append(out, i)
		}
	}
	return out
}

// Move shifts the selection by delta node rows, clamped to the ends.
func Move(rows []Row, st *State, delta int) {
	idx := nodeRows(rows)
	if len(idx) == 0 {
		return
	}
	cur := 0
	for k, i := range idx {
		if rows[i].Node.ID == st.Selected {
			cur = k
		}
	}
	next := cur + delta
	if next < 0 {
		next = 0
	}
	if next >= len(idx) {
		next = len(idx) - 1
	}
	st.Selected = rows[idx[next]].Node.ID
}

// Open expands the selected item, or steps into it if it is already open.
func Open(g *graph.Graph, st *State) {
	n := g.ByID[st.Selected]
	if n == nil || !Expandable(n) {
		return
	}
	if !st.Expanded[n.ID] {
		st.Expanded[n.ID] = true
		return
	}
	if len(n.Children) > 0 {
		st.Selected = n.Children[0].ID
	}
}

// Close collapses the selected item, or moves to its parent.
func Close(g *graph.Graph, st *State) {
	n := g.ByID[st.Selected]
	if n == nil {
		return
	}
	if st.Expanded[n.ID] {
		delete(st.Expanded, n.ID)
		return
	}
	if n.Parent != nil {
		st.Selected = n.Parent.ID
	}
}

// Toggle opens or closes the selected item.
func Toggle(g *graph.Graph, st *State) {
	n := g.ByID[st.Selected]
	if n == nil || !Expandable(n) {
		return
	}
	if st.Expanded[n.ID] {
		delete(st.Expanded, n.ID)
	} else {
		st.Expanded[n.ID] = true
	}
}

// Follow scrolls so the selected item, and as much of its open block as
// fits, stays on screen with a small margin.
func Follow(rows []Row, st *State) {
	body := st.BodyHeight()
	sel := -1
	for i, r := range rows {
		if r.IsNode() && r.Node.ID == st.Selected {
			sel = i
			break
		}
	}
	if sel < 0 {
		return
	}
	end := sel
	for i := sel + 1; i < len(rows) && !rows[i].IsNode() && rows[i].Node == rows[sel].Node; i++ {
		end = i
	}
	margin := 2
	if body < 10 {
		margin = 0
	}
	if end+margin >= st.Top+body {
		st.Top = end + margin - body + 1
	}
	if sel-margin < st.Top {
		st.Top = sel - margin
	}
	if st.Top > len(rows)-body {
		st.Top = len(rows) - body
	}
	if st.Top < 0 {
		st.Top = 0
	}
}

// Keep carries the view over to a rebuilt graph: open items that still
// exist stay open, and the selection stays put if its node survived.
func Keep(g *graph.Graph, st *State) {
	for id := range st.Expanded {
		if g.ByID[id] == nil {
			delete(st.Expanded, id)
		}
	}
	if g.ByID[st.Selected] == nil && g.Root != nil {
		st.Selected = g.Root.ID
	}
}
