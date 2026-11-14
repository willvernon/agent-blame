# Build plan

Detailed plan for building agent-blame. AGENTS.md says what the tool is and how it should look and behave. This file says how to build it, in what order, and how to know each step is done.

Where this file and the draft schema in AGENTS.md differ, this file wins.

## Summary

- Phases 1 to 3 need no hooks and no plugin. Claude Code already writes a live JSONL transcript per session that contains every prompt, tool call, diff and the agent's text. The tool reads that directly.
- Hooks come later (phase 4) as the stable, documented capture path, and to record facts the transcript lacks.
- Leaf is a binary with no library and no plugin API. It is used by shelling out to `leaf diff --patch <file>`.
- The PR level of the tree is really "branch". PR number and title are optional labels on top.
- Stack: Go, with Bubble Tea v2 for the TUI and Lip Gloss v2 for styling (decision D1).
- First usable release (v0.1) is phases 0 to 4 for Claude Code only.

## Status

Phases 0 to 4 are built: the v0.1 scope, for Claude Code only. Phases 5 to 8 are not started.

Where the build differs from this plan:

- Live updates poll every 500 ms instead of using fsnotify. Polling covers transcripts, HEAD, the reflog and the store's hook files, and was fast enough on real data. Revisit only if it shows up in CPU use.
- Transcript imports are written per session as `events/import-<session>.jsonl` and rewritten when the transcript changes, instead of appended by byte offset. Hook events are appended to `events/hook-<session>.jsonl` under a lock. Both merge by id when loading.
- The hook records only repositories that already have a store. Opening the tree or running `sync` creates it. This keeps a globally installed hook from writing into every repository.
- A hook payload that cannot be parsed is dropped silently instead of logged: without a working directory there is no store to log into. Failures after the store is found do go to `errors.log`.
- An exact commit match also comes from a `git commit` run whose commit time falls inside the run. This works with `-q`, which prints no hash.
- Prompts carried over from another machine can share one `promptId`. A repeated id falls back to the record's own `uuid`.
- Run rows show the agent's one-line description of the command when there is one. The command itself is in the detail block.
- When the selection is on the last row, the tree follows new activity.
- `Enter` opens and closes the selected item. Jumping to the cause comes with the links in phase 5.
- A `render` command prints one frame without a terminal. It is how layouts are checked and how the golden files are made.
- Golden files refresh with `UPDATE_GOLDEN=1` (`make golden`) instead of an `-update` flag, so it works across all packages.
- `.` shows or hides searches, web fetches and other tools, which are hidden by default.

Measured on 2026-11-14:

- `dump` on this repository: 0.24 s for 129 events.
- `sync`: 0.06 s.
- Hook process: median 4.0 ms, 95th percentile 5.2 ms.
- A hook event appears in an open tree within about a second.
- Fuzzing the transcript parser for 25 s found no crashes.

## 1. Verified facts

Checked on 2026-11-14 against Claude Code 2.1.309, OpenCode 2.3.0, Leaf 0.15.2, git 2.54. Re-check when versions move.

### Leaf

| Fact | Detail |
|------|--------|
| Language and stack | Rust (edition 2024), ratatui 0.30, crossterm 0.29 |
| License | MIT |
| Crate shape | binary only (`[[bin]] leaf`), no library target |
| Extension surface | none. "plugin" in Leaf means installing Leaf into the Rex terminal's command palette |
| Diff input | `leaf diff --patch <file>`, or any path ending in `.diff` or `.patch`. No stdin |
| Other diff inputs | `--range <spec>`, `--staged`, `--branch`, `--base <ref>`, a path, a revision, a GitHub URL |
| Layout | side by side by default, `--inline` for one column |
| Theme | follows the terminal. `LEAF_THEME=light` or `dark` forces it |
| Review comments | saved per diff, readable with `leaf comments --json` |

### Claude Code transcripts

Location: `~/.claude/projects/<cwd-slug>/<session-id>.jsonl`, one JSON record per line, appended live.

