# Architecture

How agent-blame works, from an agent's tool call to a row on screen.

## Summary

- Three layers: **capture** turns harness output into events, **model** arranges events and git history into a tree, **view** draws the tree.
- Every source produces the same `model.Event`, with an id derived from the source. Two views of one action (a transcript line and a hook payload) get the same id and merge.
- Capture needs no setup: Claude Code already writes a live transcript per session. Hooks are optional and add what transcripts lack: HEAD at each step and command outcomes.
- The graph joins events with git to place each prompt on its branch and tie each edit to the commit it landed in.
- The layout is pure code with no terminal dependency. It produces lines of text tagged with style names, which the theme turns into colors. Every frame can be tested as plain text.
- All state lives in `.git/agent-blame/`. Nothing leaves the machine.

## 1. The big picture

```
  CAPTURE                          MODEL                       VIEW
  -------                          -----                       ----

  ~/.claude/projects/*.jsonl                                   Bubble Tea loop
  (live session transcripts)                                   (internal/tui)
        |                                                            ^
        v                                                            |
  sources/claudecode  ----+                                     theme: style
  (Finder, Mapper)        |                                     tokens to colors
                          v                                          ^
  agent-blame hook ---> store ----> load ----> graph ----> layout ---+
  (hook payloads)    .git/agent-   (merge     (+ git      (rows, gutter,
                     blame/         by id)     history)    rails, detail)
                          ^                       ^
                          |                       |
                     sync / TUI              sources/git
                     write imports           (git CLI)
```

Read it left to right:

1. **Capture.** The transcript reader and the hook each turn raw harness output into `model.Event` values.
2. **Store.** Events are kept inside the git directory so history survives transcript cleanup.
3. **Load.** Fresh transcript parses, stored imports and hook events are merged into one list, keyed by event id.
4. **Graph.** Events and git history become a tree: main, then branches, prompts, and the reads, edits, runs and commits under each prompt.
5. **Layout.** The tree becomes lines of styled spans for a given width, selection and set of open items.
6. **Theme and TUI.** Spans become terminal colors, and Bubble Tea handles keys, polling and the diff viewer.

## 2. Package map

```
cmd/agent-blame/        entry point, sets the version
internal/
  cli/                  subcommands: tree, dump, render, sync, hook, install, doctor
  model/                Event type, deterministic ids, Merge
  sources/
    claudecode/         transcript discovery and parsing, hook payloads
    git/                repository state through the git CLI, HEAD from files
  store/                event files, blobs and sync state in .git/agent-blame
  load/                 gathers and merges events from every source
  graph/                the tree, commit attribution, branch status
  tui/
    layout/             pure layout: rows, gutter, scope, navigation
    theme.go            paper theme, style tokens to Lip Gloss
    model.go            Bubble Tea model: keys, polling, viewer
  viewer/               builds patches and hands them to Leaf or a pager
  testutil/             throwaway git repos, the reference scenario, golden files
plugins/claude-code/    the hooks packaged as a Claude Code plugin
```

The dependency rule: `tui` imports `graph` and `layout`. `layout` imports `graph` and `model` but never Bubble Tea or Lip Gloss. `sources` import only `model`. Nothing imports `tui`.

## 3. The data model

### Event

One `model.Event` is one thing that happened. The fields that matter most:

| Field | Meaning |
|-------|---------|
| `ID` | deterministic id, see below |
| `Kind` | `prompt`, `read`, `edit`, `run`, `commit` or `other` |
| `TS`, `End` | when the call started and when its result arrived |
| `Session`, `Branch` | the harness session, and the git branch it was on |
| `Parent` | the prompt a tool call belongs to |
| `Subject` | repo-relative path, command, prompt text or commit subject |
| `Hunk`, `Stat` | the diff the agent made and its line counts |
| `OK`, `Output` | a run's outcome, and its output cut to the first and last kilobyte |
| `Note.Why` | the agent's own text just before the call |
| `Head` | HEAD when the event happened, known only from hooks |
| `InFlight` | a tool call whose result has not arrived yet |

### Deterministic ids

Ids come from the source, never from a random generator:

