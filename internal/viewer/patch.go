package viewer

import (
	"fmt"
	"strings"

	"agent-blame/internal/model"
	"agent-blame/internal/sources/git"
)

// ForChange builds the patch for one edit: the hunk the agent made, with
// file headers so any diff viewer can read it.
func ForChange(e *model.Event) (string, error) {
	if e == nil || e.Hunk == "" {
		return "", fmt.Errorf("this event has no recorded change")
	}
	return header(e.Subject, e.Created) + e.Hunk, nil
}

// ForWholeFile builds a patch with the whole file as context: from the
// commit the edit landed in, or from the working tree when it is not
// committed yet. Without either, it falls back to the recorded hunk.
func ForWholeFile(repo *git.Repo, e *model.Event) (string, error) {
	if e == nil || e.Kind != model.KindEdit || e.Outside {
		return ForChange(e)
	}
	const full = "-U100000"
	if e.Commit != nil {
		out, err := repo.Git("show", "--format=", "--no-color", full, e.Commit.SHA, "--", e.Subject)
		if err == nil && strings.TrimSpace(out) != "" {
			return out, nil
		}
	}
	if out, err := repo.Git("diff", "--no-color", full, "HEAD", "--", e.Subject); err == nil && strings.TrimSpace(out) != "" {
		return out, nil
	}
	// a new file that was never added: diff it against nothing
	if out, _ := repo.Git("diff", "--no-color", "--no-index", full, "/dev/null", e.Subject); strings.TrimSpace(out) != "" {
		return out, nil
	}
	return ForChange(e)
}

func header(path string, created bool) string {
	if created {
		return fmt.Sprintf("diff --git a/%[1]s b/%[1]s\nnew file mode 100644\n--- /dev/null\n+++ b/%[1]s\n", path)
	}
	return fmt.Sprintf("diff --git a/%[1]s b/%[1]s\n--- a/%[1]s\n+++ b/%[1]s\n", path)
}