| Fact | Detail |
|------|--------|
| Record types that matter | `user`, `assistant` |
| Record types to skip | `attachment`, `api-request`, `api-request-blob`, `permission-mode`, `last-prompt`, and any unknown type |
| Fields on every user and assistant record | `uuid`, `parentUuid`, `sessionId`, `timestamp`, `cwd`, `gitBranch`, `isSidechain` |
| A real prompt | `type=user` with `promptSource` set (`typed`, `sdk`, `suggestion_accepted`), `turnOrigin=human`, and a `promptId` |
| Assistant records | one content block per record (`text`, `thinking` or `tool_use`). Blocks from one model response share `message.id` |
| Tool call | `tool_use` block with `id`, `name`, `input` |
| Tool result | `type=user` record whose content holds a `tool_result` with the matching `tool_use_id`, plus a structured `toolUseResult` |
| Edit result | `filePath`, `oldString`, `newString`, `originalFile`, `structuredPatch`, `userModified` |
| Write result | same, plus `type` (create or update) and `content` |
| `structuredPatch` items | `oldStart`, `oldLines`, `newStart`, `newLines`, `lines` |
| Bash input | `command`, `description` (a one-line statement of intent, written by the agent) |
| Bash result | `stdout`, `stderr`, `interrupted`. No exit code field |
| Failure signal | `is_error` on the `tool_result` block (true, false or absent) |
| Subagents | `<slug>/<session-id>/subagents/agent-<id>.jsonl` with `agentId` on each record. The parent's Agent tool result carries the same `agentId` |
| Stability | undocumented. Field sets vary between records and versions. Parse defensively |
| Retention | transcripts are cleaned up after a retention period (`cleanupPeriodDays`), so they are not a permanent record |

### Claude Code hooks

| Fact | Detail |
|------|--------|
| Common input | `session_id`, `transcript_path`, `cwd`, `permission_mode`, `hook_event_name`, and `prompt_id` |
| `UserPromptSubmit` | adds `prompt` (as typed, not the expanded command) |
| `PostToolUse` | adds `tool_name`, `tool_input`, `tool_response`, `tool_use_id`, optional `duration_ms` |
| Bash `tool_response` | `stdout`, `stderr`, `interrupted`, and when files changed in a git repo, `bashEditDiff` with `changedFiles` (2.1.269 or later, best effort) |
| `PostToolUseFailure` | adds `error` (for Bash it starts with the exit code), `is_interrupt` |
| `SessionStart` | adds `source` (startup, resume, clear, compact, fork). Its stdout is added to the agent's context |
| Subagent context | `agent_id`, `agent_type` on events fired inside a subagent |
| Background hooks | `"async": true` runs a command hook without blocking the agent |
| Plugin packaging | `hooks/hooks.json` inside the plugin. `${CLAUDE_PLUGIN_ROOT}` and `${CLAUDE_PLUGIN_DATA}` are available |
| Not yet verified | the `tool_response` shape for Edit in hooks, and the inputs for `Stop`, `SubagentStop`, `SessionEnd` |

### OpenCode

| Fact | Detail |
|------|--------|
| Version 2 plugin API | `Plugin.define({ id, async setup(ctx) { ... } })`. The version 1 hook names do not carry over |
| Prompt hook | `ctx.session.hook("prompt", e => e.prompt.text)` |
| Tool hooks | `ctx.tool.hook("execute.before", e => e.tool, e.input)` and `ctx.tool.hook("execute.after", e => e.status, e.result)` |
| Plugin locations | `.opencode/plugins/` in the project, `~/.config/opencode/plugins/` globally |
| Local store | SQLite at `~/.local/share/opencode/opencode.db` |
| Tables that matter | `session_v2` (`id`, `directory`, `parent_id`, times), `session_message` (`session_id`, `type`, `seq`, `data` as JSON) |
| Message types seen | `user`, `assistant`, `system`, `synthetic`, `idle`, `compaction`, `agent-switched`, `model-switched` |
| Public export | `opencode session export` prints a session as JSON |
| Not yet verified | the JSON shape inside `session_message.data`, and whether tool hook events carry a session id |

### git

- Everything needed is available from the `git` CLI. Leaf itself shells out to git.
- Worktrees share one common directory (`git rev-parse --git-common-dir`). That is where local state belongs.
- A squash merge discards the branch's commit hashes. Branch name and time are the only join keys that survive it.

## 2. Decisions

Each decision lists why, and how hard it is to undo.

### D1. Stack: Go, Bubble Tea v2, Lip Gloss v2 (decided)

| Option | For | Against |
|--------|-----|---------|
| Go + Bubble Tea v2 (chosen) | one static binary, millisecond startup for the hook command, fast builds for the TUI tweak loop, goroutines for following many files, pure Go SQLite for OpenCode | Bubble Tea renders strings, not a cell grid, so our own layout code builds the gutter as styled text |
| Rust + ratatui | same stack as Leaf, cell-grid rendering with a test backend | slower builds. Its main draw, reusing Leaf code, does not apply because Leaf has no library |
| Python + Textual | fastest to prototype | needs a Python environment everywhere, slow startup for the hook |
| TypeScript + OpenTUI on Bun | same language as the OpenCode plugin | needs Bun at runtime, younger library |

