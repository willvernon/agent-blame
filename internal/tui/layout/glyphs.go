package layout

import (
	"agent-blame/internal/graph"
	"agent-blame/internal/model"
)

// Glyphs is the set of characters the gutter and rails are drawn with.
type Glyphs struct {
	Main, PR, Prompt, Read, Edit, Run, Commit, Merge, Other string
	V, VDash, Tee, H, Open, Close, Rail, Cursor             string
	Expanded, Collapsed, Cross                              string
}

// Unicode glyphs. Every one must measure one cell wide.
var Unicode = Glyphs{
	Main: "◎", PR: "⬢", Prompt: "◆", Read: "○", Edit: "●", Run: "▸", Commit: "■", Merge: "■", Other: "·",
	V: "│", VDash: "┆", Tee: "├", H: "─", Open: "╮", Close: "╯", Rail: "│", Cursor: "▌",
	Expanded: "▾", Collapsed: "▸", Cross: "┼",
}

// ASCII glyphs, for fonts or terminals where the Unicode set misaligns.
var ASCII = Glyphs{
	Main: "@", PR: "#", Prompt: "*", Read: "o", Edit: "+", Run: ">", Commit: "=", Merge: "=", Other: ".",
	V: "|", VDash: ":", Tee: "+", H: "-", Open: `\`, Close: "/", Rail: "|", Cursor: ">",
	Expanded: "v", Collapsed: ">", Cross: "+",
}

// For returns the node glyph for a kind.
func (g Glyphs) For(k model.Kind) string {
	switch k {
	case graph.KindMain:
		return g.Main
	case graph.KindPR:
		return g.PR
	case graph.KindMerge:
		return g.Merge
	case model.KindPrompt:
		return g.Prompt
	case model.KindRead:
		return g.Read
	case model.KindEdit:
		return g.Edit
	case model.KindRun:
		return g.Run
	case model.KindCommit:
		return g.Commit
	}
	return g.Other
}
