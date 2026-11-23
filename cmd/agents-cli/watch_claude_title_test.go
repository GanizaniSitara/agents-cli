package main

import "testing"

func TestClaudeRenameSuggestionWithCaretAtStart(t *testing.T) {
	r := peekResult{Lines: []string{"──── old ─", "❯\u00a0suggested follow-up", "────", "auto mode on"}, ComposerStartRow: 2}
	if !claudeRenameReady(&r) {
		t.Fatal("suggestion blocked rename")
	}
	r.ComposerStartRow = 0
	if claudeRenameReady(&r) {
		t.Fatal("accepted draft with unknown caret")
	}
	r.Lines[1] = "❯ /rename Example existing text"
	draft, _, ok := claudeRenameFrame(r.Lines)
	if !ok || draft == "/rename Example" {
		t.Fatal("accepted mixed input")
	}
}
