package main

import "testing"

func TestCodexWrappedLiveDraft(t *testing.T) {
	lines := []string{"  Worked for 2m 52s", "• Model changed to gpt-6-astra medium", "", "› I'm having a frustrating experience", "  figure it out please.", "", "  GPT-6-Astra medium · ~ · Context 36% left · Complete JPMT-215 workbook task"}
	if got := guessState("codex", lines); got != "prompt" {
		t.Fatalf("live draft: %s", got)
	}
	if got := watchContext(lines); got != "36% left" {
		t.Fatalf("context: %q", got)
	}
	lines = append([]string{"Working (3s • esc to interrupt)"}, lines...)
	if got := guessState("codex", lines); got != "busy" {
		t.Fatalf("active turn with draft: %s", got)
	}
}

func TestCodexComposerRequiresFooter(t *testing.T) {
	if got := codexComposerState([]string{"› old request", "• assistant response", "  GPT-6-Astra medium · Context 36% left"}); got != "" {
		t.Fatalf("transcript became live draft: %s", got)
	}
	if got := codexComposerState([]string{"› Ask Codex to do anything", "", "  GPT-6-Sol high · Context 70% left"}); got != "idle" {
		t.Fatalf("empty composer: %s", got)
	}
}

func TestWatchContextFooterOnly(t *testing.T) {
	for _, tc := range []struct {
		lines []string
		want  string
	}{
		{[]string{"› Context 99% left", "GPT-6-Astra · Context 36% left"}, "36% left"},
		{[]string{"user | model | ctx 54% left"}, "54% left"},
		{[]string{"model · 42% context left"}, "42% left"},
		{[]string{"› Please say context 90% left · example"}, ""},
		{[]string{"model · Context 120% left"}, ""},
		{[]string{"unreadable"}, ""},
	} {
		if got := watchContext(tc.lines); got != tc.want {
			t.Fatalf("%v: got %q, want %q", tc.lines, got, tc.want)
		}
	}
}
