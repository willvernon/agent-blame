// Package store keeps events on disk inside the git common directory, so
// nothing it writes can be committed or pushed.
//
// Layout:
//
//	<common-dir>/agent-blame/
//	  events/import-<session>.jsonl  events parsed from a transcript, rewritten on each sync
//	  events/hook-<session>.jsonl    events appended by the hook, one line at a time
//	  blobs/<sha256>                 hunks larger than 4 KB
//	  state.json                     schema version and what was synced
//	  errors.log                     hook and import failures, no payload content
package store

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"agent-blame/internal/model"
)

// DirName is the folder created inside the git common directory.
const DirName = "agent-blame"

const blobThreshold = 4 << 10

// Store is the on-disk event store for one repository.
type Store struct {
	Dir string

	mu    sync.Mutex
	state state
	dirty bool
}

type state struct {
	V           int                  `json:"v"`
	Transcripts map[string]synced    `json:"transcripts"`
	Extra       map[string]time.Time `json:"extra,omitempty"`
}

type synced struct {
	Size    int64     `json:"size"`
	ModTime time.Time `json:"mtime"`
}

// Path returns the store directory for a git common directory.
func Path(commonDir string) string { return filepath.Join(commonDir, DirName) }

// Exists reports whether a store has been created for this repository.
// The hook writes only to repositories that have one.
func Exists(commonDir string) bool {
	fi, err := os.Stat(Path(commonDir))
	return err == nil && fi.IsDir()
}

// Open opens the store. With create false and no store on disk, it returns
// nil and no error.
func Open(commonDir string, create bool) (*Store, error) {
	dir := Path(commonDir)
	if !create && !Exists(commonDir) {
		return nil, nil
	}
	for _, sub := range []string{"events", "blobs"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o700); err != nil {
			return nil, err
		}
	}
	s := &Store{Dir: dir, state: state{V: 1, Transcripts: map[string]synced{}}}
	if b, err := os.ReadFile(filepath.Join(dir, "state.json")); err == nil {
		var st state
		if json.Unmarshal(b, &st) == nil && st.V == 1 {
			if st.Transcripts == nil {
				st.Transcripts = map[string]synced{}
			}
			s.state = st
		}
	}
	return s, nil
}

// Changed reports whether a transcript differs from when it was last synced.
func (s *Store) Changed(path string, size int64, mod time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	prev, ok := s.state.Transcripts[path]
	return !ok || prev.Size != size || !prev.ModTime.Equal(mod)
}

// MarkSynced records that a transcript has been imported at this size.
func (s *Store) MarkSynced(path string, size int64, mod time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.Transcripts[path] = synced{Size: size, ModTime: mod}
	s.dirty = true
}

// SaveState writes state.json if anything changed.
func (s *Store) SaveState() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.dirty {
		return nil
	}
	b, err := json.MarshalIndent(s.state, "", "  ")
	if err != nil {
		return err
	}
	if err := writeAtomic(filepath.Join(s.Dir, "state.json"), b); err != nil {
		return err
	}
	s.dirty = false
	return nil
}

// WriteImport replaces the imported events for one session.
func (s *Store) WriteImport(session string, evs []model.Event) error {
	var buf bytes.Buffer
	for _, e := range evs {
		e, err := s.spill(e)
		if err != nil {
			return err
		}
		line, err := json.Marshal(e)
		if err != nil {
			return err
		}
		buf.Write(line)
		buf.WriteByte('\n')
	}
	return writeAtomic(filepath.Join(s.Dir, "events", "import-"+safeName(session)+".jsonl"), buf.Bytes())
}

// AppendHook appends one hook event under an exclusive lock.
func (s *Store) AppendHook(e model.Event) error {
	e, err := s.spill(e)
	if err != nil {
		return err
	}
	line, err := json.Marshal(e)
	if err != nil {
		return err
	}
	line = append(line, '\n')
	path := filepath.Join(s.Dir, "events", "hook-"+safeName(e.Session)+".jsonl")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := lock(f); err != nil {
		return err
	}
	defer unlock(f)
	_, err = f.Write(line)
	return err
}

// Load reads every stored event. Imported events are grouped by session.
func (s *Store) Load() (imported map[string][]model.Event, hooks []model.Event, err error) {
	imported = map[string][]model.Event{}
	entries, err := os.ReadDir(filepath.Join(s.Dir, "events"))
	if err != nil {
		return nil, nil, err
	}
	for _, ent := range entries {
		name := ent.Name()
		if !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		evs, err := s.readEvents(filepath.Join(s.Dir, "events", name))
		if err != nil {
			return nil, nil, err
		}
		switch {
		case strings.HasPrefix(name, "import-"):
			session := strings.TrimSuffix(strings.TrimPrefix(name, "import-"), ".jsonl")
			imported[session] = evs
		case strings.HasPrefix(name, "hook-"):
			hooks = append(hooks, evs...)
		}
	}
	return imported, hooks, nil
}

func (s *Store) readEvents(path string) ([]model.Event, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var evs []model.Event
	br := bufio.NewReaderSize(f, 1<<20)
	for {
		line, err := br.ReadBytes('\n')
		if len(line) > 0 && line[len(line)-1] == '\n' {
			var e model.Event
			if json.Unmarshal(line, &e) == nil && e.ID != "" && e.V <= model.SchemaVersion {
				if e.HunkBlob != "" && e.Hunk == "" {
					if b, err := os.ReadFile(filepath.Join(s.Dir, "blobs", e.HunkBlob)); err == nil {
						e.Hunk = string(b)
					}
				}
				evs = append(evs, e)
			}
		}
		if err != nil {
			break // a trailing partial line is ignored
		}
	}
	return evs, nil
}

// spill moves a large hunk into a content-addressed blob.
func (s *Store) spill(e model.Event) (model.Event, error) {
	if len(e.Hunk) <= blobThreshold {
		return e, nil
	}
	sum := sha256.Sum256([]byte(e.Hunk))
	name := hex.EncodeToString(sum[:])
	path := filepath.Join(s.Dir, "blobs", name)
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		if err := writeAtomic(path, []byte(e.Hunk)); err != nil {
			return e, err
		}
	}
	e.HunkBlob = name
	e.Hunk = ""
	return e, nil
}

// LogError appends one line to errors.log. Callers pass ids and field
// names only, never payload content.
func LogError(commonDir, msg string) {
	dir := Path(commonDir)
	f, err := os.OpenFile(filepath.Join(dir, "errors.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s %s\n", time.Now().UTC().Format(time.RFC3339), msg)
}

func writeAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func safeName(s string) string {
	if s == "" {
		return "unknown"
	}
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		}
		return '_'
	}, s)
}
