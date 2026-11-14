package claudecode

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"agent-blame/internal/model"
)

// prefixResolver treats everything under root as inside the repository.
type prefixResolver string

func (r prefixResolver) Rel(p string) (string, bool) {
	root := string(r) + "/"
	if rest, ok := strings.CutPrefix(p, root); ok {
		return rest, true
	}
	return p, false
}

func mapFixture(t *testing.T) ([]model.Event, Stats) {
	t.Helper()
	m := NewMapper(prefixResolver("/repo"))
	st, err := ReadFile("testdata/session.jsonl", m.Add)
	if err != nil {
		t.Fatal(err)
	}
	return m.Events(), st
}

func TestMapFixture(t *testing.T) {
	evs, st := mapFixture(t)
	if !st.Partial {
		t.Error("truncated last line was not reported as partial")
	}
	if st.Unparsed != 0 {
		t.Errorf("unparsed = %d, want 0", st.Unparsed)
	}

	type want struct {
		kind    model.Kind
		subject string
		stat    string
		ok      string
	}
	wants := []want{
		{model.KindPrompt, "add rate limiting to /login", "", ""},
		{model.KindRead, "src/routes/auth.py", "", ""},
		{model.KindEdit, "src/middleware/rate_limit.py", "+3 -0", ""},
		{model.KindEdit, "src/routes/auth.py", "+2 -1", ""},
		{model.KindRead, "/home/someone/.config/tool/settings.json", "", ""},
		{model.KindPrompt, "tests are failing, fix it", "", ""},
		{model.KindRun, "pytest -q", "", "false"},
		{model.KindEdit, "src/middleware/rate_limit.py", "+3 -0", ""},
		{model.KindRun, "pytest -q", "", "true"},
		{model.KindRun, "git commit -am 'reset limiter state in tests'", "", ""},
	}
	if len(evs) != len(wants) {
		for _, e := range evs {
			t.Logf("%s %q", e.Kind, e.Subject)
		}
		t.Fatalf("got %d events, want %d", len(evs), len(wants))
	}
	for i, w := range wants {
		e := evs[i]
		stat := ""
		if e.Stat != nil {
			stat = "+" + itoa(e.Stat.Add) + " -" + itoa(e.Stat.Del)
		}
		ok := ""
		if e.OK != nil {
			ok = map[bool]string{true: "true", false: "false"}[*e.OK]
		}
		if e.Kind != w.kind || e.Subject != w.subject || stat != w.stat || ok != w.ok {
			t.Errorf("event %d = %s %q %q ok=%q, want %s %q %q ok=%q", i, e.Kind, e.Subject, stat, ok, w.kind, w.subject, w.stat, w.ok)
		}
	}

	byTool := map[string]model.Event{}
	for _, e := range evs {
		byTool[e.ID] = e
	}
	read := byTool[model.ToolID("s1", "t1")]
	if read.Note.Why != "I need to see how the login handler is registered before wrapping it." {
		t.Errorf("why for read = %q", read.Note.Why)
	}
	if read.Parent != model.PromptID("s1", "p1") {
		t.Errorf("read parent = %q", read.Parent)
	}
	write := byTool[model.ToolID("s1", "t2")]
	if !write.Created || !strings.HasPrefix(write.Hunk, "@@ -0,0 +1,3 @@\n+class RateLimiter:") {
		t.Errorf("write hunk = %q created=%v", write.Hunk, write.Created)
	}
	outside := byTool[model.ToolID("s1", "t4")]
	if !outside.Outside {
		t.Error("path outside the repo not marked Outside")
	}
	fail := byTool[model.ToolID("s1", "t5")]
	if fail.Parent != model.PromptID("s1", "p2") || fail.RunClass != "test" || fail.Note.Change != "Run the test suite" {
		t.Errorf("failing run = %+v", fail)
	}
	pass := byTool[model.ToolID("s1", "t7")]
	if pass.Output != "41 passed" || pass.InFlight {
		t.Errorf("passing run output=%q inflight=%v", pass.Output, pass.InFlight)
	}
	commit := byTool[model.ToolID("s1", "t9")]
	if !commit.InFlight || commit.RunClass != "git" {
		t.Errorf("commit run inflight=%v class=%q, want in flight git", commit.InFlight, commit.RunClass)
	}
	if _, ok := byTool[model.ToolID("s1", "t8")]; ok {
		t.Error("sidechain record was imported")
	}
	for _, e := range evs {
		if strings.Contains(e.Note.Why, "private reasoning") {
			t.Error("thinking text leaked into a note")
		}
	}
}

