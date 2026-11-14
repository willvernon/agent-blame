// Package model holds the normalized event type that every source produces
// and every view reads.
package model

import "time"

// SchemaVersion is written on every stored event. Bump it when a field
// changes meaning, and teach the store to read the old version.
const SchemaVersion = 1

// Kind is what an event represents in the tree.
type Kind string

const (
	KindPrompt Kind = "prompt"
	KindRead   Kind = "read"
	KindEdit   Kind = "edit"
	KindRun    Kind = "run"
	KindCommit Kind = "commit"
	KindOther  Kind = "other"
)

// Source names where an event came from.
const (
	SourceClaudeCode     = "claude-code"
	SourceClaudeCodeHook = "claude-code-hook"
	SourceGit            = "git"
)

// Note origins.
const (
	OriginDeclared   = "declared"
	OriginTranscript = "transcript"
	OriginNone       = "none"
)

// Commit attribution methods.
const (
	HowExact    = "exact"
	HowInferred = "inferred"
)

// Stat counts changed lines.
type Stat struct {
	Add int `json:"add"`
	Del int `json:"del"`
}

// Note is the agent's own account of an event.
type Note struct {
	Change string `json:"change,omitempty"`
	Why    string `json:"why,omitempty"`
	Origin string `json:"origin,omitempty"`
}

// CommitRef ties an edit to the commit it landed in.
type CommitRef struct {
	SHA string `json:"sha"`
	How string `json:"how"`
}

// Event is one thing that happened. Sources build events, the store keeps
// them, and the graph arranges them.
type Event struct {
	V       int       `json:"v"`
	ID      string    `json:"id"`
	TS      time.Time `json:"ts"`
	End     time.Time `json:"end,omitzero"`
	Source  string    `json:"source"`
	Session string    `json:"session,omitempty"`
	Agent   string    `json:"agent,omitempty"`
	Kind    Kind      `json:"kind"`
	Tool    string    `json:"tool,omitempty"`
	Branch  string    `json:"branch,omitempty"`
	Parent  string    `json:"parent,omitempty"`

	// Subject is the file path relative to the repo root, the command, the
	// prompt text or the commit subject.
	Subject string `json:"subject"`
	// Outside is true when Subject is a path outside the repository.
	Outside bool `json:"outside,omitempty"`

	Stat     *Stat  `json:"stat,omitempty"`
	OK       *bool  `json:"ok,omitempty"`
	RunClass string `json:"run_class,omitempty"`
	Output   string `json:"output,omitempty"`

	// Hunk is a unified diff body (hunk headers and lines, no file header).
	Hunk     string `json:"hunk,omitempty"`
	HunkBlob string `json:"hunk_blob,omitempty"`
	Created  bool   `json:"created,omitempty"`

	// Head is HEAD when the event happened. Only hooks know it.
	Head string `json:"head,omitempty"`
	// MadeCommit is set on a run that created a commit, once git confirms it.
	MadeCommit string `json:"made_commit,omitempty"`

	Commit *CommitRef `json:"commit,omitempty"`
	Note   Note       `json:"note,omitzero"`

	// InFlight is true for a tool call whose result has not arrived yet.
	InFlight bool `json:"in_flight,omitempty"`
}

// Bool returns a pointer to b, for optional fields.
func Bool(b bool) *bool { return &b }

// Merge folds b into a, field by field, for two views of the same event
// (a transcript import and a hook payload). Empty fields never overwrite.
// The transcript is preferred for content, the hook for HEAD and outcome.
func Merge(a, b Event) Event {
	out := a
	if out.ID == "" {
		return b
	}
	if out.TS.IsZero() || (!b.TS.IsZero() && b.TS.Before(out.TS)) {
		out.TS = b.TS
	}
	if b.End.After(out.End) {
		out.End = b.End
	}
	fill := func(dst *string, src string) {
		if *dst == "" {
			*dst = src
		}
	}
	fill(&out.Session, b.Session)
	fill(&out.Agent, b.Agent)
	fill(&out.Tool, b.Tool)
	fill(&out.Branch, b.Branch)
	fill(&out.Parent, b.Parent)
	fill(&out.Subject, b.Subject)
	fill(&out.RunClass, b.RunClass)
	fill(&out.Output, b.Output)
	fill(&out.Hunk, b.Hunk)
	fill(&out.HunkBlob, b.HunkBlob)
	fill(&out.Note.Change, b.Note.Change)
	fill(&out.Note.Why, b.Note.Why)
	fill(&out.Note.Origin, b.Note.Origin)
	fill(&out.MadeCommit, b.MadeCommit)
	if b.Head != "" {
		out.Head = b.Head
	}
	if b.OK != nil && (out.OK == nil || b.Source != SourceClaudeCode) {
		out.OK = b.OK
	}
	if out.Stat == nil {
		out.Stat = b.Stat
	}
	if out.Kind == "" || out.Kind == KindOther {
		if b.Kind != "" {
			out.Kind = b.Kind
		}
	}
	out.Created = out.Created || b.Created
	out.Outside = out.Outside || b.Outside
	out.InFlight = out.InFlight && b.InFlight
	return out
}
