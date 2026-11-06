package main

import "strings"

// Current Vibe frames its composer with a named mode rule. Reuse the framed
// composer reader so a typed draft is a wait, rather than an unknown screen.
// Return empty when the frame is absent and let the shared heuristic handle
// startup logos and standalone permission dialogs.
func vibeState(lines []string) string {
	if copilotMenu(lines) {
		return "prompt"
	}
	normalized := append([]string(nil), trimBlankTail(lines)...)
	top := -1
	for i, line := range normalized {
		text := strings.TrimSpace(line)
		if strings.HasPrefix(text, "─") && (strings.Contains(text, "accept edits") || strings.Contains(text, "auto-approve") || strings.Contains(text, "smart-approve")) {
			normalized[i] = "────────"
			top = i
		}
	}
	if top < 0 {
		return ""
	}
	draft, ok := copilotComposer(normalized)
	if !ok {
		return ""
	}
	start := top - 4
	if start < 0 {
		start = 0
	}
	for _, line := range normalized[start:top] {
		if strings.Contains(line, "to interrupt") || strings.Contains(line, "Initializing…") {
			return "busy"
		}
	}
	if draft != "" {
		return "prompt"
	}
	return "idle"
}