func TestMapIsDeterministic(t *testing.T) {
	a, _ := mapFixture(t)
	b, _ := mapFixture(t)
	if len(a) != len(b) {
		t.Fatal("different lengths")
	}
	for i := range a {
		if a[i].ID != b[i].ID {
			t.Fatalf("id %d differs: %s vs %s", i, a[i].ID, b[i].ID)
		}
	}
}

func TestToolCallsBeforeAnyPromptGetAPlaceholder(t *testing.T) {
	line := `{"type":"assistant","uuid":"a","sessionId":"s9","timestamp":"2026-03-01T10:00:00Z","message":{"id":"m","content":[{"type":"tool_use","id":"t","name":"Read","input":{"file_path":"/repo/a.go"}}]}}` + "\n"
	m := NewMapper(prefixResolver("/repo"))
	if _, err := Read(strings.NewReader(line), m.Add); err != nil {
		t.Fatal(err)
	}
	evs := m.Events()
	if len(evs) != 2 || evs[0].ID != model.SessionRootID("s9") || evs[1].Parent != evs[0].ID {
		t.Fatalf("got %+v", evs)
	}
}

func TestPromptsSharingAPromptIDStaySeparate(t *testing.T) {
	lines := `{"type":"user","uuid":"u1","sessionId":"s","timestamp":"2026-03-01T10:00:00Z","promptId":"same","promptSource":"sdk","message":{"content":"first"}}
{"type":"user","uuid":"u2","sessionId":"s","timestamp":"2026-03-01T10:05:00Z","promptId":"same","promptSource":"sdk","message":{"content":"second"}}
{"type":"assistant","uuid":"a","sessionId":"s","timestamp":"2026-03-01T10:05:01Z","message":{"id":"m","content":[{"type":"tool_use","id":"t","name":"Read","input":{"file_path":"/repo/a.go"}}]}}
`
	m := NewMapper(prefixResolver("/repo"))
	if _, err := Read(strings.NewReader(lines), m.Add); err != nil {
		t.Fatal(err)
	}
	evs := m.Events()
	if len(evs) != 3 || evs[0].Subject != "first" || evs[1].Subject != "second" {
		t.Fatalf("got %+v", evs)
	}
	if evs[0].ID != model.PromptID("s", "same") || evs[1].ID != model.PromptID("s", "u2") {
		t.Errorf("ids = %s, %s", evs[0].ID, evs[1].ID)
	}
	if evs[2].Parent != evs[1].ID {
		t.Errorf("tool call attached to %s, want the second prompt", evs[2].Parent)
	}
}

func TestCleanPrompt(t *testing.T) {
	in := "<system-reminder>\nnoise\n</system-reminder>\n  hello <b>there</b>  "
	if got := CleanPrompt(in); got != "hello <b>there</b>" {
		t.Errorf("got %q", got)
	}
}

func TestRunClass(t *testing.T) {
	cases := map[string]string{
		"go test ./...":                 "test",
		"cd x && npm test":              "test",
		"cargo build --release":         "build",
		"git commit -m wip":             "git",
		"ls -la":                        "other",
		"echo gotest":                   "other",
		"git -C repo commit -q -m done": "other",
	}
	for cmd, want := range cases {
		if got := RunClass(cmd); got != want {
			t.Errorf("RunClass(%q) = %q, want %q", cmd, got, want)
		}
	}
	if !IsGitCommit("git -C repo commit -q -m done") || !IsGitCommit("git add -A && git commit -m x") {
		t.Error("IsGitCommit missed a commit")
	}
	for _, notCommit := range []string{"git log --oneline", "git config --get commit.template", "echo git commit-graph"} {
		if IsGitCommit(notCommit) {
			t.Errorf("IsGitCommit matched %q", notCommit)
		}
	}
}

func FuzzRead(f *testing.F) {
	seed, err := os.ReadFile("testdata/session.jsonl")
	if err != nil {
		f.Fatal(err)
	}
	f.Add(seed)
	f.Add([]byte(`{"type":"user","message":{"content":5}}` + "\n"))
	f.Add([]byte(`{"type":"assistant","message":{"content":[{"type":"tool_use","input":null}]}}` + "\n"))
	f.Fuzz(func(t *testing.T, data []byte) {
		m := NewMapper(prefixResolver("/repo"))
		_, _ = Read(bytes.NewReader(data), m.Add)
		_ = m.Events()
	})
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
