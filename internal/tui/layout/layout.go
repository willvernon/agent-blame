// Package layout turns a graph and view state into lines of styled spans.
// It imports neither Bubble Tea nor Lip Gloss: spans carry style tokens,
// and the view maps tokens to colors. That keeps every frame testable as
// plain text.
package layout

import (
	"fmt"
	"strings"
	"time"

	"agent-blame/internal/graph"
	"agent-blame/internal/model"
)

// Style is a token the view maps to a color.
type Style string

// Style tokens.
const (
	StyleInk    Style = "ink"
	StyleMuted  Style = "muted"
	StyleTime   Style = "time"
	StyleTrunk  Style = "lane.trunk"
	StyleBranch Style = "lane.branch"
	StyleWork   Style = "lane.work"
	StyleHot    Style = "lane.hot"
	StyleAdd    Style = "add"
	StyleDel    Style = "del"
	StyleCtx    Style = "ctx"
	StyleOK     Style = "ok"
	StyleFail   Style = "fail"
	StyleLive   Style = "live"
	StyleBar    Style = "bar"
	StyleBarInk Style = "bar.ink"
)

// StyleNode is the color of a kind's node glyph.
func StyleNode(k model.Kind) Style { return Style("node." + string(k)) }

// StyleText is the color of a kind's label text.
func StyleText(k model.Kind) Style { return Style("text." + string(k)) }

// StyleHalo is the selected node glyph, on a soft disc of its own color.
func StyleHalo(k model.Kind) Style { return Style("halo." + string(k)) }

// StyleChevron is the open or closed marker before a row's kind.
const StyleChevron Style = "chevron"

// StyleCursor is the color of the selection bar for a kind.
func StyleCursor(k model.Kind) Style { return Style("cursor." + string(k)) }

// StyleRail is the color of a scope rail for a kind.
func StyleRail(k model.Kind) Style { return Style("rail." + string(k)) }

// Span is a run of text in one style.
type Span struct {
	Text  string
	Style Style
}

// Line is one screen line.
type Line struct {
	Spans    []Span
	TextFrom int        // index of the first span after the gutter
	Selected bool       // the selected node's row
	Tint     model.Kind // non-empty inside the innermost open scope
	Faded    bool       // outside the innermost open scope
	Card     bool       // a detail line, drawn on a lighter inset card
	CardFrom int        // index of the first span on the card
}

// Text returns the line without styles.
func (l Line) Text() string {
	var b strings.Builder
	for _, s := range l.Spans {
		b.WriteString(s.Text)
	}
	return b.String()
}

// State is everything about the view that is not the graph.
type State struct {
	Expanded map[string]bool
	Selected string
	Top      int // first visible row
	Width    int
	Height   int // total lines, including header and footer
	Now      time.Time
	Glyphs   Glyphs
	Measure  Measure
	Repo     string // shown in the header
	Live     bool   // an agent is working right now
	Status   string // a transient message for the footer
	ShowTime bool
}

func (st *State) measure() Measure {
	if st.Measure == nil {
		return runeCount
	}
	return st.Measure
}

const maxLanes = 3
const chevW = 2 // the open or closed marker and a space
const railCols = 3

func gutterWidth() int { return maxLanes * 2 }

func (st *State) textWidth() int {
	w := st.Width - 1 - gutterWidth() - 1 - railCols - 1 // cursor, gutter, gap, rails, gap
	if w < 20 {
		w = 20
	}
	return w
}

func (st *State) showTime() bool { return st.Width >= 70 }
func (st *State) showStat() bool { return st.Width >= 55 }

const timeW = 6

// subjectWidth is the room left for a row's subject before its tail.
func subjectWidth(st *State) int {
	w := st.textWidth() - chevW - labelW - 1
	if st.showTime() {
		w -= timeW + 1
	}
	return w
}

// BodyHeight is the number of tree lines that fit between header and footer.
func (st *State) BodyHeight() int {
	h := st.Height - 2
	if h < 1 {
		h = 1
	}
	return h
}

