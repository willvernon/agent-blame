package tui

import (
	"fmt"
	"image/color"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"

	"agent-blame/internal/graph"
	"agent-blame/internal/model"
	"agent-blame/internal/tui/layout"
)

// Paper theme: warm paper, dark warm-gray ink, low-contrast rules.
const (
	paper     = "#faf8f3"
	ink       = "#45423b"
	bar       = "#f5f2ea"
	selection = "#e6dfcd"
	inkStrong = "#2f2c27"
	cardBG    = "#fdfcf9"
	muted     = "#a39e90"
	clockGray = "#b3ae9f"
	hot       = "#6f6a5e"
	trunk     = "#a8a290"
	branch    = "#c4bead"
	work      = "#ddd8ca"
	added     = "#5c8a5e"
	removed   = "#b0584f"
	context   = "#8f8a7d"
)

type kindColor struct{ node, text string }

var kinds = map[model.Kind]kindColor{
	graph.KindMain:   {"#2f2c27", "#2f2c27"},
	graph.KindPR:     {"#4a9a8c", "#3d7f73"},
	model.KindPrompt: {"#c67a4a", "#a8623d"},
	model.KindRead:   {"#5b8db8", "#44709a"},
	model.KindEdit:   {"#8a6fb5", "#72589e"},
	model.KindRun:    {"#cfa13d", "#96701c"},
	model.KindCommit: {"#7d786b", "#5f5b51"},
	model.KindOther:  {"#a39e90", "#8f8a7d"},
}

// Theme maps style tokens to Lip Gloss styles.
type Theme struct {
	styles map[layout.Style]lipgloss.Style
	tints  map[model.Kind]color.Color
	sel    color.Color
	card   color.Color
	fade   lipgloss.Style
	paper  bool
}

// NewTheme builds the paper theme. With paperBG false, the terminal's own
// background is kept and only foreground colors are set.
func NewTheme(paperBG bool) *Theme {
	t := &Theme{styles: map[layout.Style]lipgloss.Style{}, tints: map[model.Kind]color.Color{}, paper: paperBG}
	fg := func(hex string) lipgloss.Style { return lipgloss.NewStyle().Foreground(lipgloss.Color(hex)) }
	t.styles[layout.StyleInk] = fg(ink)
	t.styles[layout.StyleMuted] = fg(muted)
	t.styles[layout.StyleTime] = fg(clockGray)
	t.styles[layout.StyleTrunk] = fg(trunk)
	t.styles[layout.StyleBranch] = fg(branch)
	t.styles[layout.StyleWork] = fg(work)
	t.styles[layout.StyleHot] = fg(hot)
	t.styles[layout.StyleAdd] = fg(added)
	t.styles[layout.StyleDel] = fg(removed)
	t.styles[layout.StyleCtx] = fg(context)
	t.styles[layout.StyleOK] = fg(added)
	t.styles[layout.StyleFail] = fg(removed)
	t.styles[layout.StyleLive] = fg(kinds[model.KindPrompt].node)
	t.styles[layout.StyleBar] = fg(muted).Background(lipgloss.Color(bar))
	t.styles[layout.StyleBarInk] = fg(ink).Background(lipgloss.Color(bar))
	for k, c := range kinds {
		t.styles[layout.StyleNode(k)] = fg(c.node)
		t.styles[layout.StyleText(k)] = fg(c.text)
		t.styles[layout.StyleRail(k)] = fg(c.node)
		t.styles[layout.StyleCursor(k)] = fg(c.node)
		t.styles[layout.StyleHalo(k)] = fg(c.node).Background(lipgloss.Color(blend(paper, c.node, 0.30)))
		t.tints[k] = lipgloss.Color(blend(paper, c.node, 0.11))
	}
	t.sel = lipgloss.Color(selection)
	t.card = lipgloss.Color(cardBG)
	t.styles[layout.StyleChevron] = fg(clockGray)
	t.fade = fg(blend(paper, muted, 0.75))
	return t
}

// Paper is the background color the view asks the terminal to use, or nil.
func (t *Theme) Paper() color.Color {
	if !t.paper {
		return nil
	}
	return lipgloss.Color(paper)
}

// Ink is the default foreground, or nil.
func (t *Theme) Ink() color.Color {
	if !t.paper {
		return nil
	}
	return lipgloss.Color(ink)
}

// Render turns layout lines into styled terminal text, each padded to width.
func (t *Theme) Render(lines []layout.Line, width int) string {
	var b strings.Builder
	for i, l := range lines {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(t.line(l, width))
	}
	return b.String()
}

func (t *Theme) line(l layout.Line, width int) string {
	var bg color.Color
	if l.Tint != "" {
		bg = t.tints[l.Tint]
	}
	var b strings.Builder
	used := 0
	for i, s := range l.Spans {
		st, ok := t.styles[s.Style]
		if !ok {
			st = t.styles[layout.StyleInk]
		}
		if l.Faded && i >= l.TextFrom && !strings.HasPrefix(string(s.Style), "rail.") {
			st = t.fade
		}
		span := bg
		if l.Card && i >= l.CardFrom {
			span = t.card
		}
		if l.Selected && i >= l.TextFrom {
			span = t.sel
			// Bold the row and darken plain text, so the selection reads at a glance.
			st = st.Bold(true)
			if s.Style == layout.StyleInk {
				st = st.Foreground(lipgloss.Color(inkStrong))
			}
		}
		if span != nil && !strings.HasPrefix(string(s.Style), "halo.") {
			st = st.Background(span)
		}
		b.WriteString(st.Render(s.Text))
		used += lipgloss.Width(s.Text)
	}
	if used < width {
		fill := lipgloss.NewStyle()
		if l.Selected {
			fill = fill.Background(t.sel)
		} else if l.Card {
			fill = fill.Background(t.card)
		} else if bg != nil {
			fill = fill.Background(bg)
		} else if len(l.Spans) > 0 && strings.HasPrefix(string(l.Spans[len(l.Spans)-1].Style), "bar") {
			fill = t.styles[layout.StyleBar]
		}
		b.WriteString(fill.Render(strings.Repeat(" ", width-used)))
	}
	return b.String()
}

// blend mixes color b into a by amount (0 to 1).
func blend(a, b string, amount float64) string {
	ar, ag, ab := hexRGB(a)
	br, bg, bb := hexRGB(b)
	mix := func(x, y int) int { return int(float64(x) + (float64(y)-float64(x))*amount + 0.5) }
	return fmt.Sprintf("#%02x%02x%02x", mix(ar, br), mix(ag, bg), mix(ab, bb))
}

func hexRGB(h string) (int, int, int) {
	h = strings.TrimPrefix(h, "#")
	v, _ := strconv.ParseUint(h, 16, 32)
	return int(v >> 16 & 0xff), int(v >> 8 & 0xff), int(v & 0xff)
}
