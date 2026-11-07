package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestVisibleAgentCountsAndRunningGate(t *testing.T) {
	for _, tool := range []string{"claude", "codex", "copilot"} {
		a := watchAgentDetails(tool, []string{"old answer: 99 agents running", "────────", "❯", "────────", "2/3 agents running"})
		if a.Total == nil || *a.Total != 3 || a.Running == nil || *a.Running != 2 {
			t.Fatalf("%s counts: %+v", tool, a)
		}
		w := newWatchTracker(time.Second)
		s := watchSample{Session: Session{PID: 1, State: "idle"}, Agents: a}
		w.observe(s, time.Unix(100, 0))
		w.observe(s, time.Unix(102, 0))
		if len(w.rows()) != 0 {
			t.Fatalf("%s running agents counted as stopped", tool)
		}
	}
	a := watchAgentDetails("claude", []string{"old transcript: 7 agents running", "────────", "❯", "────────", "⏵⏵ auto mode on · ← 10 agents · 1 monitor"})
	if a.Total == nil || *a.Total != 10 || a.Running != nil {
		t.Fatalf("footer count is not a running count: %+v", a)
	}
	a = watchAgentDetails("codex", []string{"• Waiting for 3 agents", "• Finished waiting", "› Ask Codex to do anything", "model · ctx left"})
	if a.Running != nil {
		t.Fatal("history was treated as live agent status")
	}
	a = watchAgentDetails("copilot", []string{"Background tasks", "Agent review [running]", "Agent audit [completed]", "enter to select · esc to close"})
	if a.Total == nil || *a.Total != 2 || a.Running == nil || *a.Running != 1 {
		t.Fatalf("visible task list: %+v", a)
	}
}

func TestWatchTrialPhases(t *testing.T) {
	if p, _ := watchPhase("claude", []string{"Task complete.", "Would you like me to continue?", "────────", "❯", "────────"}, "idle"); p != "idle" {
		t.Fatal("an old completion banner hid a later question")
	}
	for _, tool := range []string{"claude", "codex", "copilot", "vibe", "agy"} {
		if p, _ := watchPhase(tool, []string{"❯"}, "idle"); p != "idle" {
			t.Fatalf("%s uncertain idle should stay distinct", tool)
		}
		if p, _ := watchPhase(tool, []string{"Task complete.", "────────", "❯", "────────"}, "idle"); p != "finished" {
			t.Fatalf("explicit completion: %s", p)
		}
		if p, _ := watchPhase(tool, []string{"You've hit your usage limit. Try again later.", "❯"}, "prompt"); p != "error" {
			t.Fatalf("quota not red: %s", p)
		}
		if p, _ := watchPhase(tool, []string{"Error: connection failed", "❯"}, "busy"); p != "working" {
			t.Fatal("recovering tool error must not override active work")
		}
	}
}

func TestWatchFixedStateColours(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "theme.json"), []byte(`{"fade_seconds":1,"minimum_brightness":0.1}`), 0600); err != nil {
		t.Fatal(err)
	}
	theme := loadWatchTheme(dir)
	seen := map[uintptr]string{}
	for _, phase := range []string{"waiting", "error", "finished", "working", "idle"} {
		colour := theme.rowColour(phase)
		if previous, ok := seen[colour]; ok {
			t.Fatalf("%s shares colour with %s", phase, previous)
		}
		seen[colour] = phase
	}
	if theme.rowColour("unknown") != theme.rowColour("idle") {
		t.Fatal("unknown and idle use the same blue treatment")
	}
	// Colour conversion has no age argument: persisted fade fields cannot
	// make the same state change appearance as its age counter advances.
	if theme.colour(theme.Waiting) != theme.rowColour("waiting") {
		t.Fatal("waiting colour changed")
	}
}

func TestWatchStatusesShareInputTileFormat(t *testing.T) {
	for phase, label := range map[string]string{"waiting": "NEEDS INPUT", "idle": "IDLE", "unknown": "UNKNOWN", "finished": "FINISHED", "error": "ERROR"} {
		if got := watchProminentStatus(phase); got != label {
			t.Fatalf("%s: got %q, want %q", phase, got, label)
		}
	}
	if watchProminentStatus("working") != "" {
		t.Fatal("working should retain its quiet row")
	}
}