```
cc:<session>:prompt:<promptId>     a Claude Code prompt
cc:<session>:tool:<tool_use_id>    a Claude Code tool call
git:<sha>                          a commit
branch:<name>                      a branch row
```

Consequences:

- Importing a transcript twice yields identical events. Every import is idempotent.
- A tool call seen in the transcript and again in a hook payload has one id, so the two views merge instead of duplicating.

### Merging two views of one event

`model.Merge(a, b)` fills empty fields of `a` from `b` and never overwrites with emptiness. The transcript is preferred for content (hunk, why, output). The hook is preferred for HEAD and for outcome. The loader always merges transcript first and hook last.

## 4. Capture

### Claude Code transcripts

Claude Code appends one JSON record per line to `~/.claude/projects/<cwd-slug>/<session>.jsonl` while a session runs.

**Discovery** (`claudecode.Finder`):

- Lists every top-level `.jsonl` file under the projects folder.
- Reads each file only until a record carries a working directory, then caches it. New files are read once.
- Keeps a session if that directory is inside the repository. The check compares files with `os.SameFile` instead of strings, so it works on case-insensitive disks, through symlinks and across worktrees.

**Parsing** (`claudecode.Read`):

- Reads complete lines only. A trailing line without a newline is still being written and is left for the next read.
- Every field is optional. Unknown record types are skipped, and a line that fails to parse is counted, not fatal. The format is undocumented, so this matters.

**Mapping records to events** (`claudecode.Mapper`):

| Record | Becomes |
|--------|---------|
| a `user` record with `promptSource` set | a prompt. Injected blocks such as reminders are stripped from its text |
| a `tool_use` block in an `assistant` record | an in-flight read, edit, run or other event |
| a `user` record with a matching `tool_result` | completes that event: the diff from `structuredPatch`, a run's outcome from `is_error`, output |
| `text` blocks before a `tool_use` in the same model response | that call's `Note.Why`, capped at 600 characters |
| `thinking` blocks | nothing. Private reasoning is never stored |
| sidechain and meta records | nothing, for now |

Two details learned from real data:

- Prompts carried over from another machine can share one `promptId`. A repeated id falls back to the record's own `uuid`.
- `toolUseResult` can be an object, an array or a string, so it is decoded lazily and only used when it is an object.

### Claude Code hooks

`agent-blame install claude-code` adds three hooks to Claude Code's user settings: `UserPromptSubmit`, `PostToolUse` and `PostToolUseFailure`. Each one runs `agent-blame hook claude-code` in the background.

What the hook does with one payload:

1. Reads the JSON payload from stdin.
2. Walks up from the payload's `cwd` to find `.git`, without starting git. A linked worktree's `.git` file is followed to the shared directory.
3. Stops unless `.git/agent-blame/` exists. Recording is opt-in per repository, so a globally installed hook writes nowhere you have not opened agent-blame.
4. Reads HEAD and the branch straight from files.
5. Builds the event with the same id the transcript would give it.
6. Appends one line to the store under an exclusive lock.
7. Exits 0, always, and never prints. A failure goes to `errors.log`, and the agent session is never disturbed.

It takes a median of 4 ms per call.

### git

`sources/git` runs the git CLI with machine-readable output (`--format`, `-z`, `--porcelain`). It provides:

- the default branch, its first-parent history, and local branch heads
- the last 2000 commits with the files each touched
- the working tree's dirty paths
- patches for the diff viewer

## 5. The store

```
.git/agent-blame/
  events/import-<session>.jsonl   parsed from a transcript, rewritten when it changes
  events/hook-<session>.jsonl     appended by the hook, one line at a time
  blobs/<sha256>                  hunks larger than 4 KB
  state.json                      size and modification time of each synced transcript
  errors.log                      ids and messages only, never payload content
```

- **Two kinds of file, two writers.** Imports are rewritten whole with an atomic rename, and hooks only append. They never write the same file.
- **Inside `.git`.** Nothing here can be committed or pushed.
- **Survives cleanup.** Claude Code deletes old transcripts eventually. Events already synced stay.

## 6. Loading

`load.Loader.Load(sync bool)` produces the event list:

