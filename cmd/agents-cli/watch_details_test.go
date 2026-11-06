package main

import (
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

func TestWatchThemeFadeAndCautiousIdle(t *testing.T) {
	theme := defaultWatchTheme()
	fresh := theme.rowColour("waiting", 0, false, false)
	old := theme.rowColour("waiting", time.Hour, false, false)
	if fresh == old {
		t.Fatal("old stops do not fade")
	}
	if theme.rowColour("idle", 0, false, false) != fresh {
		t.Fatal("uncertain idle must use amber")
	}
	if theme.rowColour("idle", 0, false, true) == fresh {
		t.Fatal("idle colour preference ignored")
	}
	if theme.rowColour("finished", 0, false, false) == fresh {
		t.Fatal("explicit finished must be green")
	}
	if theme.rowColour("error", 0, false, false) == fresh {
		t.Fatal("errors must be red")
	}
}
