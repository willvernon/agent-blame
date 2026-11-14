// Package graph arranges events and commits into the tree the TUI draws:
// main at the root, branches (shown as PRs) under it, prompts in order on
// their branch, and each prompt's reads, edits, runs and commits.
package graph

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"agent-blame/internal/model"
	"agent-blame/internal/sources/claudecode"
	"agent-blame/internal/sources/git"
)

// Node kinds beyond the event kinds.
const (
	KindMain  model.Kind = "main"
	KindPR    model.Kind = "pr"
	KindMerge model.Kind = "merge"
)

// Edit attribution states when no commit was found.
const (
	StatusUncommitted = "uncommitted"
	StatusOrphan      = "orphan"
)

// Branch states.
const (
	BranchOpen   = "open"
	BranchMerged = "merged"
	BranchGone   = "closed"
)

// Node is one row-to-be in the tree.
type Node struct {
	ID       string
	Kind     model.Kind
	Title    string
	Time     time.Time
	Event    *model.Event
	Commit   *git.Commit
	Parent   *Node
	Children []*Node
	Lane     int
	Status   string
	PR       int
	Branch   string
}

// Graph is the whole tree.
type Graph struct {
	Root    *Node
	ByID    map[string]*Node
	Default string
	Commits map[string]*git.Commit
	// AnyOpen is true when some branch has not landed on main yet.
	AnyOpen bool
}

// Options tune Build. Zero values are the defaults.
type Options struct {
	ShowOther bool            // include searches, web fetches and other tools
	Limit     int             // commits read from git, default 2000
	Dirty     map[string]bool // override working tree state, for tests
}

// Build arranges events and repository history into a tree.
func Build(repo *git.Repo, evs []model.Event, opt Options) *Graph {
	if opt.Limit == 0 {
		opt.Limit = 2000
	}
	b := &builder{repo: repo, opt: opt, byID: map[string]*Node{}}
	b.readGit()
	b.copyEvents(evs)
	b.findAgentCommits()
	b.attributeEdits()
	return b.assemble()
}

type builder struct {
	repo *git.Repo
	opt  Options

	def     string
	commits []git.Commit
	bySHA   map[string]*git.Commit
	trunk   map[string]bool
	dirty   map[string]bool
	heads   map[string]string // branch -> sha

	events []*model.Event
	byID   map[string]*Node
}

func (b *builder) readGit() {
	b.def = b.repo.DefaultBranch()
	b.commits, _ = b.repo.Commits(b.opt.Limit)
	b.bySHA = map[string]*git.Commit{}
	for i := range b.commits {
		b.bySHA[b.commits[i].SHA] = &b.commits[i]
	}
	b.trunk = map[string]bool{}
	for _, sha := range b.repo.FirstParent(b.def, b.opt.Limit) {
		b.trunk[sha] = true
	}
	b.dirty = b.opt.Dirty
	if b.dirty == nil {
		b.dirty = b.repo.Dirty()
	}
	b.heads = map[string]string{}
	for _, br := range b.repo.Branches() {
		b.heads[br.Name] = br.SHA
	}
}

func (b *builder) copyEvents(evs []model.Event) {
	b.events = make([]*model.Event, 0, len(evs))
	for i := range evs {
		e := evs[i]
		e.Commit = nil
		e.MadeCommit = ""
		b.events = append(b.events, &e)
	}
	sort.SliceStable(b.events, func(i, j int) bool { return b.events[i].TS.Before(b.events[j].TS) })
}

// findAgentCommits links each successful git commit run to the commit it
// made: the commit whose time falls inside the run.
func (b *builder) findAgentCommits() {
	for _, e := range b.events {
		if e.Kind != model.KindRun || e.InFlight || e.OK == nil || !*e.OK || !claudecode.IsGitCommit(e.Subject) {
			continue
		}
		start := e.TS.Truncate(time.Second).Add(-time.Second)
		end := e.End
		if end.IsZero() {
			end = e.TS.Add(time.Minute)
		}
		end = end.Add(time.Second)
		var best *git.Commit
		for i := range b.commits {
			c := &b.commits[i]
			if c.Time.Before(start) || c.Time.After(end) || len(c.Parents) > 1 {
				continue
			}
			if best == nil || c.Time.Before(best.Time) {
				best = c
			}
		}
		if best != nil {
			e.MadeCommit = best.SHA
		}
	}
}

func (b *builder) attributeEdits() {
	runsBySession := map[string][]*model.Event{}
	for _, e := range b.events {
		if e.MadeCommit != "" {
			runsBySession[e.Session] = append(runsBySession[e.Session], e)
		}
	}
	for i, e := range b.events {
		if e.Kind != model.KindEdit || e.Outside || (e.OK != nil && !*e.OK) {
			continue
		}
		if c := b.exactByRun(e, runsBySession[e.Session]); c != "" {
			e.Commit = &model.CommitRef{SHA: c, How: model.HowExact}
			continue
		}
		if c := b.exactByHead(i, e); c != "" {
			e.Commit = &model.CommitRef{SHA: c, How: model.HowExact}
			continue
		}
		if c := b.inferred(e); c != "" {
			e.Commit = &model.CommitRef{SHA: c, How: model.HowInferred}
		}
	}
}

