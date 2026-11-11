package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWatchCurrentQuotaAndPolicyFailures(t *testing.T) {
	for _, tc := range []struct{ text, phase string }{
		{"You've hit your usage limit. Try again later.", "error"},
		{"Error: quota exceeded", "error"},
		{"You have exhausted your premium request allowance.", "error"},
		{"No remaining credits", "error"},
		{"Error: This command is blocked by your organization's policy.", "blocked"},
		{"Request denied by policy", "blocked"},
		{"Blocked by enterprise policy", "blocked"},
		{"Policy violation: tool execution not allowed", "blocked"},
	} {
		for _, tool := range []string{"copilot", "codex", "claude", "vibe", "agy"} {
			if phase, _ := watchPhase(tool, []string{tc.text, ">"}, "idle"); phase != tc.phase {
				t.Errorf("%s %q: %s, want %s", tool, tc.text, phase, tc.phase)
			}
		}
	}
	for _, lines := range [][]string{
		{"Error: quota exceeded", "Retry succeeded", ">"},
		{"Request denied by policy", "Command completed successfully", ">"},
		{`> explain "Request denied by policy"`, ">"},
		{"The documentation explains quota exceeded and policy violation messages.", ">"},
	} {
		phase, _ := watchPhase("copilot", lines, "busy")
		if phase == "blocked" || phase == "error" {
			t.Fatalf("history/draft/recovery became a current failure: %v", lines)
		}
	}
	theme := defaultWatchTheme()
	if theme.rowColour("blocked") != theme.rowColour("error") || watchProminentStatus("blocked") != "BLOCKED" {
		t.Fatal("policy block must have red BLOCKED presentation")
	}
	rows := watchActiveRows([]watchEntry{{Sample: watchSample{Session: Session{State: "error"}, HWND: 10, Phase: "blocked"}}}, 10)
	if rows[0].Sample.Phase != "blocked" {
		t.Fatal("focusing a blocked session hid its failure")
	}
}

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

func TestRunningAgentLabelsPreserveUnknownCounts(t *testing.T) {
	for _, tc := range []struct {
		agents watchAgents
		want   string
	}{
		{watchAgents{}, "agents: unknown"},
		{watchAgents{Total: agentNumber("11")}, "agents: ?/11 running"},
		{watchAgents{Running: agentNumber("0"), Total: agentNumber("11")}, "agents: 0/11 running"},
		{watchAgents{Running: agentNumber("2"), Total: agentNumber("3")}, "agents: 2/3 running"},
		{watchAgents{Running: agentNumber("2")}, "agents: 2 running"},
	} {
		if got := tc.agents.label(); got != tc.want {
			t.Fatalf("got %q, want %q", got, tc.want)
		}
	}
	a := watchAgentDetails("codex", []string{"Working (21s) · 5 background terminals running", "› Ask Codex to do anything", "GPT-6 · Context 28% left · ← for agents"})
	if a.Running != nil || a.Total != nil {
		t.Fatal("background terminals counted as agents")
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
