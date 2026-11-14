// Package claudecode turns Claude Code session transcripts and hook
// payloads into events.
package claudecode

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
)

// Record is the part of a transcript line this package reads. Every field
// is optional: the format is undocumented and varies between versions.
type Record struct {
	Type         string          `json:"type"`
	UUID         string          `json:"uuid"`
	SessionID    string          `json:"sessionId"`
	Timestamp    string          `json:"timestamp"`
	Cwd          string          `json:"cwd"`
	GitBranch    string          `json:"gitBranch"`
	IsSidechain  bool            `json:"isSidechain"`
	IsMeta       bool            `json:"isMeta"`
	PromptID     string          `json:"promptId"`
	PromptSource string          `json:"promptSource"`
	AgentID      string          `json:"agentId"`
	Message      *Message        `json:"message"`
	ToolResult   json.RawMessage `json:"toolUseResult"`
}

// Message is the model message inside a user or assistant record.
type Message struct {
	ID      string          `json:"id"`
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

// Block is one content block.
type Block struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	IsError   *bool           `json:"is_error"`
}

// Blocks decodes the message content, which is either a string or an
// array of blocks. A string becomes one text block.
func (m *Message) Blocks() []Block {
	if m == nil || len(m.Content) == 0 {
		return nil
	}
	c := bytes.TrimSpace(m.Content)
	if len(c) > 0 && c[0] == '"' {
		var s string
		if json.Unmarshal(c, &s) == nil {
			return []Block{{Type: "text", Text: s}}
		}
		return nil
	}
	var blocks []Block
	if json.Unmarshal(c, &blocks) != nil {
		return nil
	}
	return blocks
}

// Stats counts what a read skipped, so doctor can report format drift.
type Stats struct {
	Lines     int
	Unparsed  int
	Partial   bool
	BytesRead int64
}

const maxLine = 64 << 20 // a single record can carry a large file write

// Read parses every complete line in r. A trailing line without a newline
// is treated as still being written and left for the next read.
func Read(r io.Reader, each func(Record)) (Stats, error) {
	var st Stats
	br := bufio.NewReaderSize(r, 1<<20)
	for {
		line, err := br.ReadSlice('\n')
		if errors.Is(err, bufio.ErrBufferFull) {
			// A long line: gather it fully.
			full := append([]byte(nil), line...)
			for errors.Is(err, bufio.ErrBufferFull) {
				line, err = br.ReadSlice('\n')
				full = append(full, line...)
				if len(full) > maxLine {
					return st, errors.New("transcript line too long")
				}
			}
			line = full
		}
		if err == io.EOF {
			if len(bytes.TrimSpace(line)) > 0 {
				st.Partial = true
			}
			return st, nil
		}
		if err != nil {
			return st, err
		}
		st.BytesRead += int64(len(line))
		st.Lines++
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var rec Record
		if json.Unmarshal(line, &rec) != nil {
			st.Unparsed++
			continue
		}
		each(rec)
	}
}

// ReadFile parses a transcript file.
func ReadFile(path string, each func(Record)) (Stats, error) {
	f, err := os.Open(path)
	if err != nil {
		return Stats{}, err
	}
	defer f.Close()
	return Read(f, each)
}