func (b *builder) touches(sha, path string) bool {
	c := b.bySHA[sha]
	if c == nil {
		return false
	}
	for _, f := range c.Files {
		if f == path {
			return true
		}
	}
	return false
}

func (b *builder) exactByRun(e *model.Event, runs []*model.Event) string {
	for _, r := range runs {
		if !r.TS.After(e.TS) {
			continue
		}
		if b.touches(r.MadeCommit, e.Subject) {
			return r.MadeCommit
		}
	}
	return ""
}

// exactByHead uses HEAD as recorded by the hook: the first later event in
// the same session with a different HEAD names the commit that followed.
func (b *builder) exactByHead(i int, e *model.Event) string {
	if e.Head == "" {
		return ""
	}
	for _, later := range b.events[i+1:] {
		if later.Session != e.Session || later.Head == "" || later.Head == e.Head {
			continue
		}
		if b.touches(later.Head, e.Subject) {
			return later.Head
		}
		return ""
	}
	return ""
}

func (b *builder) inferred(e *model.Event) string {
	from := e.TS.Truncate(time.Second)
	var best *git.Commit
	for i := range b.commits {
		c := &b.commits[i]
		if c.Time.Before(from) || len(c.Parents) > 1 || !b.touches(c.SHA, e.Subject) {
			continue
		}
		if best == nil || c.Time.Before(best.Time) {
			best = c
		}
	}
	if best == nil {
		return ""
	}
	return best.SHA
}

var mergeSubject = regexp.MustCompile(`^Merge (?:branch '([^']+)'|pull request #(\d+) from \S+?/(\S+))`)

type branchInfo struct {
	status string
	pr     int
	merge  *git.Commit
}

func (b *builder) branchStatus(name string) branchInfo {
	for i := range b.commits {
		c := &b.commits[i]
		if len(c.Parents) < 2 || !b.trunk[c.SHA] {
			continue
		}
		m := mergeSubject.FindStringSubmatch(c.Subject)
		if m == nil {
			continue
		}
		if m[1] == name || m[3] == name {
			pr, _ := strconv.Atoi(m[2])
			return branchInfo{status: BranchMerged, pr: pr, merge: c}
		}
	}
	if _, ok := b.heads[name]; ok {
		return branchInfo{status: BranchOpen}
	}
	return branchInfo{status: BranchGone}
}

func (b *builder) node(n *Node) *Node {
	b.byID[n.ID] = n
	return n
}

func adopt(parent, child *Node) {
	child.Parent = parent
	parent.Children = append(parent.Children, child)
}

func (b *builder) assemble() *Graph {
	g := &Graph{Default: b.def, Commits: b.bySHA, ByID: b.byID}
	rootTitle := "no commits yet"
	if sha := b.heads[b.def]; sha != "" {
		rootTitle = shortSHA(sha)
		if c := b.bySHA[sha]; c != nil {
			rootTitle += "  " + c.Subject
		}
	}
	if b.def != "main" {
		rootTitle = b.def + "  " + rootTitle
	}
	root := b.node(&Node{ID: model.BranchID(b.def), Kind: KindMain, Title: rootTitle, Branch: b.def})
	g.Root = root

	prompts := map[string]*Node{}
	branches := map[string]*Node{}
	branchFor := func(name string, t time.Time) *Node {
		if name == "" || name == "HEAD" || name == b.def {
			return root
		}
		if n := branches[name]; n != nil {
			if t.Before(n.Time) {
				n.Time = t
			}
			return n
		}
		info := b.branchStatus(name)
		title := name
		if info.pr > 0 {
			title = "#" + strconv.Itoa(info.pr) + " " + name
		}
		n := b.node(&Node{ID: model.BranchID(name), Kind: KindPR, Title: title, Time: t, Lane: 1, Status: info.status, PR: info.pr, Branch: name})
		if info.merge != nil {
			m := b.node(&Node{ID: model.CommitID(info.merge.SHA), Kind: KindMerge, Title: shortSHA(info.merge.SHA) + "  " + info.merge.Subject, Time: info.merge.Time, Commit: info.merge, Lane: 0, Branch: b.def})
			n.Children = append(n.Children, m) // adopted after sorting, so it stays last
			m.Parent = n
		}
		if info.status == BranchOpen {
			g.AnyOpen = true
		}
		branches[name] = n
		adopt(root, n)
		return n
	}

	for _, e := range b.events {
		if e.Kind != model.KindPrompt {
			continue
		}
		parent := branchFor(e.Branch, e.TS)
		n := b.node(&Node{ID: e.ID, Kind: model.KindPrompt, Title: e.Subject, Time: e.TS, Event: e, Lane: parent.Lane, Branch: e.Branch})
		adopt(parent, n)
		prompts[e.ID] = n
	}

	commitNodes := map[string]*Node{}
	for _, e := range b.events {
		if e.Kind == model.KindPrompt {
			continue
		}
		if e.Kind == model.KindOther && !b.opt.ShowOther {
			continue
		}
		p := prompts[e.Parent]
		if p == nil {
			parent := branchFor(e.Branch, e.TS)
			p = b.node(&Node{ID: e.Parent, Kind: model.KindPrompt, Title: "(prompt not in any transcript)", Time: e.TS, Lane: parent.Lane, Branch: e.Branch})
			adopt(parent, p)
			prompts[e.Parent] = p
		}
		n := b.node(&Node{ID: e.ID, Kind: e.Kind, Title: e.Subject, Time: e.TS, Event: e, Lane: p.Lane + 1, Branch: e.Branch})
		if e.Kind == model.KindEdit && e.Commit == nil && !e.Outside && (e.OK == nil || *e.OK) {
			if b.dirty[e.Subject] {
				n.Status = StatusUncommitted
			} else {
				n.Status = StatusOrphan
			}
		}
		adopt(p, n)
		if e.MadeCommit != "" {
			b.addCommit(commitNodes, p, e.MadeCommit)
		}
	}
	// Commits that hold agent edits but were not made by an agent run go
	// under the prompt of the latest edit they hold.
	for _, e := range b.events {
		if e.Commit == nil || commitNodes[e.Commit.SHA] != nil {
			continue
		}
		latest := e
		for _, other := range b.events {
			if other.Commit != nil && other.Commit.SHA == e.Commit.SHA && other.TS.After(latest.TS) {
				latest = other
			}
		}
		if p := prompts[latest.Parent]; p != nil {
			b.addCommit(commitNodes, p, e.Commit.SHA)
		}
	}

	sortTree(root)
	return g
}