Facts this rests on:

- Go 1.27 is the installed toolchain.
- Bubble Tea, Lip Gloss and Bubbles reached stable v2 in February 2026. Import paths live under `charm.land/<name>/v2`.
- v2 renamed some v1 types, including the key message types and the value `View` returns. Follow the v2 docs, not older v1 examples.
- Inside a Bubble Tea v2 program, Lip Gloss v2 downsamples colors to what the terminal supports.

Undo cost: low until phase 3.

### D2. Capture: transcript first, hooks second

- Phases 1 to 3 read transcripts only. Zero setup, and it works on sessions that already happened.
- Phase 4 adds hooks because they are a documented contract, and because they carry what transcripts lack: HEAD at the moment of each event, files changed by shell commands, and exit codes.
- Both paths must produce the same event for the same action. See D3.

Undo cost: none. The two paths are independent adapters.

### D3. Deterministic event ids

The id is derived from the source, never random:

```
cc:<session>:prompt:<promptId>
cc:<session>:tool:<tool_use_id>
oc:<session>:msg:<message id>:<ordinal>
git:<commit sha>
branch:<name>
```

Importing a transcript twice, or seeing the same tool call from both a hook and a transcript, yields the same id. Merging is then a field-wise union with no duplicate rows.

Undo cost: high once logs exist. Get this right in phase 1.

### D4. Storage: append-only JSONL in the git common directory

```
<git-common-dir>/agent-blame/
  events/<source>-<session>.jsonl   normalized events, one per line
  blobs/<sha256>                    hunks and outputs larger than 4 KB
  state.json                        schema version, import offsets per transcript
  errors.log                        hook and import failures
```

- Nothing in this directory can be committed or pushed, because it lives inside `.git`.
- One writer rule: append one complete line while holding an exclusive advisory lock. Readers ignore a trailing partial line.
- The graph is rebuilt in memory on start. No database until measurements say one is needed.
- Links and lanes are derived at load time, not stored. Only links the agent declared are stored.

Undo cost: medium. Keep all reads and writes behind `store::`.

### D5. Diff viewer: shell out, behind an interface

```go
type Viewer interface {
    // Command builds the process that shows the change. Cleanup runs after it exits.
    Command(req PatchRequest) (cmd *exec.Cmd, cleanup func(), err error)
}
```

Order tried: the configured viewer, then Leaf, then the git pager, then the built-in inline view.

For Leaf: write the patch to a temp file and return `leaf diff --patch <file>` with `LEAF_THEME=light` in its environment, so it matches the paper theme. The TUI runs it with Bubble Tea's `tea.ExecProcess`, which suspends the program, hands the terminal to Leaf, and resumes when Leaf exits. The cleanup removes the temp file.

Undo cost: none.

### D6. git through the CLI, not a library

Subprocess calls with machine-readable output (`--porcelain`, `-z`, `--format`). Simpler, matches Leaf, and avoids a heavy dependency.

### D7. One binary, subcommands

```
agent-blame                  open the TUI for the repo in the current directory
agent-blame dump             print normalized events as text or JSON
agent-blame sync             import new transcript records into the store
agent-blame hook <source>    read one hook payload on stdin, append one event
agent-blame install <source> install or remove the capture plugin
agent-blame doctor           report what was found, what is missing, and why
agent-blame export           write a shareable, redacted bundle
```

### D8. Branch is the unit, PR is a label

Every transcript record carries `gitBranch`. Events are grouped by branch. A branch becomes a "PR" row when a merge or `gh` supplies a number and title. Work done directly on the default branch hangs off main with no PR row.

### D9. Lanes: one per prompt first, several later

The static TUI in phase 3 draws each prompt's work in a single lane. Parallel lanes need the `from` links from phase 5, so they arrive then.

## 3. Repository layout

One Go module. Everything except the entry point lives under `internal/`, so nothing is importable from outside until we choose to expose it.