// Frame is one rendered screen.
type Frame struct {
	Header Line
	Body   []Line
	Footer Line
	Rows   []Row
	Sel    int // index of the selected row in Rows, -1 if none
}

// Lines returns header, body and footer in order.
func (f Frame) Lines() []Line {
	out := make([]Line, 0, len(f.Body)+2)
	out = append(out, f.Header)
	out = append(out, f.Body...)
	return append(out, f.Footer)
}

// Build lays out one frame.
func Build(g *graph.Graph, st *State) Frame {
	if st.Glyphs.V == "" {
		st.Glyphs = Unicode
	}
	if st.Expanded == nil {
		st.Expanded = map[string]bool{}
	}
	rows := Flatten(g, st)
	f := Frame{Rows: rows, Sel: -1}
	index := map[string]int{}
	for i, r := range rows {
		if r.IsNode() {
			index[r.Node.ID] = i
			if r.Node.ID == st.Selected {
				f.Sel = i
			}
		}
	}
	gut := gutter(g, rows, index, st)
	scope := scopeOf(g, rows, index, st)

	body := st.BodyHeight()
	top := st.Top
	if top > len(rows)-body {
		top = len(rows) - body
	}
	if top < 0 {
		top = 0
	}
	for i := top; i < len(rows) && i < top+body; i++ {
		f.Body = append(f.Body, line(rows[i], i, i == f.Sel, gut[i], scope, st))
	}
	f.Header = header(g, st)
	f.Footer = footer(rows, f.Sel, scope, st)
	return f
}

// cell is one gutter column.
type cell struct {
	text  string
	style Style
}

type span struct {
	start, end int
	dashed     bool
	style      Style
}

