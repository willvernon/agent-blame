// Package git reads repository state through the git command line, with
// machine-readable output only.
package git

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Repo is one repository, seen from one of its worktrees.
type Repo struct {
	Root      string // top level of the current worktree
	GitDir    string // this worktree's git directory
	CommonDir string // shared by all worktrees; local state lives here
	Worktrees []string

	mu    sync.Mutex
	roots []os.FileInfo
	cache map[string]string // directory -> matching root, "" for none
}

// ErrNotRepo is returned when the directory is not inside a git work tree.
var ErrNotRepo = errors.New("not inside a git repository")

// Open finds the repository that contains dir.
func Open(dir string) (*Repo, error) {
	out, err := run(dir, "rev-parse", "--path-format=absolute", "--show-toplevel", "--git-dir", "--git-common-dir")
	if err != nil {
		return nil, ErrNotRepo
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) < 3 {
		return nil, ErrNotRepo
	}
	r := &Repo{Root: lines[0], GitDir: lines[1], CommonDir: lines[2]}
	r.Worktrees = r.listWorktrees()
	return r, nil
}

func (r *Repo) listWorktrees() []string {
	out, err := r.Git("worktree", "list", "--porcelain")
	if err != nil {
		return []string{r.Root}
	}
	var roots []string
	for _, line := range strings.Split(out, "\n") {
		if p, ok := strings.CutPrefix(line, "worktree "); ok {
			roots = append(roots, p)
		}
	}
	if len(roots) == 0 {
		roots = []string{r.Root}
	}
	return roots
}

// Git runs a git command in the repository root and returns stdout.
func (r *Repo) Git(args ...string) (string, error) { return run(r.Root, args...) }

