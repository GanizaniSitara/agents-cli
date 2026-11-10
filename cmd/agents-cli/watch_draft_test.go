package main

import "testing"

func TestCodexTypingDoesNotRequestAttention(t *testing.T) {
	for _, draft := range []string{"hello", "Do you want to try this?", "Allow access", "Thinking about this"} {
		lines := []string{"• Ready.", "› " + draft, "  GPT-6-Sol high · Context 50% left"}
		state := guessState("codex", lines)
		phase, _ := watchPhase("codex", lines, state)
		if state != "idle" || phase != "idle" {
			t.Fatalf("draft %q: state=%s phase=%s", draft, state, phase)
		}
		lines[0] = "• Which option should I use?"
		if phase, _ := watchPhase("codex", lines, guessState("codex", lines)); phase != "question" {
			t.Fatalf("draft hid assistant question: %s", phase)
		}
		lines[0] = "Do you want to allow this command? (y/n)"
		if state := guessState("codex", lines); state != "prompt" {
			t.Fatalf("draft hid approval request: %s", state)
		}
	}
}

func TestClaudeLiveNamedDraftWait(t *testing.T) {
	lines := []string{"✻ Churned for 6m 16s · done 15:39", "──────────────────────── Example ─", "❯ queued review request", "────────────────────────────", "admin | model | ctx 54% left", "⏵⏵ bypass permissions on"}
	if got := guessState("claude", lines); got != "idle" {
		t.Fatalf("live named draft wait: %s", got)
	}
	lines = append([]string{"✽ Thinking… (6s · esc to interrupt)"}, lines...)
	if got := guessState("claude", lines); got != "busy" {
		t.Fatalf("draft during active turn: %s", got)
	}
}