// gutter draws lanes, nodes and connectors for every row.
func gutter(g *graph.Graph, rows []Row, index map[string]int, st *State) [][]cell {
	gl := st.Glyphs
	n := len(rows)
	grid := make([][]cell, n)
	for i := range grid {
		grid[i] = make([]cell, gutterWidth())
		for j := range grid[i] {
			grid[i][j] = cell{" ", StyleMuted}
		}
	}
	if n == 0 {
		return grid
	}
	lanes := make([][]span, maxLanes)
	addSpan := func(lane int, s span) {
		if lane < maxLanes && s.end > s.start {
			lanes[lane] = append(lanes[lane], s)
		}
	}
	lastRow := func(nd *graph.Node) int {
		// the last visible row the node owns, its own detail lines included
		last := index[nd.ID]
		for i := last + 1; i < n; i++ {
			if !nd.Contains(rows[i].Node) {
				break
			}
			last = i
		}
		return last
	}

	// main: solid through the last row that sits on it or hangs off it,
	// then dashed while a branch is still open.
	lastSolid := 0
	for i, r := range rows {
		if r.IsNode() && (r.Node.Lane == 0 || r.Node.Kind == graph.KindPR) {
			lastSolid = i
		}
	}
	addSpan(0, span{start: 0, end: lastSolid, style: StyleTrunk})
	if g.AnyOpen && lastSolid < n-1 {
		addSpan(0, span{start: lastSolid, end: n, dashed: true, style: StyleTrunk})
	}

	type closer struct{ row, lane int }
	var closers []closer
	var openers []closer
	for i, r := range rows {
		if !r.IsNode() || !st.Expanded[r.Node.ID] {
			continue
		}
		nd := r.Node
		switch nd.Kind {
		case graph.KindPR:
			end := lastRow(nd)
			lastLane1 := i
			for j := i + 1; j <= end; j++ {
				if rows[j].IsNode() && (rows[j].Node.Lane == 1 || rows[j].Node.Kind == graph.KindMerge) {
					lastLane1 = j
				}
			}
			addSpan(1, span{start: i, end: lastLane1, style: StyleBranch})
			if lastLane1 > i && rows[lastLane1].Node.Kind == graph.KindMerge {
				closers = append(closers, closer{lastLane1, 0})
			}
		case model.KindPrompt:
			work := nd.Lane + 1
			if work >= maxLanes {
				continue
			}
			end, lastWork := lastRow(nd), -1
			for j := i + 1; j <= end; j++ {
				if rows[j].IsNode() && rows[j].Node.Parent == nd && rows[j].Node.Lane == work {
					lastWork = j
				}
			}
			if lastWork < 0 {
				continue
			}
			// The last commit after the last event closes the lane on its
			// own row. Commits between events are passed by.
			stop := lastWork
			for j := lastWork + 1; j <= end; j++ {
				if rows[j].IsNode() && rows[j].Node.Parent == nd && rows[j].Node.Kind == model.KindCommit {
					stop = j
				}
			}
			if stop != lastWork {
				closers = append(closers, closer{stop, nd.Lane})
			}
			addSpan(work, span{start: i, end: stop, style: StyleWork})
			openers = append(openers, closer{i, nd.Lane})
		}
	}

	// verticals
	for lane, ss := range lanes {
		for _, s := range ss {
			for row := s.start + 1; row < n && (s.dashed || row < s.end); row++ {
				ch := gl.V
				if s.dashed {
					ch = gl.VDash
				}
				grid[row][lane*2] = cell{ch, s.style}
			}
		}
	}
	// hot path: from the selected event back up to its prompt
	if si, ok := index[st.Selected]; ok {
		sel := rows[si].Node
		if sel.IsEvent() && sel.Parent != nil {
			if pi, ok := index[sel.Parent.ID]; ok && sel.Lane < maxLanes {
				for row := pi + 1; row < si; row++ {
					if grid[row][sel.Lane*2].text == gl.V {
						grid[row][sel.Lane*2].style = StyleHot
					}
				}
			}
		}
	}
	// connectors
	for _, o := range openers {
		if o.lane*2+2 < gutterWidth() {
			grid[o.row][o.lane*2+1] = cell{gl.H, StyleWork}
			grid[o.row][o.lane*2+2] = cell{gl.Open, StyleWork}
		}
	}
	for _, c := range closers {
		if c.lane*2+2 < gutterWidth() {
			style := StyleWork
			if c.lane == 0 {
				style = StyleBranch
			}
			grid[c.row][c.lane*2+1] = cell{gl.H, style}
			grid[c.row][c.lane*2+2] = cell{gl.Close, style}
		}
	}
	for i, r := range rows {
		if r.IsNode() && r.Node.Kind == graph.KindPR {
			grid[i][0] = cell{gl.Tee, StyleTrunk}
			grid[i][1] = cell{gl.H, StyleBranch}
		}
	}
	// nodes last, so they sit on top of everything
	for i, r := range rows {
		if !r.IsNode() || r.Node.Lane >= maxLanes {
			continue
		}
		grid[i][r.Node.Lane*2] = cell{gl.For(r.Node.Kind), StyleNode(nodeColorKind(r.Node))}
	}
	// The selected node gets a halo and a line in its color across to its row.
	if si, ok := index[st.Selected]; ok && rows[si].Node.Lane < maxLanes {
		nd := rows[si].Node
		k := nodeColorKind(nd)
		grid[si][nd.Lane*2] = cell{gl.For(nd.Kind), StyleHalo(k)}
		for c := nd.Lane*2 + 1; c < gutterWidth(); c++ {
			switch grid[si][c].text {
			case " ":
				grid[si][c] = cell{gl.H, StyleCursor(k)}
			case gl.V, gl.VDash:
				grid[si][c] = cell{gl.Cross, StyleCursor(k)}
			}
		}
	}
	return grid
}

func nodeColorKind(n *graph.Node) model.Kind {
	if n.Kind == graph.KindMerge {
		return graph.KindMain
	}
	return n.Kind
}

// scope is the chain of open items that contain the selection.
type scope struct {
	chain []*graph.Node
	from  []int // first row of each chain entry
	to    []int // last row of each chain entry
}

func (s scope) inner() *graph.Node {
	if len(s.chain) == 0 {
		return nil
	}
	return s.chain[len(s.chain)-1]
}