func run(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0", "LC_ALL=C")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return stdout.String(), fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

// Rel returns p relative to the worktree that contains it. It compares
// files, not strings, so it holds on case-insensitive disks and across
// symlinks. ok is false when p is outside every worktree.
func (r *Repo) Rel(p string) (rel string, ok bool) {
	if p == "" || !filepath.IsAbs(p) {
		return p, false
	}
	p = filepath.Clean(p)
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.roots == nil {
		r.cache = map[string]string{}
		for _, root := range r.Worktrees {
			if fi, err := os.Stat(root); err == nil {
				r.roots = append(r.roots, fi)
			}
		}
	}
	var visited []string
	for dir := p; ; dir = filepath.Dir(dir) {
		root, seen := r.cache[dir]
		if !seen {
			root = ""
			if fi, err := os.Stat(dir); err == nil {
				for _, rfi := range r.roots {
					if os.SameFile(fi, rfi) {
						root = dir
						break
					}
				}
			}
		}
		if root != "" || seen {
			for _, v := range visited {
				r.cache[v] = root
			}
			if root == "" {
				return p, false
			}
			rel, err := filepath.Rel(root, p)
			if err != nil {
				return p, false
			}
			return filepath.ToSlash(rel), true
		}
		visited = append(visited, dir)
		if parent := filepath.Dir(dir); parent == dir {
			for _, v := range visited {
				r.cache[v] = ""
			}
			return p, false
		}
	}
}

// Contains reports whether p is inside one of the repository's worktrees.
func (r *Repo) Contains(p string) bool {
	_, ok := r.Rel(p)
	return ok
}

// DefaultBranch is origin's HEAD, else main, else master, else the current branch.
func (r *Repo) DefaultBranch() string {
	if out, err := r.Git("symbolic-ref", "--quiet", "--short", "refs/remotes/origin/HEAD"); err == nil {
		if name := strings.TrimSpace(out); name != "" {
			return strings.TrimPrefix(name, "origin/")
		}
	}
	for _, name := range []string{"main", "master"} {
		if _, err := r.Git("rev-parse", "--verify", "--quiet", "refs/heads/"+name); err == nil {
			return name
		}
	}
	if b := r.CurrentBranch(); b != "" {
		return b
	}
	return "main"
}

// CurrentBranch is the checked-out branch, or "" when HEAD is detached.
func (r *Repo) CurrentBranch() string {
	out, err := r.Git("symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// Commit is one commit with the files it touched.
type Commit struct {
	SHA     string
	Parents []string
	Time    time.Time
	Subject string
	Files   []string
}

// Short is the abbreviated hash.
func (c Commit) Short() string {
	if len(c.SHA) > 7 {
		return c.SHA[:7]
	}
	return c.SHA
}

// Commits lists up to limit commits reachable from any local branch or
// HEAD, newest first, with the paths each touched.
func (r *Repo) Commits(limit int) ([]Commit, error) {
	if !r.HasCommits() {
		return nil, nil
	}
	out, err := r.Git("log", "--branches", "HEAD", "--date-order", "-n", strconv.Itoa(limit),
		"--format=%x1e%H%x1f%P%x1f%ct%x1f%s", "--name-only", "--no-renames")
	if err != nil {
		return nil, err
	}
	var commits []Commit
	for _, rec := range strings.Split(out, "\x1e") {
		rec = strings.TrimLeft(rec, "\n")
		if rec == "" {
			continue
		}
		head, files, _ := strings.Cut(rec, "\n")
		f := strings.Split(head, "\x1f")
		if len(f) < 4 {
			continue
		}
		secs, _ := strconv.ParseInt(f[2], 10, 64)
		c := Commit{SHA: f[0], Time: time.Unix(secs, 0), Subject: f[3]}
		if f[1] != "" {
			c.Parents = strings.Fields(f[1])
		}
		for _, name := range strings.Split(files, "\n") {
			if name = strings.TrimSpace(name); name != "" {
				c.Files = append(c.Files, name)
			}
		}
		commits = append(commits, c)
	}
	return commits, nil
}

// HasCommits is false in a repository with no commits yet.
func (r *Repo) HasCommits() bool {
	_, err := r.Git("rev-parse", "--verify", "--quiet", "HEAD")
	return err == nil
}

// FirstParent lists the first-parent history of ref, newest first.
func (r *Repo) FirstParent(ref string, limit int) []string {
	out, err := r.Git("rev-list", "--first-parent", "-n", strconv.Itoa(limit), ref, "--")
	if err != nil {
		return nil
	}
	return strings.Fields(out)
}

// Range lists commits in a..b, newest first.
func (r *Repo) Range(a, b string, limit int) []string {
	out, err := r.Git("rev-list", "-n", strconv.Itoa(limit), a+".."+b, "--")
	if err != nil {
		return nil
	}
	return strings.Fields(out)
}

// Branch is a local branch head.
type Branch struct {
	Name string
	SHA  string
}

// Branches lists local branches.
func (r *Repo) Branches() []Branch {
	out, err := r.Git("for-each-ref", "--format=%(refname:short)%1f%(objectname)", "refs/heads")
	if err != nil {
		return nil
	}
	var bs []Branch
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		name, sha, ok := strings.Cut(line, "\x1f")
		if ok {
			bs = append(bs, Branch{Name: name, SHA: sha})
		}
	}
	return bs
}

// IsAncestor reports whether a is an ancestor of b.
func (r *Repo) IsAncestor(a, b string) bool {
	_, err := r.Git("merge-base", "--is-ancestor", a, b)
	return err == nil
}

// Dirty lists paths that differ from HEAD: modified, staged or untracked.
func (r *Repo) Dirty() map[string]bool {
	out, err := r.Git("status", "--porcelain=v1", "-z", "--untracked-files=all")
	dirty := map[string]bool{}
	if err != nil {
		return dirty
	}
	entries := strings.Split(out, "\x00")
	for i := 0; i < len(entries); i++ {
		e := entries[i]
		if len(e) < 4 {
			continue
		}
		dirty[e[3:]] = true
		if e[0] == 'R' || e[0] == 'C' {
			i++ // the next entry is the original path
		}
	}
	return dirty
}

// Head returns the full hash of HEAD, or "" with no commits.
func (r *Repo) Head() string {
	out, err := r.Git("rev-parse", "--verify", "--quiet", "HEAD")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}
