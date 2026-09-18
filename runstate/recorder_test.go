package runstate

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRecorderAppendLoadAndList(t *testing.T) {
	root := t.TempDir()
	recorder, err := NewRecorder(root)
	if err != nil {
		t.Fatal(err)
	}
	for index := 1; index <= 3; index++ {
		if err := recorder.Append(Event{Sequence: uint64(index), RunID: "run-1", Kind: KindPhaseChanged, Message: "phase"}); err != nil {
			t.Fatal(err)
		}
	}
	events, err := recorder.Load("run-1", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].Sequence != 2 {
		t.Fatalf("unexpected replay: %#v", events)
	}
	ids, err := recorder.List()
	if err != nil || len(ids) != 1 || ids[0] != "run-1" {
		t.Fatalf("unexpected run list: %v %v", ids, err)
	}
}

func TestRecorderRejectsSymlinkLog(t *testing.T) {
	root := t.TempDir()
	recorder, err := NewRecorder(root)
	if err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.jsonl")
	if err := os.WriteFile(outside, []byte("safe"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "sessions", "runs", "run-1.jsonl")
	if err := os.Symlink(outside, link); err != nil {
		t.Skip(err)
	}
	if err := recorder.Append(Event{RunID: "run-1", Kind: KindRunStarted}); err == nil {
		t.Fatal("expected symlink rejection")
	}
	data, err := os.ReadFile(outside)
	if err != nil || string(data) != "safe" {
		t.Fatalf("outside target changed: %q %v", data, err)
	}
}

func TestRecorderRejectsInvalidRunID(t *testing.T) {
	recorder, err := NewRecorder(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := recorder.Append(Event{RunID: "../escape", Kind: KindRunStarted}); err == nil {
		t.Fatal("expected invalid run id rejection")
	}
}