func scopeOf(g *graph.Graph, rows []Row, index map[string]int, st *State) scope {
	var sc scope
	si, ok := index[st.Selected]
	if !ok {
		return sc
	}
	var chain []*graph.Node
	for nd := rows[si].Node; nd != nil; nd = nd.Parent {
		if nd.Kind != graph.KindMain && st.Expanded[nd.ID] && Expandable(nd) {
			chain = append([]*graph.Node{nd}, chain...)
		}
	}
	if len(chain) > railCols {
		chain = chain[len(chain)-railCols:]
	}
	for _, nd := range chain {
		from := index[nd.ID]
		to := from
		for i := from + 1; i < len(rows); i++ {
			if !nd.Contains(rows[i].Node) {
				break
			}
			to = i
		}
		if to == from {
			continue // nothing shown under it
		}
		sc.chain = append(sc.chain, nd)
		sc.from = append(sc.from, from)
		sc.to = append(sc.to, to)
	}
	return sc
}

func line(r Row, i int, selected bool, gut []cell, sc scope, st *State) Line {
	m := st.measure()
	var l Line
	// The cursor column: a bar in the selected item's color at the far left.
	if selected {
		l.Spans = append(l.Spans, Span{st.Glyphs.Cursor, StyleCursor(nodeColorKind(r.Node))})
	} else {
		l.Spans = append(l.Spans, Span{" ", StyleMuted})
	}
	for _, c := range gut {
		l.Spans = append(l.Spans, Span{c.text, c.style})
	}
	if selected {
		l.Spans = append(l.Spans, Span{st.Glyphs.H, StyleCursor(nodeColorKind(r.Node))})
	} else {
		l.Spans = append(l.Spans, Span{" ", StyleMuted})
	}
	l.TextFrom = len(l.Spans)
	for k := 0; k < railCols; k++ {
		if k < len(sc.chain) && i >= sc.from[k] && i <= sc.to[k] {
			l.Spans = append(l.Spans, Span{st.Glyphs.Rail, StyleRail(sc.chain[k].Kind)})
		} else {
			l.Spans = append(l.Spans, Span{" ", StyleMuted})
		}
	}
	l.Spans = append(l.Spans, Span{" ", StyleMuted})
	if in := sc.inner(); in != nil {
		k := len(sc.chain) - 1
		if i >= sc.from[k] && i <= sc.to[k] {
			l.Tint = in.Kind
		} else {
			l.Faded = true
		}
	}
	l.Selected = selected

	textW := st.textWidth()
	if !r.IsNode() {
		l.Spans = append(l.Spans, Span{strings.Repeat(" ", chevW), StyleMuted})
		if r.Node.IsEvent() {
			l.Card, l.CardFrom = true, len(l.Spans)
		}
		l.Spans = append(l.Spans,
			Span{pad(r.Label, labelW, m), StyleMuted}, Span{" ", StyleMuted},
			Span{truncate(r.Text, textW-chevW-labelW-1, m), r.Style})
		return l
	}

	nd := r.Node
	tail, tailStyle := rowTail(nd, st)
	subjW := subjectWidth(st)
	if tail != "" {
		subjW -= m(tail) + 2
	}
	subject := truncate(flatText(RowTitle(nd)), subjW, m)
	subjStyle := StyleInk
	switch {
	case nd.Kind == model.KindPrompt:
		subjStyle = StyleText(model.KindPrompt)
	case nd.Kind == graph.KindPR:
		subjStyle = StyleText(graph.KindPR)
	case nd.Event != nil && nd.Event.Outside:
		subjStyle = StyleMuted
	case nd.Status == graph.StatusOrphan:
		subjStyle = StyleMuted
	}
	chev := strings.Repeat(" ", chevW)
	if Expandable(nd) {
		if st.Expanded[nd.ID] {
			chev = st.Glyphs.Expanded + " "
		} else {
			chev = st.Glyphs.Collapsed + " "
		}
	}
	l.Spans = append(l.Spans, Span{chev, StyleChevron},
		Span{pad(kindLabel(nd), labelW, m), StyleText(nodeColorKind(nd))}, Span{" ", StyleMuted},
		Span{pad(subject, subjW, m), subjStyle})
	if tail != "" {
		l.Spans = append(l.Spans, Span{"  ", StyleMuted}, Span{tail, tailStyle})
	}
	if st.showTime() {
		ts := ""
		if !nd.Time.IsZero() {
			ts = clock(nd.Time, st.Now)
		}
		l.Spans = append(l.Spans, Span{" ", StyleMuted}, Span{fmt.Sprintf("%*s", timeW, ts), StyleTime})
	}
	return l
}

