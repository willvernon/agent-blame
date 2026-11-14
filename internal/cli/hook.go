package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"time"

	"agent-blame/internal/sources/claudecode"
	"agent-blame/internal/sources/git"
	"agent-blame/internal/store"
)

// runHook records one hook payload. It always returns 0 and never writes
// to stdout, so it can never disturb or block the agent session. It only
// records for repositories that already have a store, which is created by
// opening the tree or running sync there once.
func runHook(env Env, args []string) (code int) {
	defer func() { _ = recover() }()
	if len(args) == 0 || args[0] != "claude-code" {
		return 0
	}
	data, err := io.ReadAll(io.LimitReader(env.Stdin, 64<<20))
	if err != nil {
		return 0
	}
	var in claudecode.HookInput
	if json.Unmarshal(data, &in) != nil || in.Cwd == "" {
		return 0
	}
	dirs, err := git.FindDirs(in.Cwd)
	if err != nil || !store.Exists(dirs.CommonDir) {
		return 0
	}
	head, branch := git.ReadHead(dirs)
	ev, ok := claudecode.HookEvent(in, claudecode.RootResolver{Root: dirs.Root}, time.Now().UTC(), head, branch)
	if !ok {
		return 0
	}
	st, err := store.Open(dirs.CommonDir, false)
	if err != nil || st == nil {
		store.LogError(dirs.CommonDir, fmt.Sprintf("hook: open store: %v", err))
		return 0
	}
	if err := st.AppendHook(ev); err != nil {
		store.LogError(dirs.CommonDir, fmt.Sprintf("hook: append %s: %v", ev.ID, err))
	}
	return 0
}
