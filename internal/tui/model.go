// Package tui is the interactive tree, built on Bubble Tea.
package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"agent-blame/internal/graph"
	"agent-blame/internal/load"
	"agent-blame/internal/model"
	"agent-blame/internal/sources/claudecode"
	"agent-blame/internal/sources/git"
	"agent-blame/internal/store"
	"agent-blame/internal/tui/layout"
	"agent-blame/internal/viewer"
)

// Options configure the TUI.
type Options struct {
	Repo      *git.Repo
	Loader    *load.Loader
	PaperBG   bool
	Glyphs    layout.Glyphs
	ShowOther bool
	// Open selects and opens the first item whose title contains this
	// text when the tree first loads.
	Open string
	// OpenMatching implements Open. It lives in the cli package.
	OpenMatching func(g *graph.Graph, st *layout.State, text string) bool
}

// Model is the Bubble Tea model.
type Model struct {
	opt   Options
	theme *Theme
	g     *graph.Graph
	st    layout.State

	loaded, loading, pending bool
	err                      error

	stamps    map[string]stamp
	sessions  map[string]time.Time // session -> transcript mtime
	lastFind  time.Time
	lastFull  time.Time
	knownPath map[string]bool
	statusAt  time.Time
}

type stamp struct {
	size int64
	mod  time.Time
}

type loadedMsg struct {
	g      *graph.Graph
	res    load.Result
	err    error
	stamps map[string]stamp // taken before loading, so writes during a load are not missed
}

type tickMsg time.Time

type viewerDoneMsg struct {
	name string
	err  error
}

const pollEvery = 500 * time.Millisecond

// New returns a model ready to run.
func New(opt Options) *Model {
	if opt.Glyphs.V == "" {
		opt.Glyphs = layout.Unicode
	}
	return &Model{
		opt:   opt,
		theme: NewTheme(opt.PaperBG),
		st: layout.State{
			Measure: lipgloss.Width, Glyphs: opt.Glyphs,
			Repo: claudecode.ShortenHome(opt.Repo.Root), Expanded: map[string]bool{},
		},
		stamps:    map[string]stamp{},
		sessions:  map[string]time.Time{},
		knownPath: map[string]bool{},
	}
}

// Run starts the program and blocks until the user quits.
func Run(opt Options) error {
	_, err := tea.NewProgram(New(opt)).Run()
	return err
}

func (m *Model) Init() tea.Cmd { return tea.Batch(m.reload(), tick()) }

func tick() tea.Cmd {
	return tea.Tick(pollEvery, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m *Model) reload() tea.Cmd {
	if m.loading {
		m.pending = true
		return nil
	}
	m.loading = true
	loader, repo, showOther := m.opt.Loader, m.opt.Repo, m.opt.ShowOther
	var known []string
	for p := range m.knownPath {
		known = append(known, p)
	}
	return func() tea.Msg {
		stamps := watchStamps(repo, known)
		res, err := loader.Load(true)
		if err != nil {
			return loadedMsg{err: err}
		}
		for _, t := range res.Transcripts {
			if _, ok := stamps[t.Path]; !ok {
				stamps[t.Path] = stamp{t.Size, t.ModTime} // stat taken before it was parsed
			}
		}
		return loadedMsg{g: graph.Build(repo, res.Events, graph.Options{ShowOther: showOther}), res: res, stamps: stamps}
	}
}

// watchStamps stats everything whose change should trigger a reload:
// known transcripts, git's HEAD and reflog, and the store's hook files.
func watchStamps(repo *git.Repo, transcripts []string) map[string]stamp {
	stamps := map[string]stamp{}
	for _, p := range transcripts {
		stamps[p] = statOf(p)
	}
	for _, p := range []string{
		filepath.Join(repo.GitDir, "HEAD"),
		filepath.Join(repo.CommonDir, "logs", "HEAD"),
		filepath.Join(repo.CommonDir, "packed-refs"),
	} {
		stamps[p] = statOf(p)
	}
	events := filepath.Join(store.Path(repo.CommonDir), "events")
	stamps[events] = statOf(events) // changes when a new hook file appears
	if entries, err := os.ReadDir(events); err == nil {
		for _, e := range entries {
			p := filepath.Join(events, e.Name())
			stamps[p] = statOf(p)
		}
	}
	return stamps
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.st.Width, m.st.Height = msg.Width, msg.Height
		m.follow()
	case loadedMsg:
		return m, m.loaded_(msg)
	case tickMsg:
		return m, m.poll(time.Time(msg))
	case viewerDoneMsg:
		if msg.err != nil {
			m.flash(msg.name + ": " + msg.err.Error())
		}
	case tea.KeyPressMsg:
		return m, m.key(msg.String())
	}
	return m, nil
}

func (m *Model) loaded_(msg loadedMsg) tea.Cmd {
	m.loading = false
	if msg.err != nil {
		m.err = msg.err
		m.flash("load failed: " + msg.err.Error())
	} else {
		first := !m.loaded
		tail := !first && layout.AtTail(m.g, &m.st)
		m.g, m.loaded, m.err = msg.g, true, nil
		if tail {
			layout.Keep(m.g, &m.st)
			layout.FollowLatest(m.g, &m.st)
		} else if first {
			layout.Defaults(m.g, &m.st)
			if m.opt.Open != "" && m.opt.OpenMatching != nil && !m.opt.OpenMatching(m.g, &m.st, m.opt.Open) {
				m.flash("nothing matches " + m.opt.Open)
			}
		} else {
			layout.Keep(m.g, &m.st)
		}
		m.stamps = msg.stamps
		for _, t := range msg.res.Transcripts {
			m.sessions[t.Session] = t.ModTime
			m.knownPath[t.Path] = true
		}
		m.updateLive()
		m.follow()
	}
	m.lastFull = time.Now()
	if m.pending {
		m.pending = false
		return m.reload()
	}
	return nil
}

