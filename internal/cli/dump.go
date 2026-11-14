package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"agent-blame/internal/graph"
	"agent-blame/internal/load"
	"agent-blame/internal/model"
	"agent-blame/internal/sources/git"
)

func runDump(env Env, args []string) error {
	fs := newFlags("dump", env)
	asJSON := fs.Bool("json", false, "print events as JSON lines")
	tree := fs.Bool("tree", false, "print the main, branch, prompt, event tree")
	if err := fs.Parse(args); err != nil {
		return err
	}
	repo, err := git.Open(env.Dir)
	if err != nil {
		return err
	}
	l, err := load.New(repo, false)
	if err != nil {
		return err
	}
	res, err := l.Load(false)
	if err != nil {
		return err
	}
	switch {
	case *asJSON:
		enc := json.NewEncoder(env.Stdout)
		for _, e := range res.Events {
			if err := enc.Encode(e); err != nil {
				return err
			}
		}
	case *tree:
		g := graph.Build(repo, res.Events, graph.Options{})
		FormatTree(env.Stdout, g, time.Local)
	default:
		FormatEvents(env.Stdout, res.Events, time.Local)
	}
	return nil
}

// FormatEvents prints one line per event.
func FormatEvents(w io.Writer, evs []model.Event, loc *time.Location) {
	for _, e := range evs {
		indent := "  "
		if e.Kind == model.KindPrompt {
			indent = ""
		}
		fmt.Fprintf(w, "%s  %s%-7s %s%s\n", e.TS.In(loc).Format("2006-01-02 15:04:05"), indent, e.Kind, flat(e.Subject, 90), eventTail(e))
	}
}

func eventTail(e model.Event) string {
	var parts []string
	if e.Stat != nil {
		parts = append(parts, fmt.Sprintf("+%d -%d", e.Stat.Add, e.Stat.Del))
	}
	if e.Kind == model.KindRun && e.OK != nil {
		parts = append(parts, map[bool]string{true: "ok", false: "failed"}[*e.OK])
	}
	if e.Kind == model.KindEdit && e.OK != nil && !*e.OK {
		parts = append(parts, "failed")
	}
	if e.InFlight {
		parts = append(parts, "running")
	}
	if e.Outside {
		parts = append(parts, "outside repo")
	}
	if e.Commit != nil {
		parts = append(parts, "in "+short(e.Commit.SHA)+" ("+e.Commit.How+")")
	}
	if len(parts) == 0 {
		return ""
	}
	return "  [" + strings.Join(parts, ", ") + "]"
}

// FormatTree prints the graph as an indented outline.
func FormatTree(w io.Writer, g *graph.Graph, loc *time.Location) {
	var walk func(n *graph.Node, depth int)
	walk = func(n *graph.Node, depth int) {
		ts := ""
		if !n.Time.IsZero() {
			ts = n.Time.In(loc).Format("2006-01-02 15:04")
		}
		extra := ""
		if n.Event != nil {
			extra = eventTail(*n.Event)
		}
		if n.Status != "" {
			extra += "  [" + n.Status + "]"
		}
		fmt.Fprintf(w, "%-16s %s%-7s %s%s\n", ts, strings.Repeat("  ", depth), n.Kind, flat(n.Title, 80), extra)
		for _, c := range n.Children {
			walk(c, depth+1)
		}
	}
	walk(g.Root, 0)
}

func flat(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) > n {
		return string(r[:n-3]) + "..."
	}
	return s
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}