```
go.mod                        module path set once the remote exists
Makefile                      check, build, install
cmd/agent-blame/main.go       subcommand dispatch
internal/
  cli/                        dump.go sync.go hook.go install.go doctor.go export.go
  model/
    event.go                  Event, Kind, Note, schema version
    id.go                     deterministic ids (D3)
    graph.go                  Graph: nodes, parent tree, derived links
  sources/
    source.go                 Source interface
    claudecode/
      discover.go             find transcripts that belong to this repo
      transcript.go           tolerant JSONL reader, incremental by byte offset
      mapping.go              records to events
      hook.go                 hook payload to event
    opencode/
      export.go               session export JSON to events
      db.go                   read-only SQLite follow
    git/
      spine.go                default branch, first-parent history, merges
      branches.go             branch and PR inference, optional gh enrichment
      attribute.go            which commit an edit landed in
      worktree.go             common dir, worktree list
  store/                      log.go blobs.go state.go lock.go
  derive/                     notes.go links.go lanes.go
  tui/
    model.go                  Bubble Tea model: graph plus view state
    update.go keys.go watch.go
    layout/                   rows.go gutter.go arcs.go scope.go detail.go
    view.go theme.go glyphs.go
  viewer/                     viewer.go leaf.go pager.go inline.go
  testutil/                   scripted repo builder shared by tests
plugins/
  claude-code/                .claude-plugin/plugin.json, hooks/hooks.json
  opencode/                   version 2 server plugin, the one TypeScript part
docs/
  build-plan.md
```

Test fixtures live in each package's `testdata/` directory, which the Go toolchain skips when building.

Import rule: `tui` imports `model` and `derive`. `sources` import `model`. Nothing imports `tui`. A test in `internal/` checks this with `go list -deps`.

Dependencies, kept small:

| Need | Package |
|------|---------|
| TUI loop | `charm.land/bubbletea/v2` |
| styling and width measurement | `charm.land/lipgloss/v2` |
| key help, viewport (only if useful) | `charm.land/bubbles/v2` |
| file watching | `github.com/fsnotify/fsnotify` |
| OpenCode database, phase 6 only | `modernc.org/sqlite` (pure Go, no C toolchain) |
| everything else | standard library: `flag`, `encoding/json`, `os/exec`, `log/slog`, `testing` |

## 4. Data model

### Event (schema version 1)

```
Event {
  v:        1
  id:       Id                       // D3
  ts:       timestamp, UTC
  source:   claude-code | opencode | git
  session:  string | null
  agent:    string | null            // subagent id, null for the main thread
  kind:     prompt | read | edit | run | commit | branch | other
  tool:     string | null            // original tool name, kept for kind=other
  branch:   string | null
  cwd:      path relative to the repo root
  parent:   Id | null                // the prompt for tool events, the branch for prompts and commits
  subject:  string                   // file path, command, prompt text or commit subject
  stat:     { add, del } | null
  ok:       bool | null              // runs only. null means unknown
  hunk:     inline text | blob ref | null
  head:     sha | null               // HEAD when the event happened. hooks only
  commit:   { sha, how: exact | inferred } | null
  note:     { change, why, origin: declared | transcript | none }
  declared_links: [ { to, type } ]   // only what the agent stated
}
```

### Claude Code record to event

| Record | Event | Notes |
|--------|-------|-------|
| first `user` record of a `promptId` that has `promptSource` | prompt | subject is the prompt text. Skip records with `isSidechain=true` unless importing subagents |
| `tool_use` Read | read | subject is `input.file_path` |
| `tool_use` Edit, Write, MultiEdit, NotebookEdit | edit | hunk from `structuredPatch`, stat counted from its lines |
| `tool_use` Bash | run | subject is the command. `note.change` is `input.description`. `ok` is the negation of `is_error` |
| `tool_use` Agent or Task | other, plus a subagent span | children come from the subagent transcript, joined on `agentId` |
| any other `tool_use` | other | stored, hidden by default |
| `text` records sharing `message.id` with a `tool_use`, placed before it | `note.why` of that event | capped at 600 characters |
| `thinking` records | nothing | never stored |

A tool event is created when its `tool_use` is seen and completed when the matching `tool_result` arrives. An event with no result yet is "in flight" and drawn as the live tail.

### Run classification

Runs are tagged by a small rule table so tests can show a pass or fail badge:

```
test    pytest | cargo test | go test | npm test | bun test | jest | vitest | rspec
build   cargo build | npm run build | make | tsc
git     ^git (commit, checkout, merge, rebase, push)
other   everything else
```

### Which commit did an edit land in

Tried in order. The first that matches wins, and the method is recorded.

1. exact: a run whose command is `git commit` and whose stdout names a sha. All edits in that session since the previous commit on that branch, whose paths are in that commit, belong to it.
2. exact: `head` changed between two hook events. Same assignment.
3. inferred: the first commit on the same branch, at or after the event time, that touches the path.
4. none: if the path differs from HEAD the edit is uncommitted. Otherwise it is an orphan (written, then overwritten or reverted before any commit) and is drawn dimmed.

### Branch and PR inference

