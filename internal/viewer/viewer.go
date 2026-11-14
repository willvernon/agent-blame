// Package viewer hands a change to an external diff viewer.
package viewer

import (
	"errors"
	"os"
	"os/exec"
	"strings"

	"github.com/google/shlex"
)

// PatchRequest is one change to show.
type PatchRequest struct {
	Path  string // the file, relative to the repository
	Patch string // a unified diff with file headers
	Dir   string // repository root, the viewer's working directory
}

// Viewer builds the process that shows a change. The cleanup removes any
// temporary file and runs after the process exits.
type Viewer interface {
	Name() string
	Command(req PatchRequest) (cmd *exec.Cmd, cleanup func(), err error)
}

// ErrNone is returned when no viewer could be found.
var ErrNone = errors.New("no diff viewer found: install leaf, or set AGENT_BLAME_VIEWER or PAGER")

// Find picks the first available viewer: AGENT_BLAME_VIEWER, then leaf,
// then the pager.
func Find() (Viewer, error) {
	if cmd := os.Getenv("AGENT_BLAME_VIEWER"); cmd != "" {
		args, err := shlex.Split(cmd)
		if err == nil && len(args) > 0 {
			return Custom{Args: args}, nil
		}
	}
	if path, err := exec.LookPath("leaf"); err == nil {
		return Leaf{Bin: path}, nil
	}
	pager := os.Getenv("PAGER")
	if pager == "" {
		pager = "less -R"
	}
	if args, err := shlex.Split(pager); err == nil && len(args) > 0 {
		if _, err := exec.LookPath(args[0]); err == nil {
			return Custom{Args: args, Label: "pager"}, nil
		}
	}
	return nil, ErrNone
}

// Leaf opens the patch in Leaf's diff view, in the light theme to match
// the paper look.
type Leaf struct{ Bin string }

func (Leaf) Name() string { return "leaf" }

func (l Leaf) Command(req PatchRequest) (*exec.Cmd, func(), error) {
	path, cleanup, err := tempPatch(req.Patch)
	if err != nil {
		return nil, nil, err
	}
	cmd := exec.Command(l.Bin, "diff", "--patch", path)
	cmd.Dir = req.Dir
	cmd.Env = append(os.Environ(), "LEAF_THEME=light")
	return cmd, cleanup, nil
}

// Custom runs a configured command with the patch file as its last argument.
type Custom struct {
	Args  []string
	Label string
}

func (c Custom) Name() string {
	if c.Label != "" {
		return c.Label
	}
	return c.Args[0]
}

func (c Custom) Command(req PatchRequest) (*exec.Cmd, func(), error) {
	path, cleanup, err := tempPatch(req.Patch)
	if err != nil {
		return nil, nil, err
	}
	args := append(append([]string{}, c.Args[1:]...), path)
	cmd := exec.Command(c.Args[0], args...)
	cmd.Dir = req.Dir
	return cmd, cleanup, nil
}

func tempPatch(patch string) (string, func(), error) {
	f, err := os.CreateTemp("", "agent-blame-*.patch")
	if err != nil {
		return "", nil, err
	}
	if !strings.HasSuffix(patch, "\n") {
		patch += "\n"
	}
	if _, err := f.WriteString(patch); err != nil {
		f.Close()
		os.Remove(f.Name())
		return "", nil, err
	}
	if err := f.Close(); err != nil {
		os.Remove(f.Name())
		return "", nil, err
	}
	name := f.Name()
	return name, func() { os.Remove(name) }, nil
}
