package main

import "testing"

func TestCodexMailkeepRestartQuestion(t *testing.T) {
	lines := []string{
		"• Fixed and passed all nine VM checks:",
		"  • Dotted-row selection and HTML rendering also passed.",
		"  Ready to apply. May I briefly close and reopen Mailkeep? You previously asked me to check before restarting it.",
		"  Worked for 8m 6s • 11:06",
		"› Ask Codex to do anything",
		"  GPT-6.1-Sol medium · Context 81% left",
	}
	if phase, _ := watchPhase("codex", lines, guessState("codex", lines)); phase != "question" {
		t.Fatalf("missed Mailkeep approval question: %s", phase)
	}
	answered := append(append([]string{}, lines[:4]...), "› yes", "• I will apply the update now.", lines[4], lines[5])
	if watchCodexQuestion(answered) {
		t.Fatal("answered question still requests attention")
	}
}

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