```
default branch     origin/HEAD, else main, else master
trunk              git log --first-parent <default>
merged branch      a two-parent commit on trunk. commits are parent1..parent2
PR number          subject matches "Merge pull request #N" or ends with "(#N)"
squash merge       one commit on trunk ending "(#N)". join events by branch name and time window
open branch        a local branch that is not an ancestor of the default branch
direct work        events whose branch is the default branch
enrichment         gh pr list --state all --json number,title,state,headRefName,mergeCommit   (opt-in)
```

## 5. Phases

Sizes are relative: S is a sitting, M is a few sittings, L is a week or more of sittings.

### Phase 0. Scaffold (S)

Goal: an empty project that builds, tests and lints.

- [ ] `go mod init`, with the `go` directive at the installed toolchain version
- [ ] MIT license file, `.gitignore` for build output
- [ ] `cmd/agent-blame/main.go` with a subcommand switch over the standard `flag` package
- [ ] version string set at build time with `-ldflags "-X main.version=..."`
- [ ] `make check` runs `gofmt -l` (must print nothing), `go vet ./...`, `staticcheck ./...` and `go test -race ./...`
- [ ] CI runs `make check` on macOS and Linux
- [ ] a note in `internal/testutil` stating that only synthetic data may be committed as test data
- [ ] `agent-blame --version` runs

Done when: a fresh clone passes `make check`.

### Phase 1. Read one transcript (S)

Goal: prove the data is there. `agent-blame dump` prints the events of this repo's sessions.

- [ ] `discover`: list `~/.claude/projects/*/*.jsonl`, read the first record that has `cwd`, keep sessions whose `cwd` is inside the repo root or one of its worktrees
- [ ] `transcript`: line reader that skips unknown record types, tolerates missing fields, and stops cleanly on a partial last line
- [ ] `map`: the record to event table in section 4, including pairing `tool_use` with `tool_result`
- [ ] `id`: deterministic ids
- [ ] `dump`: one line per event (time, kind, subject, stat, ok), plus `--json`
- [ ] synthetic fixture transcript covering: two prompts, a read, an edit, a write, a failing run, a passing run, an unknown record type, a truncated last line
- [ ] golden file test of `dump` over the fixture
- [ ] fuzz test for the transcript line parser (`go test -fuzz`)

Done when:

- `agent-blame dump` in this repo lists the real sessions that touched it, in time order
- importing the same transcript twice produces identical output
- no record type or missing field causes a panic

Risk: the format changes between Claude Code versions. Mitigation: every field access is optional, and `doctor` reports the count of skipped and unparsed records.

### Phase 2. Join with git (M)

Goal: a complete in-memory graph with the main, branch, prompt, event hierarchy.

- [ ] `worktree`: common dir, repo root, worktree list
- [ ] `spine`: trunk commits and merges
- [ ] `branches`: the inference table in section 4, without `gh`
- [ ] `attribute`: the four-step commit assignment
- [ ] commits with no agent events appear on their branch as plain commit rows
- [ ] `graph`: parent tree (main, branch, prompt, event), stable ordering by time
- [ ] `dump --tree`: indented text view of the hierarchy
- [ ] scripted fixture repo that reproduces the reference scenario: branch 41 merged with one prompt, branch 42 open with three prompts and an uncommitted tail
- [ ] snapshot test of `dump --tree` over that scenario

Done when:

- the reference scenario prints the same hierarchy as the final mockup
- every edit is marked exact, inferred, uncommitted or orphan
- a repo with no branches and work directly on main still renders

Risk: squash merges and rebases break hash-based joins. Mitigation: branch name plus time window is the fallback, and the result is marked inferred.

### Phase 3. Static TUI (L)

Goal: the interface in AGENTS.md, read-only, over the phase 2 graph. Build in slices so there is something to look at after each one.

The split that keeps this testable, mapped onto Bubble Tea's Model, Update and View:

```
Update   takes a key, resize or watch message and returns the new ViewState
View     calls layout.Build(graph, viewState, width, height), gets []Line, joins it into the frame
```

`layout` imports neither Bubble Tea nor Lip Gloss. It returns lines of spans, each span a piece of text plus a style token such as `kind.edit` or `muted`. `view.go` maps tokens to Lip Gloss styles. Golden files compare text and tokens, so tests do not depend on terminal colors.

3a. Rows and movement (S)

- [ ] Bubble Tea program in the alternate screen. The model holds the graph and the view state. Update handles keys and window size
- [ ] View builds only the rows inside the visible window, never the whole list
- [ ] flat list of visible rows with kind, subject, stat, time
- [ ] `j` `k` `g` `G`, scrolling that keeps the selection on screen
- [ ] header and footer bars

