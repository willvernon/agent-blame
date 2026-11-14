package claudecode

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"agent-blame/internal/model"
)

// Resolver turns an absolute path into a repo-relative one.
type Resolver interface {
	Rel(p string) (rel string, ok bool)
}

const maxWhy = 600

// Mapper turns transcript records into events. Records must be added in
// file order. One mapper handles one transcript.
type Mapper struct {
	res Resolver

	events  []*model.Event
	byID    map[string]*model.Event
	current map[string]string // session -> current prompt id
	rooted  map[string]bool   // sessions that needed a placeholder prompt
	pending map[string]string // message id -> text not yet attached
	lastWhy map[string]string // message id -> why given to the last tool call
	inputs  map[string]toolInput
}

// NewMapper returns a mapper that resolves paths with res.
func NewMapper(res Resolver) *Mapper {
	return &Mapper{
		res:     res,
		byID:    map[string]*model.Event{},
		current: map[string]string{},
		rooted:  map[string]bool{},
		pending: map[string]string{},
		lastWhy: map[string]string{},
		inputs:  map[string]toolInput{},
	}
}

// Add processes one record.
func (m *Mapper) Add(rec Record) {
	if rec.Type != "user" && rec.Type != "assistant" {
		return
	}
	if rec.IsMeta || rec.IsSidechain || rec.Message == nil {
		return
	}
	ts, _ := time.Parse(time.RFC3339Nano, rec.Timestamp)
	session := rec.SessionID
	if rec.Type == "user" {
		m.addUser(rec, session, ts)
		return
	}
	m.addAssistant(rec, session, ts)
}

func (m *Mapper) addUser(rec Record, session string, ts time.Time) {
	blocks := rec.Message.Blocks()
	if rec.PromptSource != "" {
		// Prompts carried over from another machine can share one promptId,
		// so a repeated id falls back to the record's own uuid.
		pid := rec.PromptID
		if _, used := m.byID[model.PromptID(session, pid)]; pid == "" || used {
			pid = rec.UUID
		}
		id := model.PromptID(session, pid)
		if _, seen := m.byID[id]; !seen && pid != "" {
			var parts []string
			for _, b := range blocks {
				if b.Type == "text" && b.Text != "" {
					parts = append(parts, b.Text)
				}
			}
			if text := CleanPrompt(strings.Join(parts, "\n")); text != "" {
				m.emit(&model.Event{
					ID: id, TS: ts, Source: model.SourceClaudeCode, Session: session,
					Kind: model.KindPrompt, Branch: rec.GitBranch, Subject: text,
				})
				m.current[session] = id
			}
		}
	}

	var results []Block
	for _, b := range blocks {
		if b.Type == "tool_result" && b.ToolUseID != "" {
			results = append(results, b)
		}
	}
	for _, b := range results {
		ev := m.byID[model.ToolID(session, b.ToolUseID)]
		if ev == nil {
			continue
		}
		ev.InFlight = false
		ev.End = ts
		failed := b.IsError != nil && *b.IsError
		var tr toolResult
		structured := false
		if len(results) == 1 {
			tr, structured = parseResult(rec.ToolResult)
		}
		switch ev.Kind {
		case model.KindEdit:
			m.finishEdit(ev, tr, structured, failed)
		case model.KindRun:
			ok := !failed && !(structured && tr.Interrupted)
			ev.OK = model.Bool(ok)
			if structured {
				out := tr.Stdout
				if tr.Stderr != "" {
					out = strings.TrimSpace(out + "\n" + tr.Stderr)
				}
				ev.Output = capOutput(out, 1024)
			}
		}
	}
}

func (m *Mapper) finishEdit(ev *model.Event, tr toolResult, structured, failed bool) {
	if failed {
		ev.OK = model.Bool(false)
		return
	}
	var patch string
	var st model.Stat
	switch {
	case structured && len(tr.StructuredPatch) > 0:
		patch, st = patchFromHunks(tr.StructuredPatch)
	case structured && tr.Type == "create":
		patch, st = patchFromContent(tr.Content)
	default:
		if in, ok := m.inputs[ev.ID]; ok && in.Content != "" && ev.Tool == "Write" {
			patch, st = patchFromContent(in.Content)
		}
	}
	if structured && tr.Type == "create" {
		ev.Created = true
	}
	if patch != "" {
		ev.Hunk = patch
		ev.Stat = &st
	}
	delete(m.inputs, ev.ID)
}

