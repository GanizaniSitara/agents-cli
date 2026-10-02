package main

import "testing"

// Screens captured from live sessions on 2026-10-01; the heuristic must keep
// reading them the same way.
func TestGuessStateLiveScreens(t *testing.T) {
	agyIdle := []string{
		"      ▄▀▀▄        Antigravity CLI 1.2.14",
		"    ▀▀▀▀▀▀▀▀      Gemini 3.1 Pro (High)",
		"────────────────────────────────────────",
		">",
		"────────────────────────────────────────",
		"? for shortcuts                                Gemini 3.1 Pro · high",
		"",
	}
	if s := guessState("agy", agyIdle); s != "idle" {
		t.Errorf("agy idle screen -> %s", s)
	}
	agyTrust := []string{
		"Do you trust the contents of this project?",
		"> Yes, I trust this folder",
		"  No, exit",
		"  ↑/↓ Navigate · enter Confirm",
	}
	if s := guessState("agy", agyTrust); s != "prompt" {
		t.Errorf("agy trust dialog -> %s", s)
	}
	claudeBusy := []string{
		"✢ Misting… (10s · ↓ 550 tokens)",
		"",
		"> We should probably run it every day to be honest.",
		"  ctrl+x ctrl+s to send now",
	}
	if s := guessState("claude", claudeBusy); s != "busy" {
		t.Errorf("claude busy screen -> %s", s)
	}
	codexBusy := []string{
		"Working (4m 43s • esc to interrupt)",
		"",
		"› Ask Codex to do anything",
	}
	if s := guessState("codex", codexBusy); s != "busy" {
		t.Errorf("codex busy screen -> %s", s)
	}
	// A real window has dozens of blank rows under the TUI; they must not hide it.
	for i := 0; i < 40; i++ {
		agyIdle = append(agyIdle, "")
	}
	if s := guessState("agy", agyIdle); s != "idle" {
		t.Errorf("agy idle screen with blank tail -> %s", s)
	}
	cmdIdle := []string{"ping-from-agents-cli", "", `C:\git\agents-cli>`, ""}
	if s := guessState("other", cmdIdle); s != "idle" {
		t.Errorf("cmd prompt -> %s", s)
	}
}
