package main

import "strings"

func framedDraftState(tool string, lines []string) string {
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
