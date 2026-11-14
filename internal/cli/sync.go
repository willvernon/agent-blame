package cli

import (
	"fmt"

	"agent-blame/internal/load"
	"agent-blame/internal/sources/claudecode"
	"agent-blame/internal/sources/git"
	"agent-blame/internal/store"
)

func runSync(env Env, args []string) error {
	fs := newFlags("sync", env)
	if err := fs.Parse(args); err != nil {
		return err
	}
	repo, err := git.Open(env.Dir)
	if err != nil {
		return err
	}
	l, err := load.New(repo, true)
	if err != nil {
		return err
	}
	res, err := l.Load(true)
	if err != nil {
		return err
	}
	read, unchanged, failed := 0, 0, 0
	for _, t := range res.Transcripts {
		switch {
		case t.Err != nil:
			failed++
			fmt.Fprintf(env.Stderr, "could not import %s: %v\n", claudecode.ShortenHome(t.Path), t.Err)
		case t.Events == 0 && t.Stats.Lines == 0:
			unchanged++
		default:
			read++
		}
	}
	fmt.Fprintf(env.Stdout, "%d transcripts imported, %d unchanged, %d failed. %d events stored in %s\n",
		read, unchanged, failed, len(res.Events), claudecode.ShortenHome(store.Path(repo.CommonDir)))
	return nil
}
