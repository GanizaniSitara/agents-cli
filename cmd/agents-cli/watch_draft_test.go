package main

import "testing"

func TestClaudeLiveNamedDraftWait(t *testing.T) {
	lines := []string{"✻ Churned for 6m 16s · done 15:39", "──────────────────────── Example ─", "❯ queued review request", "────────────────────────────", "admin | model | ctx 54% left", "⏵⏵ bypass permissions on"}
	if got := guessState("claude", lines); got != "prompt" {
		t.Fatalf("live named draft wait: %s", got)
	}
	lines = append([]string{"✽ Thinking… (6s · esc to interrupt)"}, lines...)
	if got := guessState("claude", lines); got != "busy" {
		t.Fatalf("draft during active turn: %s", got)
	}
}
