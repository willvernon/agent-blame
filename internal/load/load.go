// Package load gathers events for one repository from every source and the
// local store, merged by id.
package load

import (
	"sort"

	"agent-blame/internal/model"
	"agent-blame/internal/sources/claudecode"
	"agent-blame/internal/sources/git"
	"agent-blame/internal/store"
)

// Loader reads events for one repository.
type Loader struct {
	Repo   *git.Repo
	Finder *claudecode.Finder
	Store  *store.Store // nil means read transcripts only, write nothing
}

// New returns a loader for repo. The store is opened only if it already
// exists or create is true.
func New(repo *git.Repo, create bool) (*Loader, error) {
	l := &Loader{
		Repo:   repo,
		Finder: claudecode.NewFinder(claudecode.ProjectsDir(), repo.Contains),
	}
	st, err := store.Open(repo.CommonDir, create)
	if err != nil {
		return nil, err
	}
	l.Store = st
	return l, nil
}

// TranscriptReport describes one transcript that was read.
type TranscriptReport struct {
	claudecode.Transcript
	Stats  claudecode.Stats
	Events int
	Err    error
}

// Result is everything loaded.
type Result struct {
	Events      []model.Event
	Transcripts []TranscriptReport
}

// Load reads every transcript for the repository and merges in the store.
// With sync true and a store present, transcripts that changed since the
// last sync are written to the store first.
func (l *Loader) Load(sync bool) (Result, error) {
	var res Result
	ts, err := l.Finder.Find()
	if err != nil {
		return res, err
	}
	fresh := map[string][]model.Event{} // session -> events parsed now
	for _, t := range ts {
		rep := TranscriptReport{Transcript: t}
		if sync && l.Store != nil && !l.Store.Changed(t.Path, t.Size, t.ModTime) {
			res.Transcripts = append(res.Transcripts, rep)
			continue
		}
		m := claudecode.NewMapper(l.Repo)
		rep.Stats, rep.Err = claudecode.ReadFile(t.Path, m.Add)
		evs := m.Events()
		rep.Events = len(evs)
		fresh[t.Session] = append(fresh[t.Session], evs...)
		if sync && l.Store != nil && rep.Err == nil {
			if err := l.Store.WriteImport(t.Session, evs); err != nil {
				rep.Err = err
			} else {
				l.Store.MarkSynced(t.Path, t.Size, t.ModTime)
			}
		}
		res.Transcripts = append(res.Transcripts, rep)
	}
	if sync && l.Store != nil {
		if err := l.Store.SaveState(); err != nil {
			return res, err
		}
	}

	byID := map[string]model.Event{}
	var order []string
	add := func(e model.Event) {
		if prev, ok := byID[e.ID]; ok {
			byID[e.ID] = model.Merge(prev, e)
			return
		}
		byID[e.ID] = e
		order = append(order, e.ID)
	}
	var hooks []model.Event
	if l.Store != nil {
		imported, hookEvs, err := l.Store.Load()
		if err != nil {
			return res, err
		}
		for session, evs := range imported {
			if _, parsed := fresh[session]; parsed {
				continue // the fresh parse is newer
			}
			for _, e := range evs {
				add(e)
			}
		}
		hooks = hookEvs
	}
	for _, evs := range fresh {
		for _, e := range evs {
			add(e)
		}
	}
	for _, e := range hooks {
		add(e) // merged last: the hook supplies HEAD and outcome
	}

	res.Events = make([]model.Event, 0, len(order))
	for _, id := range order {
		res.Events = append(res.Events, byID[id])
	}
	sort.SliceStable(res.Events, func(i, j int) bool { return res.Events[i].TS.Before(res.Events[j].TS) })
	return res, nil
}
