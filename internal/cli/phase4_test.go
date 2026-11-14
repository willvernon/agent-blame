package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agent-blame/internal/model"
	"agent-blame/internal/store"
	"agent-blame/internal/testutil"
)

// isolate points transcript discovery at an empty folder, so tests never
// read the real ~/.claude.
func isolate(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	return dir
}

func TestSyncedEventsSurviveTranscriptCleanup(t *testing.T) {
	config := isolate(t)
	r := testutil.NewRepo(t)
	r.Commit(time.Unix(1_700_000_000, 0), "initial", map[string]string{"README.md": "x\n"})
	fixture, err := os.ReadFile("../sources/claudecode/testdata/session.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	projects := filepath.Join(config, "projects", "x")
	if err := os.MkdirAll(projects, 0o700); err != nil {
		t.Fatal(err)
	}
	transcript := filepath.Join(projects, "s1.jsonl")
	body := strings.ReplaceAll(string(fixture), `"/repo`, `"`+r.Dir)
	if err := os.WriteFile(transcript, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	count := func() int {
		code, out, stderr := run(t, r.Dir, "", "dump", "--json")
		if code != 0 {
			t.Fatalf("dump failed: %s", stderr)
		}
		return strings.Count(out, `"id":`)
	}
	before := count()
	if before != 10 {
		t.Fatalf("fixture gave %d events, want 10", before)
	}
	if code, _, stderr := run(t, r.Dir, "", "sync"); code != 0 {
		t.Fatalf("sync failed: %s", stderr)
	}
	if err := os.Remove(transcript); err != nil {
		t.Fatal(err)
	}
	if after := count(); after != before {
		t.Errorf("after the transcript was deleted, %d events remain, want %d", after, before)
	}
}

func run(t *testing.T, dir string, stdin string, args ...string) (int, string, string) {
	t.Helper()
	if os.Getenv("CLAUDE_CONFIG_DIR") == "" {
		t.Fatal("call isolate(t) first so the test cannot read real transcripts")
	}
	var out, errb bytes.Buffer
	old, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(old)
	code := Run(args, "test", strings.NewReader(stdin), &out, &errb)
	return code, out.String(), errb.String()
}

func TestInstallKeepsOtherHooksAndIsIdempotent(t *testing.T) {
	isolate(t)
	dir := t.TempDir()
	settings := filepath.Join(dir, "settings.json")
	existing := `{"theme":"light","hooks":{"PostToolUse":[{"matcher":"Write","hooks":[{"type":"command","command":"fmt.sh"}]}]}}`
	if err := os.WriteFile(settings, []byte(existing), 0o600); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if code, _, stderr := run(t, dir, "", "install", "claude-code", "--settings", settings); code != 0 {
			t.Fatalf("install failed: %s", stderr)
		}
	}
	b, _ := os.ReadFile(settings)
	var cfg map[string]any
	if err := json.Unmarshal(b, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg["theme"] != "light" {
		t.Error("unrelated setting lost")
	}
	if n := strings.Count(string(b), hookMarker); n != 3 {
		t.Errorf("agent-blame hook appears %d times, want once per event (3)", n)
	}
	if !strings.Contains(string(b), "fmt.sh") {
		t.Error("existing hook removed")
	}
	if _, err := os.Stat(settings + ".agent-blame.bak"); err != nil {
		t.Error("no backup written")
	}

	if code, _, stderr := run(t, dir, "", "install", "claude-code", "--uninstall", "--settings", settings); code != 0 {
		t.Fatalf("uninstall failed: %s", stderr)
	}
	b, _ = os.ReadFile(settings)
	if strings.Contains(string(b), hookMarker) || !strings.Contains(string(b), "fmt.sh") {
		t.Errorf("uninstall left %s", b)
	}
}

func TestHookRoundTrip(t *testing.T) {
	isolate(t)
	r := testutil.NewRepo(t)
	r.Commit(time.Unix(1_700_000_000, 0), "initial", map[string]string{"a.py": "x\n"})
	payload := func(event, extra string) string {
		return `{"session_id":"s1","prompt_id":"p1","cwd":"` + r.Dir + `","hook_event_name":"` + event + `"` + extra + `}`
	}

	// Without a store the hook records nothing.
	if code, out, _ := run(t, r.Dir, payload("UserPromptSubmit", `,"prompt":"hello"`), "hook", "claude-code"); code != 0 || out != "" {
		t.Fatalf("code %d out %q", code, out)
	}
	if store.Exists(filepath.Join(r.Dir, ".git")) {
		t.Fatal("hook created a store")
	}

	if code, _, stderr := run(t, r.Dir, "", "sync"); code != 0 {
		t.Fatalf("sync failed: %s", stderr)
	}
	run(t, r.Dir, payload("UserPromptSubmit", `,"prompt":"hello"`), "hook", "claude-code")
	edit := `,"tool_name":"Edit","tool_use_id":"t1","tool_input":{"file_path":"` + r.Dir + `/a.py"},` +
		`"tool_response":{"structuredPatch":[{"oldStart":1,"oldLines":1,"newStart":1,"newLines":1,"lines":["-x","+y"]}]}`
	run(t, r.Dir, payload("PostToolUse", edit), "hook", "claude-code")
	// garbage never fails
	if code, _, _ := run(t, r.Dir, "{not json", "hook", "claude-code"); code != 0 {
		t.Errorf("garbage input exited %d", code)
	}

	code, out, stderr := run(t, r.Dir, "", "dump", "--json")
	if code != 0 {
		t.Fatalf("dump failed: %s", stderr)
	}
	var evs []model.Event
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		var e model.Event
		if err := json.Unmarshal([]byte(line), &e); err == nil {
			evs = append(evs, e)
		}
	}
	if len(evs) != 2 {
		t.Fatalf("got %d events, want prompt and edit: %s", len(evs), out)
	}
	ed := evs[1]
	if ed.Subject != "a.py" || ed.Head == "" || ed.Parent != model.PromptID("s1", "p1") || ed.Stat == nil {
		t.Errorf("edit = %+v", ed)
	}
}

func TestRenderNeedsNoTerminal(t *testing.T) {
	isolate(t)
	r := testutil.NewRepo(t)
	r.Commit(time.Unix(1_700_000_000, 0), "initial", map[string]string{"a.py": "x\n"})
	code, out, stderr := run(t, r.Dir, "", "render", "--width", "80", "--height", "10")
	if code != 0 || !strings.Contains(out, "agent-blame") || !strings.Contains(out, "initial") {
		t.Fatalf("code %d out %q err %q", code, out, stderr)
	}
	if code, _, _ := run(t, r.Dir, "", "render", "--width", "80", "--ansi"); code != 0 {
		t.Error("ansi render failed")
	}
}

func TestTUIRefusesWithoutTerminal(t *testing.T) {
	isolate(t)
	r := testutil.NewRepo(t)
	if code, _, stderr := run(t, r.Dir, ""); code == 0 || !strings.Contains(stderr, "terminal") {
		t.Errorf("code %d stderr %q", code, stderr)
	}
}
