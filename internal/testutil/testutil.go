// Package testutil builds throwaway repositories and compares golden files.
//
// Only synthetic data may be committed as test data. Never copy a real
// transcript, hook payload or event log into a testdata directory.
package testutil

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Repo is a git repository in a temp directory with deterministic commits.
type Repo struct {
	T   *testing.T
	Dir string
}

// NewRepo creates an empty repository on branch main.
func NewRepo(t *testing.T) *Repo {
	t.Helper()
	dir := t.TempDir()
	// Resolve symlinks (macOS temp dirs live under a symlinked /var) so
	// paths compare equal to what git reports.
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		dir = real
	}
	r := &Repo{T: t, Dir: dir}
	r.Git("init", "-q", "-b", "main")
	r.Git("config", "user.name", "test")
	r.Git("config", "user.email", "test@example.com")
	r.Git("config", "commit.gpgsign", "false")
	return r
}

// Git runs git in the repository and returns trimmed stdout.
func (r *Repo) Git(args ...string) string {
	r.T.Helper()
	return r.gitAt(time.Time{}, args...)
}

func (r *Repo) gitAt(at time.Time, args ...string) string {
	r.T.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = r.Dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	if !at.IsZero() {
		stamp := fmt.Sprintf("@%d +0000", at.Unix())
		cmd.Env = append(cmd.Env, "GIT_AUTHOR_DATE="+stamp, "GIT_COMMITTER_DATE="+stamp)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		r.T.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// Write puts content at path, creating directories.
func (r *Repo) Write(path, content string) {
	r.T.Helper()
	full := filepath.Join(r.Dir, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		r.T.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		r.T.Fatal(err)
	}
}

// Commit writes files, stages everything and commits at a fixed time.
func (r *Repo) Commit(at time.Time, msg string, files map[string]string) string {
	r.T.Helper()
	for p, c := range files {
		r.Write(p, c)
	}
	r.Git("add", "-A")
	r.gitAt(at, "commit", "-q", "-m", msg)
	return r.Git("rev-parse", "HEAD")
}

// Merge merges branch into the current branch with a merge commit.
func (r *Repo) Merge(at time.Time, branch, msg string) string {
	r.T.Helper()
	r.gitAt(at, "merge", "-q", "--no-ff", "-m", msg, branch)
	return r.Git("rev-parse", "HEAD")
}

// Golden compares got with testdata/<name>. With UPDATE_GOLDEN=1 set, it
// writes got instead.
func Golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if os.Getenv("UPDATE_GOLDEN") != "" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("missing golden file %s (run with UPDATE_GOLDEN=1): %v", path, err)
	}
	if string(want) != got {
		t.Errorf("%s differs from golden file.\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
	}
}
