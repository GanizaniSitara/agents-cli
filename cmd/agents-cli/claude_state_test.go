package main

import "testing"

func TestClaudeDraftAndRunningAttention(t *testing.T) {
	frame := func(output, draft string) []string {
		return []string{output, "────────────────────────", "> " + draft, "────────────────────────", "admin | Opus 5.5", "auto mode on (shift+tab to cycle)"}
	}
	for _, draft := range []string{"we need the ticket", "Allow access?", "Do you want to Approve this?", "Thinking about this"} {
		for _, output := range []string{"", "The earlier question was: Do you want to allow this?"} {
			lines := frame(output, draft)
			if got := guessState("claude", lines); got != "idle" {
				t.Fatalf("draft %q: %s", draft, got)
			}
			lines[0] = "✽ Thinking… (6s · esc to interrupt)"
			if got := guessState("claude", lines); got != "busy" {
				t.Fatalf("running with draft %q: %s", draft, got)
			}
			phase, _ := watchPhase("claude", lines, guessState("claude", lines))
			if phase != "working" {
				t.Fatalf("running phase = %s", phase)
			}
		}
	}
	approval := []string{"✽ Thinking… (6s · esc to interrupt)", "Do you want to allow this command?", "❯ 1. Yes, allow once", "  2. No"}
	if got := guessState("claude", approval); got != "prompt" {
		t.Fatalf("actual approval hidden: %s", got)
	}
	cleared := frame("Command completed.", "")
	if got := guessState("claude", cleared); got != "idle" {
		t.Fatalf("cleared approval = %s", got)
	}
}