// RowTitle is what a node's row shows. A run shows the agent's own
// one-line description of the command when it gave one, since agent
// commands are often long chains; the command itself is in the detail.
func RowTitle(n *graph.Node) string {
	if n.Kind == model.KindRun && n.Event != nil && n.Event.Note.Change != "" {
		return n.Event.Note.Change
	}
	return n.Title
}

func kindLabel(n *graph.Node) string {
	switch n.Kind {
	case graph.KindMain:
		return "main"
	case graph.KindPR:
		return "pr"
	case graph.KindMerge:
		return "merge"
	}
	if n.Kind == model.KindOther && n.Event != nil && n.Event.Tool != "" {
		return strings.ToLower(n.Event.Tool)
	}
	return string(n.Kind)
}

func rowTail(n *graph.Node, st *State) (string, Style) {
	if !st.showStat() {
		if n.Event != nil && n.Event.InFlight {
			return "running", StyleLive
		}
		return "", StyleMuted
	}
	switch n.Kind {
	case graph.KindPR:
		return n.Status, StyleMuted
	case model.KindPrompt:
		return n.Summary(), StyleMuted
	}
	e := n.Event
	if e == nil {
		return "", StyleMuted
	}
	if e.InFlight {
		return "running", StyleLive
	}
	switch e.Kind {
	case model.KindRun:
		if e.OK != nil && !*e.OK {
			return "failed", StyleFail
		}
		if e.OK != nil && e.RunClass == "test" {
			return "passed", StyleOK
		}
	case model.KindEdit:
		if e.OK != nil && !*e.OK {
			return "failed", StyleFail
		}
		if e.Stat != nil {
			s := fmt.Sprintf("+%d -%d", e.Stat.Add, e.Stat.Del)
			if n.Status == graph.StatusUncommitted {
				s += " *"
			}
			return s, StyleMuted
		}
	}
	return "", StyleMuted
}

func clock(t, now time.Time) string {
	if now.IsZero() {
		now = time.Now()
	}
	t = t.In(now.Location())
	y1, m1, d1 := t.Date()
	y2, m2, d2 := now.Date()
	if y1 == y2 && m1 == m2 && d1 == d2 {
		return t.Format("15:04")
	}
	return t.Format("Jan 02")
}

func header(g *graph.Graph, st *State) Line {
	m := st.measure()
	left := " agent-blame  " + st.Repo
	if g != nil {
		left += "  " + g.Default
	}
	right := ""
	style := StyleBarInk
	if st.Live {
		right, style = "● working ", StyleLive
	}
	gap := st.Width - m(left) - m(right)
	if gap < 1 {
		left = truncate(left, st.Width-m(right)-1, m)
		gap = 1
	}
	return Line{Spans: []Span{{left, StyleBarInk}, {strings.Repeat(" ", gap), StyleBar}, {right, style}}}
}

func footer(rows []Row, sel int, sc scope, st *State) Line {
	m := st.measure()
	left := " j/k move  l/h open/close  1 2 3 fold  o diff  q quit"
	if st.Status != "" {
		left = " " + st.Status
	}
	nodes, pos := 0, 0
	for i, r := range rows {
		if r.IsNode() {
			nodes++
			if i == sel {
				pos = nodes
			}
		}
	}
	right := fmt.Sprintf("%d/%d ", pos, nodes)
	if in := sc.inner(); in != nil {
		right = "in " + kindLabel(in) + "  " + right
	}
	gap := st.Width - m(left) - m(right)
	if gap < 1 {
		left = truncate(left, st.Width-m(right)-1, m)
		gap = 1
	}
	return Line{Spans: []Span{{left, StyleBar}, {strings.Repeat(" ", gap), StyleBar}, {right, StyleBar}}}
}
