package main

import "testing"

// Captured from an isolated installed Copilot console on 6 Nov 2026. Its
// navigation footer is inside a box, so the bottom border is the last row.
func TestCopilotBoxedTrustWait(t *testing.T) {
	lines := []string{"│ Confirm folder trust │", "│ Do you trust the files in this folder? │", "│ ❯ 1. Yes │", "│   2. Yes, and remember this folder for future sessions │", "│   3. No (Esc) │", "│ ↑/↓ to navigate · enter to select · esc to cancel │", "╰────────────────────────────────────────────────────╯"}
	if got := copilotState(lines); got != "prompt" {
		t.Fatalf("live trust wait missed: %s", got)
	}
	if copilotState(copilotScreen("", "Working esc interrupt")) != "busy" {
		t.Fatal("busy footer regressed")
	}
	if copilotState([]string{"An old transcript says enter to select · esc to cancel", "────────", "❯", "────────", "/ commands · ? help"}) != "idle" {
		t.Fatal("transcript became a menu")
	}
}
