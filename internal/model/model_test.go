package model

import (
	"testing"
	"time"
)

func TestMergePrefersTranscriptContentAndHookOutcome(t *testing.T) {
	t0 := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	transcript := Event{
		ID: "cc:s:tool:1", TS: t0, Source: SourceClaudeCode, Kind: KindRun,
		Subject: "go test ./...", OK: Bool(true), Note: Note{Why: "check it"},
		Output: "ok",
	}
	hook := Event{
		ID: "cc:s:tool:1", TS: t0.Add(time.Second), Source: "claude-code-hook", Kind: KindRun,
		Subject: "go test ./...", OK: Bool(false), Head: "abc123",
	}
	got := Merge(transcript, hook)
	if got.Head != "abc123" {
		t.Errorf("head = %q, want hook value", got.Head)
	}
	if got.OK == nil || *got.OK {
		t.Errorf("ok = %v, want hook value false", got.OK)
	}
	if got.Note.Why != "check it" || got.Output != "ok" {
		t.Errorf("transcript content lost: %+v", got)
	}
	if !got.TS.Equal(t0) {
		t.Errorf("ts = %v, want earliest %v", got.TS, t0)
	}
}

func TestMergeIntoEmpty(t *testing.T) {
	b := Event{ID: "x", Subject: "y"}
	if got := Merge(Event{}, b); got.ID != "x" || got.Subject != "y" {
		t.Errorf("got %+v", got)
	}
}

func TestMergeClearsInFlightWhenEitherSideFinished(t *testing.T) {
	a := Event{ID: "x", InFlight: true}
	b := Event{ID: "x", InFlight: false}
	if Merge(a, b).InFlight {
		t.Error("still in flight after a finished view was merged")
	}
}