```
find transcripts for this repo
for each transcript:
    unchanged since last sync?  skip it (its stored import is used)
    otherwise                   parse it; if syncing, rewrite its import file
merge, keyed by event id:
    1. stored imports, for sessions not parsed just now
    2. fresh parses
    3. hook events, last, so they supply HEAD and outcome
sort by time
```

`dump` and `render` call it with `sync` off and write nothing. The TUI and `sync` call it with `sync` on.

## 7. The graph

`graph.Build(repo, events, options)` arranges everything into a tree.

### Shape

```
main                          the default branch, titled by its head commit
  pr        one per branch    other than the default; status open, merged or closed
    prompt                    in time order on its branch
      read / edit / run       the prompt's tool calls
      commit                  commits that hold the prompt's edits
    merge                     for a merged branch, the merge commit, drawn on main
  prompt                      work done directly on the default branch hangs off main
```

Branch status comes from git alone:

- **merged:** a merge commit on main whose subject names the branch (`Merge branch 'x'` or `Merge pull request #N from owner/x`). The PR number is taken from it.
- **open:** a local branch ref still exists.
- **closed:** neither is true.

Searches, web fetches and other tools are hidden unless asked for.

### Which commit did an edit land in

Each edit is checked against these rules in order, and the first match wins:

1. **exact, by run.** A `git commit` the agent ran later in the same session, whose commit time falls inside that run, and which touches the file. This works even with `-q`, which prints no hash.
2. **exact, by HEAD.** The next hook event in the session has a different HEAD, and that commit touches the file.
3. **inferred.** The first commit at or after the edit that touches the file.
4. **none.** The file still differs from HEAD (uncommitted), or it does not (orphan: overwritten or reverted before any commit).

A commit row is placed under the prompt whose run made it, or else under the prompt with the latest edit it holds.

### Lanes

Lanes are the gutter columns:

| Lane | What sits there |
|------|-----------------|
| 0 | main, merge rows, and prompts made directly on main |
| 1 | branch rows, their prompts, and those prompts' commits |
| 2 | the tool calls of a prompt on a branch |

Each prompt's tool calls sit one lane to the right of the prompt.

## 8. The view

### Layout: pure functions

`internal/tui/layout` takes the graph plus a `State` (open items, selection, scroll, width) and returns a `Frame` of lines. Each line is a list of spans, each span a piece of text and a style token such as `node.edit` or `muted`. It never touches the terminal.

**Rows.** `Flatten` walks the tree and emits a row for every visible node. An open event also gets detail rows: change, why, a diff of up to 14 lines, output, and the commit match. A long prompt gets its full text.

**Gutter.** Built in passes over the visible rows:

1. Vertical spans: main runs solid to the last row on it and dashed below while a branch is open. Each open branch spans its rows, and each open prompt spans its tool calls.
2. Connectors: `◆─╮` where a prompt opens its lane, `■─╯` where a commit or merge closes it, and `├─` where a branch leaves main.
3. Node glyphs on top.
4. The selected node gets a halo, and a line in its color runs across to its row.

**Scope.** The chain of open items that contain the selection, for example a branch, then a prompt, then an edit:

- **rails:** each item in the chain draws a rail in its color down the rows it owns
- **tint:** the innermost item's rows get a soft tint
- **fade:** everything outside the innermost item fades

**Row.** Left to right:

```
cursor | gutter | gap | rails | chevron | kind | subject | tail | time
```

The time column drops below 70 columns, and the tail (line counts, outcome) below 55. Rows never wrap.

### Theme

`tui.Theme` maps style tokens to Lip Gloss styles with the paper palette. It applies the band, card and tint backgrounds, and asks the terminal for the paper background through Bubble Tea. Lip Gloss lowers colors to what the terminal supports.

### The TUI loop

`tui.Model` is a Bubble Tea model:

- **Init:** starts a background load and a 500 ms tick.
- **Load:** runs `Load(sync=true)` and `graph.Build` off the UI thread. It records the size and modification time of every watched file before loading, so a write that lands mid-load still triggers the next reload.
- **Tick:** checks whether anything watched changed: transcripts, HEAD, the reflog, packed refs, and the store's hook files and their folder. Every 3 s it also looks for new transcripts, and every 15 s it reloads regardless, because the working tree can change on its own.
- **After a reload:** open items and the selection carry over. If the selection was on the last row, the view follows the newest prompt, like `tail -f`.
- **Keys:** navigation calls the pure functions in `layout/nav.go`. `o` and `O` build a patch and run the viewer through `tea.ExecProcess`, which pauses the UI and hands the terminal over until the viewer exits.
- **Live marker:** the header shows `working` while a tool call is in flight in a transcript that changed in the last 30 s.

