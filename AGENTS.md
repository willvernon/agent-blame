# agent-blame

Brief for anyone, human or agent, working in this repo.

agent-blame (working name) is a terminal UI you run at the root of a git repository. It shows what coding agents did to the codebase as a live branching graph: every prompt, every file read and edit, every command, every commit, and the reasoning behind each change. Think git blame or lazygit, but for agent actions.

## Goals

- Show the exact trail of events, in time order, at a glance.
- Make ownership obvious: which PR and which prompt every change came from.
- Show how changes connect, including when one change affects or revises a later one.
- Let people spend less time combing through changes and more time solving problems.

Design principle: lean on preattentive cues (shape, color, position) so patterns register before anything is read. Keep the palette small so each cue still stands out.

## The model

The graph is rooted at main (production). Each level hangs off the one above it:

```
main                  production trunk
  PR                  branches off main, merges back when it lands
    prompt            what the human asked for, in order on the PR's lane
      read            a file the agent looked at
      edit            a file the agent changed
      run             a command the agent ran (tests, builds)
      commit          where the prompt's work landed
        note          why the agent did it, attached to any event
```

## Interface

### Layout

- One vertical timeline, oldest at the top, newest at the bottom.
- A graph gutter on the left, one row per item on the right.
- Each row shows: kind, subject (file, command, prompt text or commit), a stat (+/- lines, pass or fail) and a timestamp.
- Live mode watches for new events and appends them as the agent works, with a working marker at the tail of the active PR.

### Graph

- main is the heavy trunk in the leftmost lane. It continues dashed past the bottom while a PR is still open.
- A PR branches off main and merges back into it when it lands.
- Prompts sit in sequence on their PR's lane.
- Each prompt's work branches off the prompt and merges back at its commit. Parallel lines of work get their own lanes.
- Dashed "affects" arcs connect an event to a later event it caused or that revised it, including across prompts.
- Selecting an event darkens its causal path back to its prompt and highlights its arcs.

### Action encoding

Every kind gets both a shape and a color, so the encoding still works in a 16-color terminal and for colorblind users.

| Kind    | Shape        | Color     |
|---------|--------------|-----------|
| main    | target ring  | ink       |
| pr      | hexagon      | teal      |
| prompt  | diamond      | orange    |
| read    | hollow ring  | blue      |
| edit    | filled dot   | violet    |
| run     | triangle     | amber     |
| commit  | square       | gray      |
| affects | dashed arc   | muted red |

Reads are hollow because nothing changed.

### Expansion and scope

- Items expand one at a time, like a file tree. A PR opens to its prompts and commits, a prompt opens to its events, an event opens to its detail block.
- The detail block for an event shows:
  - change: what it did
  - note: why it did it
  - hunk: the diff
  - from: the earlier event that caused it, as a jump target
  - links: affects arcs in and out, as jump targets
  - open: actions to view the diff
- Scope rails: every expanded item you are inside gets a vertical rail in its own color, running from that item down to the last row it owns. Rails nest left to right (PR, then prompt, then event).
- The innermost expanded scope gets a soft tint behind its rows, and rows outside it fade back. A short connector ties the tint to its node in the graph. The status bar names the current scope.
- A rail can grow downward when a scope opens. Keep motion optional.
- Collapsed items keep their arcs. An arc from a hidden child is drawn from its nearest visible ancestor, so you can still see that something inside caused trouble later.

### Keys

Vim motions throughout.

| Key       | Action                                             |
|-----------|----------------------------------------------------|
| j, k      | move down, up                                      |
| g, G      | first row, last row                                |
| l         | expand the selected item, or step into it          |
| h         | collapse the selected item, or move to its parent  |
| 1, 2, 3   | fold everything to PRs, prompts, or events         |
| Enter     | open or close the selected item (jump to cause in phase 5) |
| o         | open this change's diff                            |
| O         | open the diff in whole-file context                |
| .         | show or hide searches, fetches and other tools     |
| r         | reload                                             |
| q         | quit                                               |

### Look

- Soft and easy on the eyes for long reading sessions.
- Warm paper background, dark warm gray ink, faint rules, muted diff colors, generous line height.
- Hierarchy by lightness: timestamps, kind labels and reads recede. File names and the open detail block carry the weight.
- Font: Berkeley Mono when the user has it, any monospace otherwise. The terminal owns the font, so the TUI ships none.

## Architecture

- One adapter per source normalizes raw events and appends them to a local event log.
- The TUI reads only the event log and git. It never talks to a harness directly, so supporting a new harness means writing an adapter, not touching the UI.
- The diff viewer is a separate program the TUI hands a change to.
- Built in Go, with Bubble Tea v2 for the TUI and Lip Gloss v2 for styling. See decision D1 in `docs/build-plan.md`.

### Sources

git is the spine: branches, merges, commits and the final diff per commit. It cannot see:

- per-edit events between commits
- uncommitted work
- PRs, which are a forge concept rather than a git one. Infer them from merge commits, and enrich with `gh` (number, title, state) when the user opts in.

Claude Code, from its transcripts first and plugin hooks second:

