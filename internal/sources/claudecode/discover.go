package claudecode

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// ProjectsDir is where Claude Code keeps one folder of transcripts per
// working directory.
func ProjectsDir() string {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return filepath.Join(d, "projects")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude", "projects")
}

// Transcript is one session file.
type Transcript struct {
	Path    string
	Session string
	Cwd     string
	Size    int64
	ModTime time.Time
}

// Finder discovers transcripts for one repository. It remembers the
// working directory of every file it has opened, so repeated calls only
// read new files.
type Finder struct {
	Dir    string
	InRepo func(cwd string) bool

	mu   sync.Mutex
	cwds map[string]string // path -> cwd, "" when none was found
}

// NewFinder returns a finder over dir.
func NewFinder(dir string, inRepo func(string) bool) *Finder {
	return &Finder{Dir: dir, InRepo: inRepo, cwds: map[string]string{}}
}

// Find lists the main-thread transcripts whose working directory is inside
// the repository, oldest first. Subagent transcripts in subfolders are not
// included yet.
func (f *Finder) Find() ([]Transcript, error) {
	projects, err := os.ReadDir(f.Dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []Transcript
	for _, p := range projects {
		if !p.IsDir() {
			continue
		}
		dir := filepath.Join(f.Dir, p.Name())
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
				continue
			}
			path := filepath.Join(dir, e.Name())
			cwd, known := f.cwds[path]
			if !known {
				cwd = firstCwd(path)
				if cwd != "" {
					f.cwds[path] = cwd
				}
			}
			if cwd == "" || !f.InRepo(cwd) {
				continue
			}
			info, err := e.Info()
			if err != nil {
				continue
			}
			out = append(out, Transcript{
				Path: path, Session: strings.TrimSuffix(e.Name(), ".jsonl"), Cwd: cwd,
				Size: info.Size(), ModTime: info.ModTime(),
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ModTime.Before(out[j].ModTime) })
	return out, nil
}

// firstCwd reads lines until one carries a working directory. Early lines
// can be large, so it gives up after a few megabytes.
func firstCwd(path string) string {
	fh, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer fh.Close()
	br := bufio.NewReaderSize(io.LimitReader(fh, 8<<20), 1<<20)
	for i := 0; i < 200; i++ {
		line, err := br.ReadBytes('\n')
		if len(line) > 0 {
			var r struct {
				Cwd string `json:"cwd"`
			}
			if json.Unmarshal(line, &r) == nil && r.Cwd != "" {
				return r.Cwd
			}
		}
		if err != nil {
			return ""
		}
	}
	return ""
}
