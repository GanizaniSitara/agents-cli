package main

import "testing"

func TestCodexReviewHandoff(t *testing.T) {
	footer := "  GPT-6.1-Sol medium · Context 80% left"
	for _, reply := range []string{
		"  Try: Tab → body → Tab → Connected Accounts. Down enters the email; Esc returns to pane navigation.",
		"  Please review the result.",
		"  Ready for your review.",
	} {
		lines := []string{"• Applied and reopened Mailkeep without taking focus. HTML loaded successfully.", reply, "  Worked for 44s • 17:09", "› Ask Codex to do anything", footer}
		if phase, _ := watchPhase("codex", lines, "idle"); phase != "review" {
			t.Fatalf("missed review %q: %s", reply, phase)
		}
		if phase, _ := watchPhase("codex", lines, "busy"); phase != "working" {
			t.Fatal("review overwrote running state")
		}
	}
	for _, lines := range [][]string{
		{"• Please review this.", "› Looks good", "• Saved.", "› Ask Codex to do anything", footer},
		{"• Saved.", "› Please review this", footer},
		{"• Ran echo Please review this", "  Please review this", "› Ask Codex to do anything", footer},
		{"• I checked the result. All checks passed.", "› Ask Codex to do anything", footer},
	} {
		if phase, _ := watchPhase("codex", lines, "idle"); phase == "review" {
			t.Fatalf("false review: %v", lines)
		}
	}
	entry := &watchEntry{Sample: watchSample{HWND: 1, DesktopID: "one", Phase: "review"}, Stopped: true, Attention: true}
	if watchTaskbarState(entry, "one") != 8 || defaultWatchTheme().rowColour("review") != defaultWatchTheme().rowColour("question") {
		t.Fatal("review should use yellow panel and taskbar")
	}
}