- Each session writes a live JSONL transcript under `~/.claude/projects/`. It holds every prompt, tool call, diff and the agent's text, so reading it needs no setup and covers past sessions.
- Hooks are the documented capture path and come later. They also record HEAD at each event and the files a shell command changed.
- `UserPromptSubmit` carries the prompt text and becomes a prompt node.
- `PostToolUse` on Edit and Write carries the file and change and becomes an edit node.
- `PostToolUse` on Read becomes a read node.
- `PostToolUse` on Bash carries the command and result and becomes a run node, with pass or fail when it can be parsed.
- Every hook payload includes `session_id` and `transcript_path`. The transcript is a JSONL file on disk. Parsing it backfills past sessions with no hooks installed, and it holds the agent's own text between tool calls, which is the free source of notes.
- Hooks can ship inside a plugin (`hooks/hooks.json` or inline in `plugin.json`), so installing the plugin is the whole setup.

OpenCode (version 2), from its session export first and a plugin second:

- `opencode session export` prints a session as JSON, which covers backfill.
- Sessions and messages are also stored in a local SQLite database, which can be followed read-only for live updates.
- A server plugin can capture live: `ctx.session.hook("prompt")` for prompts and `ctx.tool.hook("execute.after")` for tool results.
- The version 1 hook names (`tool.execute.after` and similar) do not carry over to version 2.

### Diff viewer: Leaf

Leaf (github.com/DeepanshuMishraa/leaf, crate `leaf-reader`, MIT) is a Rust terminal reader with a diff viewer.

- It is a binary only. There is no library target and no plugin API.
- It is built on ratatui and crossterm.
- `leaf diff --patch <file>` opens a patch file. It does not read a diff from stdin.
- `leaf diff` also takes a path, a revision or a range.

So the TUI shells out to it: write the change to a temporary patch file, hand the terminal to `leaf diff --patch`, and take it back when Leaf exits. Keep the viewer behind one small interface, with a pager and a built-in inline view as fallbacks.

## Event log

Draft schema. Expect it to change. One JSON object per line.

```json
{
  "id": "evt_...",
  "ts": "2026-11-14T14:07:31Z",
  "source": "claude-code | opencode | git",
  "session": "session id from the harness",
  "kind": "pr | prompt | read | edit | run | commit",
  "parent": "id of the prompt or PR this belongs to",
  "subject": "src/middleware/rate_limit.py",
  "stat": "+5 -2",
  "hunk": "unified diff text, edits only",
  "commit": "7be02d1, or null while uncommitted",
  "note": { "change": "what it did", "why": "why it did it" },
  "from": "id of the event that caused this one",
  "links": [
    { "to": "evt_...", "type": "affects | revises | depends_on", "origin": "declared | inferred" }
  ]
}
```

Lanes are a rendering concern. The TUI computes them, and the log does not store them.

## Notes and links

Where the "why" comes from, ordered by how much stays on the machine:

1. The agent's own text just before the tool call, lifted from the transcript. Free and local, but uneven.
2. The agent is told to state change, reason and cause with each edit. Costs a few tokens per edit, gives the best fidelity, and is declared rather than guessed.
3. A model summarizes after the fact. Extra calls, may send data off the machine, and is a reconstruction.

Links are either declared by the agent or inferred by heuristics:

- same file
- same symbol
- a failing run that touches a changed line
- a later edit that rewrites an earlier hunk

Starting point: option 1 for notes, plus the same-file heuristic for links. Always record whether a link was declared or inferred, and draw inferred links more quietly.

## Privacy

Prompts and transcripts can contain secrets and sensitive context.

- Everything stays local by default. The event log lives under `.git/` or another ignored path, so it is never committed or pushed.
- No network calls in the core path. Enrichment such as `gh` for PR titles is opt-in.
- Sharing is an explicit export, never automatic.
- When choosing between approaches (note sources, link inference), prefer the one that exposes less data outside the machine.

## Build order

The full plan, with tasks and exit checks per phase, is in `docs/build-plan.md`. In short:

1. Parse one existing Claude Code transcript JSONL and print its events as plain text. This proves the data is there.
2. Join it with git to build the main, branch and commit spine.
3. Build a static, read-only TUI over the joined data that matches the interface above.
4. Make it live: follow transcripts as they grow, persist events to the local log, and add hooks.
5. Add notes and links.
6. Add the OpenCode adapter.

## Open questions

- CLI name and install method.
- Parallel PRs: two or three can each get their own lane. Beyond that, should one PR be focused and the rest collapsed to single rows?
- Cross-PR arcs: linking a change in an open PR back to the change in an earlier, merged PR that set it up.
- Agent Trace compatibility: whether to also read or write Agent Trace records (an open spec for line-level AI code attribution, agent-trace.dev) alongside the event log.
- Subagents: should a subagent's events nest under the step that spawned it as another level?
- A dark theme.

## Working in this repo

- Keep the TUI read-only over the event log and git. All harness-specific code lives in adapters.
- No co-author trailers, "generated by" footers or tool attribution in commits, PR descriptions, code comments or files.
- Keep event logs, transcripts and local tool config out of commits.
- Prefer small commits that are easy to review.