3b. Expansion (S)

- [ ] per-item expanded set, `l` and `h` semantics, `1` `2` `3` fold levels
- [ ] hidden rows resolve to their nearest visible ancestor

3c. Gutter (M)

- [ ] lane assignment: trunk 0, branch 1, prompt work 2
- [ ] connectors drawn on the node's own row, so branching costs no extra lines
- [ ] glyph table with a Unicode set and an ASCII fallback (section 6)
- [ ] dashed trunk below the last merge while a branch is open

3d. Detail block (S)

- [ ] virtual rows under an expanded event: change, note, hunk, from
- [ ] hunk coloring, long lines truncated with an ellipsis

3e. Scope (M)

- [ ] rail column per nesting level, in the color of the item that owns the scope
- [ ] tint behind the innermost scope, muted foreground outside it
- [ ] status bar names the current scope

3f. Theme (S)

- [ ] paper theme tokens (section 6), applied as an explicit background so it holds on dark terminals
- [ ] truecolor, 256-color and 16-color output, left to Lip Gloss v2's downsampling. Check by eye that the 16-color result is still readable
- [ ] `NO_COLOR` respected

3g. Viewer handoff (S)

- [ ] `PatchRequest` builder: a unified diff from `structuredPatch` for `o`, and a full-context diff for `O`
- [ ] Leaf, pager and inline viewers behind the `Viewer` interface, run with `tea.ExecProcess`
- [ ] the TUI resumes cleanly when the viewer exits with an error or is killed

Done when:

- a snapshot of the reference scenario at 100 columns matches the agreed target rendering
- every key in AGENTS.md works
- 5,000 events open in under 300 ms and scroll without visible lag
- resizing to 60 columns degrades by dropping the time column, then the stat column, never by wrapping

Risk: glyph widths differ by font. Mitigation: `doctor` prints a glyph test row, and `glyphs = "ascii"` is one config line away.

### Phase 4. Live and persistent (M)

Goal: the tree updates while the agent works, and history survives transcript cleanup. This completes v0.1.

- [ ] `store`: layout from D4, an exclusive `flock` around each append (macOS and Linux), blobs, tolerant reader
- [ ] `sync`: import new transcript bytes since the stored offset. Idempotent
- [ ] TUI runs `sync` on start
- [ ] `watch`: fsnotify on the repo's transcripts and on `HEAD`, `refs` and `logs/HEAD`, debounced, with polling as a fallback. A goroutine feeds a channel, and a `tea.Cmd` that waits on that channel turns each batch into a message
- [ ] incremental graph update that keeps the selection and scroll position
- [ ] live tail row and an "agent working" marker on the active branch
- [ ] `hook claude-code`: parse stdin, read HEAD from files (no git subprocess), append one event, always exit 0
- [ ] `plugins/claude-code`: `UserPromptSubmit`, `PostToolUse`, `PostToolUseFailure`, `SubagentStop`, all with `"async": true`
- [ ] merge rule when both paths saw an event: union of fields, hook wins for `head` and exit code, transcript wins for `hunk` and `note.why`
- [ ] `install claude-code` and `doctor`

Done when:

- an edit made by a running session appears in the TUI within one second
- deleting the transcript file loses nothing already synced
- the hook adds no perceptible delay (it is async) and finishes in under 20 ms at the 95th percentile
- a malformed hook payload is logged to `errors.log` and never surfaces in the agent session

Risk: two hook processes append at once. Mitigation: the lock in D4, and a test that fires 200 concurrent appends and checks for 200 intact lines.

### Phase 5. Notes and links (M)

Goal: the "why" on each change and the arcs between changes.

Notes, in order of preference per event:

1. declared: a line beginning `Why:` in the agent's text before the tool call. Opt-in, enabled by a `SessionStart` hook that asks the agent to write one
2. transcript: the agent's text before the tool call (already captured in phase 1)
3. none: the detail block shows the hunk only

Links, all computed at load and marked inferred:

| Link | Rule |
|------|------|
| `from` for an edit on path P | the latest read or edit of P earlier in the same prompt, else the latest failing run before it in the prompt, else the prompt |
| `from` for a run | the latest edit before it in the prompt, else the prompt |
| revises | an edit on P whose line range overlaps an earlier edit on P, in any earlier prompt or branch |
| affects | a failing run, linked back to earlier edits whose paths appear in its output, or that changed since the last passing run of the same command |

