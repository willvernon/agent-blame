package git

import (
	"bufio"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// The functions in this file read repository layout and HEAD straight from
// the filesystem, without starting git. The hook runs on every tool call,
// so it cannot afford a subprocess.

// Dirs is the layout found by walking up from a directory.
type Dirs struct {
	Root      string
	GitDir    string
	CommonDir string
}

// FindDirs walks up from start to the nearest .git, which is a directory in
// a normal checkout and a file pointing elsewhere in a linked worktree.
func FindDirs(start string) (Dirs, error) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return Dirs{}, err
	}
	for {
		dotgit := filepath.Join(dir, ".git")
		fi, err := os.Stat(dotgit)
		if err == nil {
			if fi.IsDir() {
				return Dirs{Root: dir, GitDir: dotgit, CommonDir: dotgit}, nil
			}
			gitDir, err := readGitFile(dotgit, dir)
			if err != nil {
				return Dirs{}, err
			}
			common := gitDir
			if b, err := os.ReadFile(filepath.Join(gitDir, "commondir")); err == nil {
				c := strings.TrimSpace(string(b))
				if !filepath.IsAbs(c) {
					c = filepath.Join(gitDir, c)
				}
				common = filepath.Clean(c)
			}
			return Dirs{Root: dir, GitDir: gitDir, CommonDir: common}, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return Dirs{}, ErrNotRepo
		}
		dir = parent
	}
}

func readGitFile(path, base string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	line := strings.TrimSpace(string(b))
	target, ok := strings.CutPrefix(line, "gitdir: ")
	if !ok {
		return "", errors.New("unrecognized .git file")
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(base, target)
	}
	return filepath.Clean(target), nil
}

// ReadHead returns the commit HEAD points at and the branch name, if any.
// Either may be empty: a new repository has a branch but no commit.
func ReadHead(d Dirs) (sha, branch string) {
	b, err := os.ReadFile(filepath.Join(d.GitDir, "HEAD"))
	if err != nil {
		return "", ""
	}
	head := strings.TrimSpace(string(b))
	ref, isRef := strings.CutPrefix(head, "ref: ")
	if !isRef {
		return head, ""
	}
	branch = strings.TrimPrefix(ref, "refs/heads/")
	if b, err := os.ReadFile(filepath.Join(d.CommonDir, filepath.FromSlash(ref))); err == nil {
		return strings.TrimSpace(string(b)), branch
	}
	return packedRef(d.CommonDir, ref), branch
}

func packedRef(common, ref string) string {
	f, err := os.Open(filepath.Join(common, "packed-refs"))
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if sha, name, ok := strings.Cut(line, " "); ok && name == ref {
			return sha
		}
	}
	return ""
}
