package claudecode

import (
	"encoding/json"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"agent-blame/internal/model"
)

// HookInput is the part of a Claude Code hook payload this package reads.
type HookInput struct {
	SessionID     string          `json:"session_id"`
	PromptID      string          `json:"prompt_id"`
	Cwd           string          `json:"cwd"`
	HookEventName string          `json:"hook_event_name"`
	Prompt        string          `json:"prompt"`
	ToolName      string          `json:"tool_name"`
	ToolInput     json.RawMessage `json:"tool_input"`
	ToolResponse  json.RawMessage `json:"tool_response"`
	ToolUseID     string          `json:"tool_use_id"`
	Error         string          `json:"error"`
	IsInterrupt   bool            `json:"is_interrupt"`
	AgentID       string          `json:"agent_id"`
}

// HookEvent turns one hook payload into an event with the same id the
// transcript import gives it, so the two merge. head and branch come from
// the repository at the moment the hook ran. ok is false for payloads that
// do not map to an event.
func HookEvent(in HookInput, res Resolver, now time.Time, head, branch string) (model.Event, bool) {
	if in.SessionID == "" || in.AgentID != "" {
		return model.Event{}, false // subagent activity is not captured yet
	}
	base := model.Event{
		V: model.SchemaVersion, TS: now, Source: model.SourceClaudeCodeHook,
		Session: in.SessionID, Branch: branch, Head: head,
	}
	switch in.HookEventName {
	case "UserPromptSubmit":
		text := CleanPrompt(in.Prompt)
		if in.PromptID == "" || text == "" {
			return model.Event{}, false
		}
		e := base
		e.ID = model.PromptID(in.SessionID, in.PromptID)
		e.Kind = model.KindPrompt
		e.Subject = text
		return e, true

	case "PostToolUse", "PostToolUseFailure":
		if in.ToolUseID == "" {
			return model.Event{}, false
		}
		kind, subject, change, cmd := classify(in.ToolName, in.ToolInput)
		e := base
		e.ID = model.ToolID(in.SessionID, in.ToolUseID)
		e.Kind = kind
		e.Tool = in.ToolName
		e.Subject = subject
		e.End = now
		if in.PromptID != "" {
			e.Parent = model.PromptID(in.SessionID, in.PromptID)
		}
		if change != "" {
			e.Note = model.Note{Change: change, Origin: model.OriginTranscript}
		}
		if (kind == model.KindRead || kind == model.KindEdit) && res != nil {
			if rel, ok := res.Rel(subject); ok {
				e.Subject = rel
			} else {
				e.Outside = true
				e.Subject = ShortenHome(subject)
			}
		}
		failed := in.HookEventName == "PostToolUseFailure"
		switch kind {
		case model.KindRun:
			e.RunClass = RunClass(cmd.Command)
			e.OK = model.Bool(!failed && !in.IsInterrupt)
			if failed {
				e.Output = capOutput(in.Error, 1024)
			} else if tr, ok := parseResult(in.ToolResponse); ok {
				out := tr.Stdout
				if tr.Stderr != "" {
					out = strings.TrimSpace(out + "\n" + tr.Stderr)
				}
				e.Output = capOutput(out, 1024)
				e.OK = model.Bool(!tr.Interrupted)
			}
		case model.KindEdit:
			if failed {
				e.OK = model.Bool(false)
				break
			}
			if tr, ok := parseResult(in.ToolResponse); ok {
				var patch string
				var st model.Stat
				switch {
				case len(tr.StructuredPatch) > 0:
					patch, st = patchFromHunks(tr.StructuredPatch)
				case tr.Type == "create":
					content := tr.Content
					if content == "" {
						content = cmd.Content
					}
					patch, st = patchFromContent(content)
					e.Created = true
				}
				if patch != "" {
					e.Hunk, e.Stat = patch, &st
				}
			}
		}
		return e, true
	}
	return model.Event{}, false
}

// RootResolver resolves paths against one worktree root without touching
// the filesystem, for the hook, which must stay fast.
type RootResolver struct{ Root string }

func (r RootResolver) Rel(p string) (string, bool) {
	if p == "" || !filepath.IsAbs(p) || r.Root == "" {
		return p, false
	}
	root := filepath.Clean(r.Root)
	p = filepath.Clean(p)
	prefix := root + string(filepath.Separator)
	match := strings.HasPrefix(p, prefix)
	if !match && runtime.GOOS == "darwin" {
		// macOS disks are usually case-insensitive
		match = len(p) > len(prefix) && strings.EqualFold(p[:len(prefix)], prefix)
	}
	if !match {
		return p, false
	}
	return filepath.ToSlash(p[len(prefix):]), true
}
