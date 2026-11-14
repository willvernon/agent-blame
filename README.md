# agent-blame POC

See exactly what your coding agent did to a repository. agent-blame is a terminal UI that lays out every prompt, file read, edit, command and commit as a live tree rooted at `main`, with the agent's reasoning attached to each change.

```
◎          main    6a8f3f9  docs: add AGENTS.md project brief
├─⬢      ▾ pr      rate-limit                                        open  14:01
┆ ◆─╮    ▾ prompt  tests are failing, fix it                2 edits, a1f3c9e  14:06
┆ │ ▸    ▸ run     Run the test suite                              failed  14:06
┆ │ ●──  ▾ edit    src/middleware/rate_limit.py                     +5 -2  14:07
┆ │ │      why     Tests need a clean limiter per case.
┆ │ │      commit  a1f3c9e  exact: committed in this session
┆ ■─╯      commit  a1f3c9e  reset limiter state in tests                   14:08
```

## Install

Requires Go 1.27+ and git.

```sh
git clone https://origin.cursor.com/willvernon/Agent-Blame.git
cd Agent-Blame
make install
```

This installs `agent-blame` to your Go bin directory (`go env GOPATH`/bin). Make sure that directory is on your `PATH`.

Optional: install [Leaf](https://github.com/DeepanshuMishraa/leaf) for the side-by-side diff view. Without it, diffs open in `$PAGER`.

## Start

From the root of any git repository you have used a coding agent in:

```sh
agent-blame
```

It works immediately on past sessions. No setup is needed to read history.

| Key | Action |
|-----|--------|
| `j` `k` | move |
| `l` `h` | open, close (or step in, step out) |
| `1` `2` `3` | fold to branches, prompts or events |
| `o` / `O` | open the change / the whole file in the diff viewer |
| `.` | show or hide searches and other tools |
| `q` | quit |

Leave the selection on the last row and the tree follows the agent live.

## Connect your coding harness

### Claude Code

agent-blame reads Claude Code session transcripts from `~/.claude/projects` with no configuration. To also capture HEAD and command outcomes as they happen, which makes commit matching exact, add the hooks:

```sh
agent-blame install claude-code
```

This adds three background hooks to `~/.claude/settings.json` and keeps a backup of the original. They take effect in new sessions and only record repositories where you have run `agent-blame` at least once. Remove them with `agent-blame install claude-code --uninstall`.

The same hooks are also available as a plugin in `plugins/claude-code`.

### OpenCode

Support is planned.

## Commands

| Command | Purpose |
|---------|---------|
| `agent-blame` | open the tree |
| `agent-blame doctor` | check what was found and what is missing |
| `agent-blame sync` | import transcripts into the local store |
| `agent-blame dump [--json] [--tree]` | print events for scripts |
| `agent-blame render --width 100` | print one frame without the interactive UI |

## Privacy

- Data is stored in `.git/agent-blame/`, inside the git directory, so it is never committed or pushed.
- Nothing is sent over the network.
- The agent's private reasoning is never stored, and command output is truncated.

## Development

```sh
make check    # gofmt, go vet, staticcheck, tests with the race detector
```

See `AGENTS.md` for the design and `docs/build-plan.md` for the roadmap.

## License

MIT
