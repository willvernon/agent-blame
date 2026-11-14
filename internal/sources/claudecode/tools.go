package claudecode

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"agent-blame/internal/model"
)

// toolInput holds the input fields of the tools this package understands.
type toolInput struct {
	FilePath     string `json:"file_path"`
	NotebookPath string `json:"notebook_path"`
	Path         string `json:"path"`
	Command      string `json:"command"`
	Description  string `json:"description"`
	Pattern      string `json:"pattern"`
	Query        string `json:"query"`
	URL          string `json:"url"`
	Content      string `json:"content"`
	OldString    string `json:"old_string"`
	NewString    string `json:"new_string"`
}

// classify maps a tool name and input to an event kind and subject.
func classify(name string, raw json.RawMessage) (kind model.Kind, subject, change string, in toolInput) {
	_ = json.Unmarshal(raw, &in)
	switch name {
	case "Read":
		return model.KindRead, in.FilePath, "", in
	case "Edit", "MultiEdit", "Write":
		return model.KindEdit, in.FilePath, "", in
	case "NotebookEdit":
		return model.KindEdit, in.NotebookPath, "", in
	case "Bash":
		return model.KindRun, oneLine(in.Command), in.Description, in
	case "Grep", "Glob":
		s := in.Pattern
		if in.Path != "" {
			s += " in " + in.Path
		}
		return model.KindOther, s, "", in
	case "WebSearch":
		return model.KindOther, in.Query, "", in
	case "WebFetch":
		return model.KindOther, in.URL, "", in
	case "Agent", "Task":
		return model.KindOther, in.Description, "", in
	}
	return model.KindOther, name, "", in
}

func oneLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i] + " ..."
	}
	return s
}

var runClasses = []struct {
	class string
	re    *regexp.Regexp
}{
	{"test", regexp.MustCompile(`(^|[\s;&|(])(pytest|go test|cargo test|npm (run )?test|bun test|yarn test|pnpm test|jest|vitest|rspec|mix test|make test)\b`)},
	{"build", regexp.MustCompile(`(^|[\s;&|(])(go build|cargo build|npm run build|bun run build|make( |$)|tsc\b|gradle|mvn)`)},
	{"git", regexp.MustCompile(`(^|[\s;&|(])git (commit|checkout|switch|merge|rebase|push|reset|cherry-pick)\b`)},
}

// RunClass tags a shell command as test, build, git or other.
func RunClass(cmd string) string {
	for _, rc := range runClasses {
		if rc.re.MatchString(cmd) {
			return rc.class
		}
	}
	return "other"
}

var gitCommitRE = regexp.MustCompile(`(^|[\s;&|(])git\s[^;&|]*?\bcommit(\s|$|[;&|)])`)

// IsGitCommit reports whether a command runs git commit.
func IsGitCommit(cmd string) bool { return gitCommitRE.MatchString(cmd) }

// toolResult is the structured result of the tools this package reads.
type toolResult struct {
	FilePath        string  `json:"filePath"`
	Type            string  `json:"type"`
	Content         string  `json:"content"`
	StructuredPatch []hunk  `json:"structuredPatch"`
	Stdout          string  `json:"stdout"`
	Stderr          string  `json:"stderr"`
	Interrupted     bool    `json:"interrupted"`
	OriginalFile    *string `json:"originalFile"`
}

type hunk struct {
	OldStart int      `json:"oldStart"`
	OldLines int      `json:"oldLines"`
	NewStart int      `json:"newStart"`
	NewLines int      `json:"newLines"`
	Lines    []string `json:"lines"`
}

// parseResult decodes a structured tool result, which may be an object,
// an array or a plain string depending on the tool.
func parseResult(raw json.RawMessage) (toolResult, bool) {
	var tr toolResult
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '{' {
		return tr, false
	}
	if json.Unmarshal(raw, &tr) != nil {
		return tr, false
	}
	return tr, true
}

// patchFromHunks renders structured hunks as a unified diff body.
func patchFromHunks(hs []hunk) (string, model.Stat) {
	var b strings.Builder
	var st model.Stat
	for _, h := range hs {
		fmt.Fprintf(&b, "@@ -%d,%d +%d,%d @@\n", h.OldStart, h.OldLines, h.NewStart, h.NewLines)
		for _, l := range h.Lines {
			b.WriteString(l)
			b.WriteByte('\n')
			switch {
			case strings.HasPrefix(l, "+"):
				st.Add++
			case strings.HasPrefix(l, "-"):
				st.Del++
			}
		}
	}
	return b.String(), st
}

// patchFromContent renders a newly created file as an all-added hunk.
func patchFromContent(content string) (string, model.Stat) {
	content = strings.TrimSuffix(content, "\n")
	if content == "" {
		return "", model.Stat{}
	}
	lines := strings.Split(content, "\n")
	var b strings.Builder
	fmt.Fprintf(&b, "@@ -0,0 +1,%d @@\n", len(lines))
	for _, l := range lines {
		b.WriteString("+")
		b.WriteString(l)
		b.WriteByte('\n')
	}
	return b.String(), model.Stat{Add: len(lines)}
}

// capOutput keeps the start and end of long command output.
func capOutput(s string, keep int) string {
	s = strings.TrimSpace(s)
	if len(s) <= 2*keep {
		return s
	}
	head := s[:keep]
	tail := s[len(s)-keep:]
	return head + "\n...\n" + tail
}

var tagBlocks = func() []*regexp.Regexp {
	names := []string{
		"system-reminder", "command-name", "command-message", "command-args",
		"local-command-stdout", "local-command-stderr", "local-command-caveat",
		"user-prompt-submit-hook", "ide_selection", "ide_opened_file", "ide_diagnostics",
	}
	res := make([]*regexp.Regexp, len(names))
	for i, n := range names {
		res[i] = regexp.MustCompile(`(?s)<` + regexp.QuoteMeta(n) + `>.*?</` + regexp.QuoteMeta(n) + `>`)
	}
	return res
}()

// CleanPrompt strips the blocks Claude Code injects around a typed prompt.
func CleanPrompt(s string) string {
	for _, re := range tagBlocks {
		s = re.ReplaceAllString(s, "")
	}
	return strings.TrimSpace(s)
}