- [ ] `internal/derive/notes.go` and `internal/derive/links.go`, with one table-driven test per rule
- [ ] lanes from the `from` forest: each chain within a prompt is a lane, capped at three, extras folded into the nearest
- [ ] arc layout (section 6)
- [ ] `Enter` follows `from`. The detail block gains the links field with jump targets
- [ ] selecting a row darkens its path to the prompt and highlights its arcs
- [ ] collapsed items keep their arcs, re-anchored to the visible ancestor
- [ ] inferred links drawn more quietly than declared ones

Done when: in the reference scenario the failing test run arcs back to the middleware edit from the previous prompt, and both revisions in the third prompt arc back to the edits they replace.

Risk: heuristics produce false links. Mitigation: origin is always shown, and a config switch hides inferred links entirely.

### Phase 6. OpenCode adapter (M)

Goal: OpenCode sessions appear in the same tree.

- [ ] spike: run `opencode session export` on one session and write down the shape of messages, tool calls and diffs. Update section 1
- [ ] `internal/sources/opencode/export.go`: export JSON to events, for backfill
- [ ] `internal/sources/opencode/db.go`: read-only follow of `session_message` by `seq` for live mode, filtered by `session_v2.directory`
- [ ] `plugins/opencode`: version 2 server plugin using the `prompt` and `execute.after` hooks, which calls `agent-blame hook opencode`
- [ ] a source badge on rows when a repo has events from more than one harness

Done when: a session run in OpenCode in the fixture repo renders with the same structure as the equivalent Claude Code session.

Risk: the database schema is internal and may change. Mitigation: prefer export and the plugin, and treat the database reader as an optional accelerator behind a config switch.

### Phase 7. Depth (M, optional for 1.0)

- [ ] subagents as a nested level under the step that spawned them
- [ ] several open branches at once, with a focus mode that collapses the others to one row each
- [ ] arcs across branches
- [ ] `/` filter by path, kind or text, and `n` `N` to step through matches
- [ ] `gh` enrichment for PR numbers and titles, opt-in
- [ ] dark theme

### Phase 8. Ship (S)

- [ ] decide the command name
- [ ] README with a screenshot, install steps and the privacy model
- [ ] `go install`, then prebuilt binaries for macOS and Linux (GoReleaser), then a Homebrew tap
- [ ] `export`: a redacted bundle (paths kept, prompt text and hunks optional)
- [ ] changelog and a tagged release

## 6. Rendering reference

### Target text rendering

Connectors share the row of the node they leave or join. Arcs live in their own gutter columns to the right of the lanes.

```
◎            main    9c2e1f0  production
├─⬢          pr      #42 login rate limiting          open
│ ◆─╮        prompt  add rate limiting to /login
│ │ ○        read    src/routes/auth.py
│ │ ●┄╮      edit    src/middleware/rate_limit.py     +48
│ │ ● ┆      edit    src/routes/auth.py               +6 -1
│ ■─╯ ┆      commit  a1f3c9e add login rate limiter
│ ◆─╮ ┆      prompt  tests are failing, fix it
│ │ ▸┄╯      run     pytest                           3 failed
│ │ ●        edit    src/middleware/rate_limit.py     +5 -2
┆ ▌          agent working
```

### Glyphs

| Kind | Unicode | Code point | ASCII |
|------|---------|------------|-------|
| main | ◎ | U+25CE | @ |
| pr | ⬢ | U+2B22 | # |
| prompt | ◆ | U+25C6 | * |
| read | ○ | U+25CB | o |
| edit | ● | U+25CF | + |
| run | ▸ | U+25B8 | > |
| commit | ■ | U+25A0 | = |
| lanes | │ ├ ╮ ╯ ─ | box drawing | pipe, plus, backslash, slash, hyphen |
| arcs | ┄ ┆ | box drawing, dashed | period, colon |

Every glyph must measure one cell wide. Check with `lipgloss.Width`, so measuring and rendering agree, and by eye in the target font.

### Gutter layout

```
for each visible row r, in time order:
    lane(r)   = 0 for main, 1 for branch-level rows, 2+ for prompt work
    active    = lanes that have a node above and a node below r
    cells     = for each lane: node glyph if lane == lane(r)
                               vertical if lane in active
                               blank otherwise
    connector = if r opens a lane to its right: horizontal + down-turn corner on r
                if r closes a lane to its right: horizontal + up-turn corner on r
```

### Arc layout

```
arcs     = links whose two ends resolve to different visible rows
order    = shortest span first
column   = first arc column free over the whole span (interval coloring)
cap      = 3 columns. beyond that, draw only arcs that touch the selection
style    = dashed, muted. solid accent color when one end is selected
```

