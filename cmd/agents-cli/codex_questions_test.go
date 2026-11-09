package main

import (
	"strings"
	"testing"
	"time"
)

func queuedQuestionScreen(body string) []string {
	return strings.Split(body+"\n\n› Ask Codex to do anything\n\n  GPT-6.1-Sol low · Context 57% left", "\n")
}

func TestCodexQueuedQuestions(t *testing.T) {
	queue := "• Queued follow-up inputs\n  ? 1 question\n    shift+← to answer"
	working := "Working (26m • esc to interrupt) · 1 background terminal running"
	for _, tc := range []struct {
		name, body, state string
		count             int
		working           bool
	}{
		{"busy question", working + "\n\n" + queue, "prompt", 1, true},
		{"background question", "Waiting for background terminal (26m • esc to interrupt)\n  └ generic command\n\n" + queue, "prompt", 1, true},
		{"busy without question", working, "busy", 0, false},
		{"historical queue", queue + "\n\n• Answered earlier.\n" + working, "busy", 0, false},
		{"historical prose", "• The queued follow-up inputs had 1 question; shift+left to answer.\n" + working, "busy", 0, false},
		{"cleared", "• Answer received.", "idle", 0, false},
		{"zero questions", working + "\n• Queued follow-up inputs\n  ? 0 questions\n    shift+← to answer", "busy", 0, false},
		{"idle queue", queue, "prompt", 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lines := queuedQuestionScreen(tc.body)
			count, active := codexQueuedQuestions(lines)
			if count != tc.count || active != tc.working || guessState("codex", lines) != tc.state {
				t.Fatalf("count=%d working=%v state=%s", count, active, guessState("codex", lines))
			}
			if count > 0 {
				phase, reason := watchPhase("codex", lines, tc.state)
				if phase != "waiting" || !strings.Contains(reason, "question to answer") {
					t.Fatalf("phase=%s reason=%s", phase, reason)
				}
			}
		})
	}
	// No live footer: even an exact UI example is transcript, not a queue.
	if n, _ := codexQueuedQuestions(strings.Split(queue, "\n")); n != 0 {
		t.Fatal("unframed transcript became actionable")
	}
}

func TestCodexQueuedQuestionAttentionWhileWorking(t *testing.T) {
	w := newWatchTracker(2 * time.Second)
	now := time.Now()
	s := watchSample{Session: Session{PID: 1, Tool: "codex", State: "prompt"}, HWND: 11,
		DesktopID: "one", Phase: "waiting", PendingQuestions: 1, Working: true,
		Agents: watchAgents{Running: agentNumber("2")}}
	if !w.observe(s, now) {
		t.Fatal("live question did not immediately request attention")
	}
	e := w.entries[1]
	if e.Sample.State != "prompt" || !e.Sample.Working || !e.Attention || !e.Stopped || watchPanelGroup(e.Sample) != 0 {
		t.Fatalf("question overwritten by concurrent work: %+v", e)
	}
	c := watchFlashController{}
	on := false
	c.update(w.entries, "one", func(_ uintptr, value bool) { on = value })
	if !on {
		t.Fatal("live question did not flash")
	}
	s.Signature = "changed spinner and transcript"
	if w.observe(s, now.Add(time.Second)) || !e.Attention {
		t.Fatal("changing work repeated or cleared pending attention")
	}
	s.PendingQuestions, s.State, s.Phase = 0, "busy", "working"
	w.observe(s, now.Add(2*time.Second))
	c.update(w.entries, "one", func(_ uintptr, value bool) { on = value })
	if e.Attention || e.Stopped || on {
		t.Fatal("answered question retained attention")
	}
	s.PendingQuestions, s.State, s.Phase = 1, "prompt", "waiting"
	if !w.observe(s, now.Add(3*time.Second)) {
		t.Fatal("new question did not rearm attention")
	}
}
