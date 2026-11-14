package store

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"agent-blame/internal/model"
)

func TestOpenWithoutCreateReturnsNil(t *testing.T) {
	s, err := Open(t.TempDir(), false)
	if err != nil || s != nil {
		t.Fatalf("got %v, %v; want nil store", s, err)
	}
}

func TestImportRoundTripAndBlobs(t *testing.T) {
	common := t.TempDir()
	s, err := Open(common, true)
	if err != nil {
		t.Fatal(err)
	}
	big := "@@ -0,0 +1,1 @@\n+" + strings.Repeat("x", 5000) + "\n"
	evs := []model.Event{
		{V: 1, ID: "a", Session: "s1", Kind: model.KindPrompt, Subject: "hi"},
		{V: 1, ID: "b", Session: "s1", Kind: model.KindEdit, Subject: "f.go", Hunk: big},
	}
	if err := s.WriteImport("s1", evs); err != nil {
		t.Fatal(err)
	}
	blobs, _ := os.ReadDir(filepath.Join(s.Dir, "blobs"))
	if len(blobs) != 1 {
		t.Fatalf("blobs = %d, want 1", len(blobs))
	}
	imported, _, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	got := imported["s1"]
	if len(got) != 2 || got[1].Hunk != big {
		t.Fatalf("round trip lost the hunk: %d events", len(got))
	}
}

func TestStateTracksChanges(t *testing.T) {
	common := t.TempDir()
	s, _ := Open(common, true)
	now := time.Now().Truncate(time.Second)
	if !s.Changed("/t.jsonl", 10, now) {
		t.Fatal("unseen transcript reported unchanged")
	}
	s.MarkSynced("/t.jsonl", 10, now)
	if err := s.SaveState(); err != nil {
		t.Fatal(err)
	}
	s2, _ := Open(common, false)
	if s2.Changed("/t.jsonl", 10, now) {
		t.Error("state not persisted")
	}
	if !s2.Changed("/t.jsonl", 11, now) {
		t.Error("size change not detected")
	}
}

func TestConcurrentHookAppendsStayIntact(t *testing.T) {
	common := t.TempDir()
	s, _ := Open(common, true)
	const n = 200
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s2, _ := Open(common, false) // separate handles, like separate processes
			e := model.Event{V: 1, ID: "e" + strconv.Itoa(i), Session: "s", Kind: model.KindRun, Subject: strings.Repeat("y", 3000)}
			if err := s2.AppendHook(e); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	_, hooks, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(hooks) != n {
		t.Fatalf("got %d intact hook events, want %d", len(hooks), n)
	}
}

func TestPartialLineIgnored(t *testing.T) {
	common := t.TempDir()
	s, _ := Open(common, true)
	path := filepath.Join(s.Dir, "events", "hook-s.jsonl")
	if err := os.WriteFile(path, []byte(`{"v":1,"id":"ok","kind":"run","subject":"x"}`+"\n"+`{"v":1,"id":"cut`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, hooks, _ := s.Load()
	if len(hooks) != 1 || hooks[0].ID != "ok" {
		t.Fatalf("got %+v", hooks)
	}
}
