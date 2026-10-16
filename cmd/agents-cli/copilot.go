package main

import "strings"

// Copilot keeps its live status below the composer. Transcript text (including
// echoed prompts and old permission questions) is not a status indicator.
func copilotState(lines []string) string {
	tail := trimBlankTail(lines)
	if len(tail) == 0 {
		return "unknown"
	}
	footer := strings.ToLower(strings.TrimSpace(tail[len(tail)-1]))
	if strings.Contains(footer, "enter") &&
		(strings.Contains(footer, "select") || strings.Contains(footer, "confirm")) {
		return "prompt"
	}
	if strings.Contains(footer, "esc interrupt") || strings.Contains(footer, "esc to interrupt") {
		return "busy"
	}
	if draft, ok := copilotComposer(tail); ok {
		if draft == "" {
			return "idle"
		}
		return "prompt"
	}
	return "unknown"
}

// The composer is enclosed by horizontal rules; it may wrap or scroll. Do not
// mistake a transcript's identical prompt glyph for the active input field.
func copilotComposer(lines []string) (string, bool) {
	bottom := -1
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if len([]rune(line)) < 3 || strings.Trim(line, "─━") != "" {
			continue
		}
		if bottom < 0 {
			bottom = i
			continue
		}
		if i+1 >= bottom {
			return "", false
		}
		first := strings.TrimSpace(lines[i+1])
		if !strings.HasPrefix(first, "❯") && !strings.HasPrefix(first, ">") {
			return "", false
		}
		draft := []string{strings.TrimSpace(first[len(string([]rune(first)[0])):])}
		for _, row := range lines[i+2 : bottom] {
			draft = append(draft, strings.TrimSpace(row))
		}
		return strings.TrimSpace(strings.Join(draft, "\n")), true
	}
	return "", false
}

func waitCanIdle(tool string, lines []string) bool {
	// Copilot's spinner can pause during a request, and menus can stay stable
	// indefinitely. For it, require both stability and an empty idle composer.
	return tool != "copilot" || copilotState(lines) == "idle"
}