### Scope

```
chain  = expanded ancestors of the selection, outermost first
rail   = one cell per chain entry, colored by that item's kind, from its row to the last row it owns
tint   = background blend of the innermost item's color at about 11 percent, truecolor only
fade   = rows outside the innermost scope use the muted foreground
```

Fade uses an explicit color, not the terminal's dim attribute, because dim renders inconsistently.

### Paper theme tokens

| Token | Hex | Token | Hex |
|-------|-----|-------|-----|
| background | #faf8f3 | ink | #45423b |
| bar | #f5f2ea | rule | #e6e2d6 |
| selection | #ebe6d9 | muted | #a39e90 |
| timestamp | #b3ae9f | hot path | #6f6a5e |
| trunk | #a8a290 | branch lane | #c4bead |
| work lane | #ddd8ca | arc idle | #cfc8b4 |
| added | #5c8a5e | removed | #b0584f |

| Kind | Node | Text |
|------|------|------|
| main | #2f2c27 | #2f2c27 |
| pr | #4a9a8c | #3d7f73 |
| prompt | #c67a4a | #a8623d |
| read | #5b8db8 | #44709a |
| edit | #8a6fb5 | #72589e |
| run | #cfa13d | #96701c |
| commit | #7d786b | #5f5b51 |

## 7. Testing

| Layer | Method |
|-------|--------|
| transcript parsing | synthetic JSONL fixtures, golden file of `dump` |
| parser robustness | native Go fuzzing of the transcript and hook payload parsers |
| git inference | `internal/testutil` builds a throwaway repo per scenario (merge, squash, rebase, direct to main, worktree) |
| layout | golden files of `layout.Build` output at fixed sizes, refreshed with an `-update` test flag |
| derived links | one unit test per rule, plus the reference scenario end to end |
| store | concurrent append test, partial line test, schema version test |
| hook | payload fixtures per event type, latency benchmark |
| viewer | a fake viewer binary that records its arguments |

Layout invariants worth property tests:

- every visible row has exactly one node glyph
- no two arcs share a cell
- a scope rail never extends past the last row its item owns
- collapsing and re-expanding an item returns the identical grid

Real transcripts never enter the repo. Fixtures are written by hand or generated.

## 8. Privacy checklist

Applied at every phase review.

- [ ] no network access outside the opt-in `gh` enrichment and `leaf` itself
- [ ] all state is under the git common directory, and nothing is written into the working tree
- [ ] `thinking` content is never stored
- [ ] run output is capped (first and last 2 KB) before it is stored
- [ ] reads of files matched by a deny list (`.env`, keys, credentials) store the path only
- [ ] `export` is the only way data leaves the machine, and it lists what it will include before writing
- [ ] error logs contain event ids and field names, never payload content

## 9. Risks

| Risk | Likelihood | Effect | Response |
|------|------------|--------|----------|
| transcript format changes | high | import breaks or drops fields | tolerant parser, `doctor` counts, hooks as the stable path |
| transcripts cleaned up before sync | medium | history lost | sync on every start, and hooks write directly to the store |
| glyphs render at the wrong width | medium | gutter misaligns | ASCII set, glyph test in `doctor` |
| inferred links mislead | medium | trust in the tool drops | always show origin, switch to hide inferred links |
| squash merges and rebases | high | events detach from commits | branch and time fallback, mark as inferred |
| Leaf changes its flags | low | `o` stops working | `Viewer` interface and fallbacks |
| OpenCode internals change | medium | adapter breaks | prefer export and plugin over the database |
| large sessions | low | slow start | byte-offset import, blobs for big payloads, measure before optimizing |
| shell commands edit files unseen | medium | missing edit rows | `bashEditDiff.changedFiles` from hooks, and a working tree diff after runs |

## 10. Open questions

- Command name. `agent-blame` is the working name.
- Should Grep, Glob and web fetches appear as rows by default, or stay hidden as "other".
- Declared notes change agent behavior and cost tokens. Default off or on.
- Review loop: Leaf saves diff comments and exposes them as JSON. Feeding those back to the agent as the next prompt would close the loop, but it is a second product.
- Agent Trace: emit records for line-level attribution alongside the event log, or stay separate.
- When running inside Rex, open the diff in a split pane instead of suspending the TUI.

## 11. Next actions

1. Phase 0.
2. Phase 1, ending with `agent-blame dump` run against this repo's own sessions.
3. Review the dump output against a real session before starting phase 2. That review is the checkpoint that the model in section 4 fits real data.
