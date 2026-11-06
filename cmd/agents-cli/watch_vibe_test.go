package main

import "testing"

func TestVibeLiveDraftWait(t *testing.T) {
	lines := []string{"Task complete.", "──────────────── accept edits · auto-approve ─", "> yes", "", "", "────────────────────────────", "~ Mistral Large 4[high] · 97.5k/202k tokens (48%)"}
	if got := guessState("vibe", lines); got != "prompt" {
		t.Fatalf("live draft wait missed: %s", got)
	}
	lines[2] = ">"
	if got := guessState("vibe", lines); got != "idle" {
		t.Fatalf("empty composer: %s", got)
	}
	lines = append([]string{"⠙ Initializing…"}, lines...)
	if got := guessState("vibe", lines); got != "busy" {
		t.Fatalf("initializing: %s", got)
	}
}
