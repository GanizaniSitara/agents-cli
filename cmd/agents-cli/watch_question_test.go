package main

import "testing"

func TestCodexLatestReplyQuestion(t *testing.T) {
	lines := []string{"• Yes. The repair is staged.", "", "  For the email itself, do you mean: Tab focuses the body pane then Esc returns?", "", "  Down still selects the next email.", "", "  Worked for 9m 29s", "", "› Ask Codex to do anything", "", "  GPT-6.1-Sol medium · Context 33% left"}
	if phase, _ := watchPhase("codex", lines, "idle"); phase != "question" {
		t.Fatalf("question missed: %s", phase)
	}
	if phase, _ := watchPhase("codex", lines, "busy"); phase != "working" {
		t.Fatalf("busy overwritten: %s", phase)
	}
	if watchProminentStatus("question") != "QUESTION" {
		t.Fatal("question category absent")
	}
	if defaultWatchTheme().rowColour("question") != defaultWatchTheme().rowColour("waiting") {
		t.Fatal("question should use attention colour")
	}
}

func TestCodexQuestionExcludesHistoryDraftAndToolText(t *testing.T) {
	footer := "  GPT-6-Sol high · Context 50% left"
	for _, lines := range [][]string{
		{"• Do you want the red option?", "› Use blue", "• Done. Blue is selected.", "› Ask Codex to do anything", footer},
		{"• Done.", "› Do you want to try another approach?", footer},
		{"• Ran echo Which option should I use?", "  └ output", "› Ask Codex to do anything", footer},
	} {
		if watchCodexQuestion(lines) {
			t.Fatalf("false question: %v", lines)
		}
	}
}