func statOf(p string) stamp {
	fi, err := os.Stat(p)
	if err != nil {
		return stamp{}
	}
	return stamp{fi.Size(), fi.ModTime()}
}

func (m *Model) poll(now time.Time) tea.Cmd {
	cmds := []tea.Cmd{tick()}
	if m.statusAt.Before(now.Add(-4*time.Second)) && m.st.Status != "" {
		m.st.Status = ""
	}
	changed := false
	for p, old := range m.stamps {
		if statOf(p) != old {
			changed = true
			break
		}
	}
	if !changed && now.Sub(m.lastFind) > 3*time.Second {
		m.lastFind = now
		if ts, err := m.opt.Loader.Finder.Find(); err == nil {
			for _, t := range ts {
				if !m.knownPath[t.Path] {
					changed = true
				}
			}
		}
	}
	// The working tree can change without any of the above, so refresh
	// now and then regardless.
	if changed || now.Sub(m.lastFull) > 15*time.Second {
		cmds = append(cmds, m.reload())
	}
	m.updateLive()
	return tea.Batch(cmds...)
}

// updateLive marks the view as working when a tool call is in flight in a
// transcript that changed in the last half minute.
func (m *Model) updateLive() {
	m.st.Live = false
	if m.g == nil {
		return
	}
	cutoff := time.Now().Add(-30 * time.Second)
	for _, n := range m.g.ByID {
		if n.Event != nil && n.Event.InFlight && m.sessions[n.Event.Session].After(cutoff) {
			m.st.Live = true
			return
		}
	}
}

func (m *Model) flash(s string) {
	m.st.Status = s
	m.statusAt = time.Now()
}

func (m *Model) follow() {
	if m.g == nil || m.st.Height == 0 {
		return
	}
	layout.Follow(layout.Flatten(m.g, &m.st), &m.st)
}

func (m *Model) key(k string) tea.Cmd {
	if k == "q" || k == "ctrl+c" || k == "esc" {
		return tea.Quit
	}
	if m.g == nil {
		return nil
	}
	rows := layout.Flatten(m.g, &m.st)
	page := m.st.BodyHeight() - 2
	switch k {
	case "j", "down":
		layout.Move(rows, &m.st, 1)
	case "k", "up":
		layout.Move(rows, &m.st, -1)
	case "g", "home":
		layout.Move(rows, &m.st, -len(rows))
	case "G", "shift+g", "end":
		layout.Move(rows, &m.st, len(rows))
	case "ctrl+d", "pgdown", "space":
		layout.Move(rows, &m.st, page/2)
	case "ctrl+u", "pgup", "b":
		layout.Move(rows, &m.st, -page/2)
	case "l", "right":
		layout.Open(m.g, &m.st)
	case "h", "left":
		layout.Close(m.g, &m.st)
	case "enter":
		layout.Toggle(m.g, &m.st)
	case "1", "2", "3":
		layout.Fold(m.g, &m.st, int(k[0]-'0'))
	case "o", "O", "shift+o":
		return m.openDiff(k != "o")
	case ".":
		m.opt.ShowOther = !m.opt.ShowOther
		if m.opt.ShowOther {
			m.flash("showing searches, fetches and other tools")
		} else {
			m.flash("hiding searches, fetches and other tools")
		}
		return m.reload()
	case "r":
		m.flash("reloading")
		return m.reload()
	}
	m.follow()
	return nil
}

func (m *Model) openDiff(whole bool) tea.Cmd {
	n := m.g.ByID[m.st.Selected]
	if n == nil || n.Event == nil || n.Event.Kind != model.KindEdit {
		m.flash("select an edit to open its diff")
		return nil
	}
	var patch string
	var err error
	if whole {
		patch, err = viewer.ForWholeFile(m.opt.Repo, n.Event)
	} else {
		patch, err = viewer.ForChange(n.Event)
	}
	if err != nil {
		m.flash(err.Error())
		return nil
	}
	v, err := viewer.Find()
	if err != nil {
		m.flash(err.Error())
		return nil
	}
	cmd, cleanup, err := v.Command(viewer.PatchRequest{Path: n.Event.Subject, Patch: patch, Dir: m.opt.Repo.Root})
	if err != nil {
		m.flash(err.Error())
		return nil
	}
	name := v.Name()
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		cleanup()
		return viewerDoneMsg{name: name, err: err}
	})
}

func (m *Model) View() tea.View {
	var content string
	switch {
	case m.st.Width == 0:
		content = ""
	case !m.loaded && m.err != nil:
		content = fmt.Sprintf("\n  agent-blame could not load this repository:\n  %v\n\n  press q to quit", m.err)
	case !m.loaded:
		content = "\n  reading sessions and history..."
	default:
		m.st.Now = time.Now()
		f := layout.Build(m.g, &m.st)
		content = m.theme.Render(f.Lines(), m.st.Width)
	}
	v := tea.NewView(content)
	v.AltScreen = true
	v.WindowTitle = "agent-blame"
	v.BackgroundColor = m.theme.Paper()
	v.ForegroundColor = m.theme.Ink()
	return v
}