func (m *Mapper) addAssistant(rec Record, session string, ts time.Time) {
	msgID := rec.Message.ID
	if msgID == "" {
		msgID = rec.UUID
	}
	for _, b := range rec.Message.Blocks() {
		switch b.Type {
		case "text":
			if t := strings.TrimSpace(b.Text); t != "" {
				if p := m.pending[msgID]; p != "" {
					m.pending[msgID] = p + "\n" + t
				} else {
					m.pending[msgID] = t
				}
			}
		case "tool_use":
			if b.ID == "" {
				continue
			}
			id := model.ToolID(session, b.ID)
			if _, seen := m.byID[id]; seen {
				continue
			}
			why := m.pending[msgID]
			if why == "" {
				why = m.lastWhy[msgID]
			}
			m.lastWhy[msgID] = why
			delete(m.pending, msgID)

			kind, subject, change, in := classify(b.Name, b.Input)
			ev := &model.Event{
				ID: id, TS: ts, Source: model.SourceClaudeCode, Session: session,
				Kind: kind, Tool: b.Name, Branch: rec.GitBranch, Subject: subject,
				InFlight: true,
			}
			if why != "" {
				ev.Note = model.Note{Why: clip(why, maxWhy), Origin: model.OriginTranscript}
			}
			if change != "" {
				ev.Note.Change = change
				if ev.Note.Origin == "" {
					ev.Note.Origin = model.OriginTranscript
				}
			}
			if kind == model.KindRead || kind == model.KindEdit {
				m.resolvePath(ev)
			}
			if kind == model.KindRun {
				ev.RunClass = RunClass(in.Command)
			}
			if kind == model.KindEdit && b.Name == "Write" {
				m.inputs[id] = toolInput{Content: in.Content}
			}
			ev.Parent = m.parentFor(session)
			m.emit(ev)
		}
	}
}

func (m *Mapper) resolvePath(ev *model.Event) {
	if m.res == nil || ev.Subject == "" {
		return
	}
	if rel, ok := m.res.Rel(ev.Subject); ok {
		ev.Subject = rel
		return
	}
	ev.Outside = true
	ev.Subject = ShortenHome(ev.Subject)
}

func (m *Mapper) parentFor(session string) string {
	if p := m.current[session]; p != "" {
		return p
	}
	m.rooted[session] = true
	return model.SessionRootID(session)
}

func (m *Mapper) emit(ev *model.Event) {
	ev.V = model.SchemaVersion
	m.events = append(m.events, ev)
	m.byID[ev.ID] = ev
}

// Events returns the events seen so far, in transcript order, with a
// placeholder prompt for tool calls that arrived before any prompt.
func (m *Mapper) Events() []model.Event {
	out := make([]model.Event, 0, len(m.events)+len(m.rooted))
	for session := range m.rooted {
		root := model.Event{
			V: model.SchemaVersion, ID: model.SessionRootID(session), Source: model.SourceClaudeCode,
			Session: session, Kind: model.KindPrompt, Subject: "(earlier prompt, not in this transcript)",
		}
		for _, ev := range m.events {
			if ev.Session == session {
				root.TS = ev.TS
				root.Branch = ev.Branch
				break
			}
		}
		out = append(out, root)
	}
	for _, ev := range m.events {
		out = append(out, *ev)
	}
	return out
}

func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !isRuneStart(s[cut]) {
		cut--
	}
	return strings.TrimSpace(s[:cut]) + " ..."
}

func isRuneStart(b byte) bool { return b&0xC0 != 0x80 }

// ShortenHome replaces the home directory prefix with ~.
func ShortenHome(p string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return p
	}
	if p == home {
		return "~"
	}
	if strings.HasPrefix(p, home+string(filepath.Separator)) {
		return "~" + p[len(home):]
	}
	return p
}
