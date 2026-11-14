package graph_test

import (
	"strings"
	"testing"

	"agent-blame/internal/graph"
	"agent-blame/internal/model"
	"agent-blame/internal/sources/git"
	"agent-blame/internal/testutil"
)

func buildScenario(t *testing.T) (*graph.Graph, testutil.Scenario) {
	t.Helper()
	sc := testutil.BuildScenario(testutil.NewRepo(t))
	repo, err := git.Open(sc.Repo.Dir)
	if err != nil {
		t.Fatal(err)
	}
	return graph.Build(repo, sc.Events, graph.Options{}), sc
}

func TestScenarioShape(t *testing.T) {
	g, _ := buildScenario(t)
	if g.Root.Kind != graph.KindMain || g.Default != "main" {
		t.Fatalf("root = %s %q", g.Root.Kind, g.Default)
	}
	if len(g.Root.Children) != 2 {
		t.Fatalf("main has %d children, want 2 branches", len(g.Root.Children))
	}
	pr41, pr42 := g.Root.Children[0], g.Root.Children[1]
	if pr41.Kind != graph.KindPR || pr41.PR != 41 || pr41.Status != graph.BranchMerged {
		t.Errorf("first branch = %+v", pr41)
	}
	if last := pr41.Children[len(pr41.Children)-1]; last.Kind != graph.KindMerge || last.Lane != 0 {
		t.Errorf("merged branch does not end in a merge row on main: %+v", last)
	}
	if pr42.Status != graph.BranchOpen || pr42.Title != "rate-limit" || !g.AnyOpen {
		t.Errorf("second branch = %+v", pr42)
	}
	if n := len(pr42.Children); n != 3 {
		t.Errorf("open branch has %d prompts, want 3", n)
	}
}

func TestEditAttribution(t *testing.T) {
	g, _ := buildScenario(t)
	want := map[string]string{
		"e41": "exact",
		"e1":  "exact",
		"e2":  "exact",
		"e3":  "exact",
		"e4":  "inferred",
		"e5":  "inferred",
		"e6":  graph.StatusUncommitted,
		"e7":  graph.StatusOrphan,
	}
	for id, w := range want {
		n := g.ByID[id]
		if n == nil {
			t.Errorf("%s missing", id)
			continue
		}
		got := n.Status
		if n.Event.Commit != nil {
			got = n.Event.Commit.How
		}
		if got != w {
			t.Errorf("%s (%s) = %q, want %q", id, n.Title, got, w)
		}
	}
	if c := g.ByID["e1"].Event.Commit; c == nil || g.Commits[c.SHA].Subject != "add login rate limiter" {
		t.Errorf("e1 attributed to the wrong commit: %+v", c)
	}
}

func TestCommitRowsAndLanes(t *testing.T) {
	g, _ := buildScenario(t)
	p1 := g.ByID["p1"]
	last := p1.Children[len(p1.Children)-1]
	if last.Kind != model.KindCommit || !strings.Contains(last.Title, "add login rate limiter") {
		t.Errorf("first prompt ends with %s %q, want its commit", last.Kind, last.Title)
	}
	if last.Lane != p1.Lane || g.ByID["e1"].Lane != p1.Lane+1 {
		t.Errorf("lanes: prompt %d, commit %d, edit %d", p1.Lane, last.Lane, g.ByID["e1"].Lane)
	}
	// The human-made commit holding prompt 2's edits sits under prompt 2.
	p2 := g.ByID["p2"]
	found := false
	for _, c := range p2.Children {
		if c.Kind == model.KindCommit && strings.Contains(c.Title, "reset limiter state") {
			found = true
		}
	}
	if !found {
		t.Error("inferred commit not placed under the prompt whose edits it holds")
	}
	if got := p1.Summary(); got != "3 edits, "+g.ByID["e1"].Event.Commit.SHA[:7] {
		t.Errorf("summary = %q", got)
	}
}

func TestTreeGolden(t *testing.T) {
	g, sc := buildScenario(t)
	var b strings.Builder
	g.Walk(func(n *graph.Node, depth int) bool {
		title := n.Title
		if n.Commit != nil {
			title = n.Commit.Subject // hashes differ per run
		}
		if n.Kind == graph.KindMain {
			title = g.Default
		}
		status := n.Status
		if n.Event != nil && n.Event.Commit != nil {
			status = n.Event.Commit.How
		}
		ts := ""
		if !n.Time.IsZero() {
			ts = n.Time.Sub(sc.T0).String()
		}
		b.WriteString(strings.Repeat("  ", depth) + string(n.Kind) + " lane" + string(rune('0'+n.Lane)) + " " + title)
		if status != "" {
			b.WriteString(" [" + status + "]")
		}
		if ts != "" {
			b.WriteString(" @" + ts)
		}
		b.WriteString("\n")
		return true
	})
	testutil.Golden(t, "scenario_tree.txt", b.String())
}

func TestEmptyRepoWithNoEvents(t *testing.T) {
	r := testutil.NewRepo(t)
	repo, err := git.Open(r.Dir)
	if err != nil {
		t.Fatal(err)
	}
	g := graph.Build(repo, nil, graph.Options{})
	if g.Root == nil || len(g.Root.Children) != 0 {
		t.Fatalf("got %+v", g.Root)
	}
}