### Diff viewer

`viewer.Find` tries `AGENT_BLAME_VIEWER`, then `leaf`, then `$PAGER` or `less -R`. The patch is written to a temporary file that is removed when the viewer exits.

- `o` sends the agent's own hunk.
- `O` sends the whole file as context, from the commit the edit landed in, or from the working tree if it is not committed.

## 9. Walkthrough: one edit, end to end

The agent edits `src/auth.py` while the tree is open.

1. Claude Code appends an `assistant` record with a `tool_use` for Edit, then a `user` record with its `tool_result`.
2. The hook fires in the background: it writes a line with the same tool id and the current HEAD to `events/hook-<session>.jsonl`.
3. Within 500 ms the tick sees the transcript and the hook file have changed, and starts a reload.
4. The loader re-parses that one transcript and rewrites its import file. It merges in the hook line by id, so the event now has both the diff and HEAD.
5. `graph.Build` places the edit under its prompt and checks it against the commit rules. With no commit yet, the file is dirty, so the edit is marked uncommitted.
6. The selection was on the last row, so the view follows: the prompt opens and the new edit is selected.
7. The layout draws the edit with `+3 -1 *` and the agent's why beneath it. The theme colors it.

When the agent later runs `git commit`, the next reload matches the commit to that run by time, and the edit's commit line changes to exact.

## 10. Adding a harness

Supporting another coding agent is mostly a capture task. The model, store, graph and view stay unchanged. Using OpenCode as the example:

| Piece | Where | What it does |
|-------|-------|--------------|
| reader | `sources/opencode` | reads past sessions from `opencode session export` or OpenCode's local SQLite database, and maps them to events |
| ids | `model/id.go` | `oc:<session>:prompt:<id>` and `oc:<session>:tool:<call id>`, so the reader and the hook agree |
| hook | `cli/hook.go` | `agent-blame hook opencode`, the same flow as Claude Code: find the store, read HEAD, append |
| plugin | `plugins/opencode` | an OpenCode 2 server plugin that forwards `session.hook("prompt")` and `tool.hook("execute.after")` payloads to the hook |
| install | `cli/install.go` | `agent-blame install opencode` copies the plugin into OpenCode's plugin folder |
| loader | `load/load.go` | a second finder whose sessions feed the same merge |

The rule to keep is the same one Claude Code follows: both the reader and the hook must produce the same id for the same action.

## 11. Testing

| Layer | How |
|-------|-----|
| transcript parsing | a hand-written synthetic transcript covering every record shape, plus fuzzing |
| hook payloads | payload fixtures per event, a benchmark, and an end-to-end round trip in a temp repo |
| store | concurrent appends from 200 writers, partial lines, blobs, state |
| graph | `testutil` builds a git repo with fixed commit times: one merged PR, one open PR, and exact, inferred, uncommitted and orphaned edits |
| layout | golden text frames at 100 and 60 columns, ASCII glyphs, fold levels, an open edit, plus invariants: no line too wide, exactly one node glyph per row |
| CLI | install against a temp settings file, sync surviving transcript deletion, render without a terminal |

Tests never read real transcripts: they point the transcript folder at an empty temp directory. Golden files refresh with `make golden`.

## 12. Invariants worth protecting

- **Same action, same id, from every source.**
- **The layout never imports the terminal libraries.**
- **The hook always exits 0, never prints, and only writes where a store already exists.**
- **Nothing outside `.git/agent-blame/` is written,** apart from the temp patch file handed to the viewer.
- **Private reasoning is never stored.** Command output is capped.

## 13. Current limits

- Claude Code only. Subagent sessions are skipped.
- Arcs linking related changes are not drawn yet, so `Enter` opens and closes items instead of jumping to a cause.
- Branches are drawn one subtree at a time. Several open branches are listed, not laid side by side.
- Live updates poll every 500 ms instead of using file system notifications.
