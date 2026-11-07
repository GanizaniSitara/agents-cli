package main

import "strings"

func framedDraftState(tool string, lines []string) string {
	if tool == "codex" {
		return codexComposerState(lines)
	}
	if tool != "claude" && tool != "agy" {
		return ""
	}
	normalized := append([]string(nil), lines...)
	for i, line := range normalized {
		text := strings.TrimSpace(line)
		if strings.HasPrefix(text, "───") && strings.HasSuffix(text, "─") {
			normalized[i] = "────────"
		}
	}
	draft, ok := copilotComposer(normalized)
	if !ok {
		return ""
	}
	if draft != "" {
		return "prompt"
	}
	return "idle"
}

// The live Codex composer sits immediately above its model/context footer.
func codexComposerState(lines []string) string {
	tail := trimBlankTail(lines)
	if len(tail) == 0 {
		return ""
	}
	footer := strings.TrimSpace(tail[len(tail)-1])
	if !strings.HasPrefix(strings.ToLower(footer), "gpt-") || watchContext([]string{footer}) == "" {
		return ""
	}
	for i := len(tail) - 2; i >= 0; i-- {
		text := strings.TrimSpace(tail[i])
		if strings.HasPrefix(text, "›") {
			return "idle"
		}
		if text != "" && !strings.HasPrefix(tail[i], "  ") {
			return ""
		}
	}
	return ""
}

// User draft text is not evidence of an approval prompt or running turn.
func withoutCodexDraft(lines []string) []string {
	if codexComposerState(lines) == "" {
		return lines
	}
	for i := len(lines) - 2; i >= 0; i-- {
		if strings.HasPrefix(strings.TrimSpace(lines[i]), "›") {
			return lines[:i]
		}
	}
	return lines
}
