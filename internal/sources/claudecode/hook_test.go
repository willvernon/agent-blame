package claudecode

import (
	"encoding/json"
	"testing"
	"time"

	"agent-blame/internal/model"
)

func hook(t *testing.T, payload string) (model.Event, bool) {
	t.Helper()
	var in HookInput
	if err := json.Unmarshal([]byte(payload), &in); err != nil {
		t.Fatal(err)
	}
	return HookEvent(in, RootResolver{Root: "/repo"}, time.Unix(100, 0), "abc123", "main")
}

func TestHookPromptMatchesTranscriptID(t *testing.T) {
	e, ok := hook(t, `{"session_id":"s1","prompt_id":"p1","hook_event_name":"UserPromptSubmit","prompt":"add rate limiting"}`)
	if !ok || e.ID != model.PromptID("s1", "p1") || e.Kind != model.KindPrompt || e.Head != "abc123" {
		t.Fatalf("got %+v ok=%v", e, ok)
	}
}

func TestHookEditCarriesPatchAndHead(t *testing.T) {
	e, ok := hook(t, `{"session_id":"s1","prompt_id":"p1","hook_event_name":"PostToolUse","tool_name":"Edit","tool_use_id":"t3",
		"tool_input":{"file_path":"/repo/src/a.py","old_string":"x","new_string":"y"},
		"tool_response":{"filePath":"/repo/src/a.py","structuredPatch":[{"oldStart":1,"oldLines":1,"newStart":1,"newLines":1,"lines":["-x","+y"]}]}}`)
	if !ok || e.ID != model.ToolID("s1", "t3") || e.Subject != "src/a.py" || e.Parent != model.PromptID("s1", "p1") {
		t.Fatalf("got %+v", e)
	}
	if e.Stat == nil || e.Stat.Add != 1 || e.Stat.Del != 1 || e.Head != "abc123" {
		t.Errorf("stat %+v head %q", e.Stat, e.Head)
	}
}

func TestHookFailedRun(t *testing.T) {
	e, ok := hook(t, `{"session_id":"s1","prompt_id":"p1","hook_event_name":"PostToolUseFailure","tool_name":"Bash","tool_use_id":"t5",
		"tool_input":{"command":"pytest -q","description":"Run tests"},"error":"Exit code 1\n3 failed","is_interrupt":false}`)
	if !ok || e.Kind != model.KindRun || e.OK == nil || *e.OK || e.RunClass != "test" {
		t.Fatalf("got %+v", e)
	}
}

func TestHookIgnoresSubagentsAndUnknownEvents(t *testing.T) {
	if _, ok := hook(t, `{"session_id":"s1","agent_id":"a1","hook_event_name":"PostToolUse","tool_name":"Read","tool_use_id":"t"}`); ok {
		t.Error("subagent payload produced an event")
	}
	if _, ok := hook(t, `{"session_id":"s1","hook_event_name":"Stop"}`); ok {
		t.Error("Stop produced an event")
	}
}

func TestRootResolver(t *testing.T) {
	r := RootResolver{Root: "/repo"}
	if rel, ok := r.Rel("/repo/a/b.go"); !ok || rel != "a/b.go" {
		t.Errorf("got %q %v", rel, ok)
	}
	if _, ok := r.Rel("/repository/x"); ok {
		t.Error("prefix of a different directory matched")
	}
}

func BenchmarkHookEvent(b *testing.B) {
	payload := []byte(`{"session_id":"s1","prompt_id":"p1","hook_event_name":"PostToolUse","tool_name":"Edit","tool_use_id":"t3",
		"tool_input":{"file_path":"/repo/src/a.py","old_string":"x","new_string":"y"},
		"tool_response":{"filePath":"/repo/src/a.py","structuredPatch":[{"oldStart":1,"oldLines":1,"newStart":1,"newLines":1,"lines":["-x","+y"]}]}}`)
	for b.Loop() {
		var in HookInput
		_ = json.Unmarshal(payload, &in)
		_, _ = HookEvent(in, RootResolver{Root: "/repo"}, time.Now(), "abc", "main")
	}
}