func (b *builder) addCommit(seen map[string]*Node, prompt *Node, sha string) {
	if seen[sha] != nil {
		return
	}
	c := b.bySHA[sha]
	if c == nil {
		return
	}
	n := b.node(&Node{ID: model.CommitID(sha), Kind: model.KindCommit, Title: shortSHA(sha) + "  " + c.Subject, Time: c.Time, Commit: c, Lane: prompt.Lane, Branch: prompt.Branch})
	adopt(prompt, n)
	seen[sha] = n
}

// sortTree orders children by time. A merge row stays last under its branch.
func sortTree(n *Node) {
	sort.SliceStable(n.Children, func(i, j int) bool {
		a, c := n.Children[i], n.Children[j]
		if (a.Kind == KindMerge) != (c.Kind == KindMerge) {
			return c.Kind == KindMerge
		}
		if !a.Time.Equal(c.Time) {
			return a.Time.Before(c.Time)
		}
		// at the same second a commit follows the run that made it
		if (a.Kind == model.KindCommit) != (c.Kind == model.KindCommit) {
			return c.Kind == model.KindCommit
		}
		return a.ID < c.ID
	})
	for _, c := range n.Children {
		sortTree(c)
	}
}

func shortSHA(s string) string {
	if len(s) > 7 {
		return s[:7]
	}
	return s
}

// Walk visits nodes depth first, in display order.
func (g *Graph) Walk(fn func(n *Node, depth int) bool) {
	var walk func(n *Node, depth int)
	walk = func(n *Node, depth int) {
		if !fn(n, depth) {
			return
		}
		for _, c := range n.Children {
			walk(c, depth+1)
		}
	}
	walk(g.Root, 0)
}

// IsEvent reports whether the node is a tool event (read, edit, run, other).
func (n *Node) IsEvent() bool {
	switch n.Kind {
	case model.KindRead, model.KindEdit, model.KindRun, model.KindOther:
		return true
	}
	return false
}

// Last returns the deepest last descendant, the last row the node owns.
func (n *Node) Last() *Node {
	for len(n.Children) > 0 {
		n = n.Children[len(n.Children)-1]
	}
	return n
}

// Contains reports whether d is n or below n.
func (n *Node) Contains(d *Node) bool {
	for ; d != nil; d = d.Parent {
		if d == n {
			return true
		}
	}
	return false
}

// Summary describes a prompt's work for its row: edit count and commits.
func (n *Node) Summary() string {
	edits, uncommitted := 0, false
	var shas []string
	for _, c := range n.Children {
		switch c.Kind {
		case model.KindEdit:
			edits++
			if c.Status == StatusUncommitted {
				uncommitted = true
			}
		case model.KindCommit:
			shas = append(shas, shortSHA(c.Commit.SHA))
		}
	}
	parts := []string{}
	if edits == 1 {
		parts = append(parts, "1 edit")
	} else if edits > 1 {
		parts = append(parts, strconv.Itoa(edits)+" edits")
	}
	if len(shas) > 0 {
		parts = append(parts, strings.Join(shas, " "))
	} else if uncommitted {
		parts = append(parts, "uncommitted")
	}
	return strings.Join(parts, ", ")
}
