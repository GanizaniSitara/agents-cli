package main

import (
	"regexp"
	"strings"
)

var claudeChoice = regexp.MustCompile(`(?m)^\s*[>❯›]?\s*[1-9]\.\s+`)

// The composer can contain a queued draft during a running turn. Its text is
// neither an approval request nor evidence of activity.
func claudeState(lines []string) string {
	tail := trimBlankTail(lines)
	if len(tail) > 24 {
		tail = tail[len(tail)-24:]
	}
	signals := append([]string(nil), tail...)
	composer := false
	bottom := -1
	for i := len(tail) - 1; i >= 0; i-- {
		text := strings.TrimSpace(tail[i])
		if !strings.HasPrefix(text, "───") || !strings.HasSuffix(text, "─") {
			continue
		}
		if bottom >= 0 && i+1 < bottom {
			first := strings.TrimSpace(tail[i+1])
			if strings.HasPrefix(first, ">") || strings.HasPrefix(first, "❯") {
				signals = append(append([]string(nil), tail[:i]...), tail[bottom+1:]...)
				composer = true
				break
			}
		}
		bottom = i
	}
	joined := strings.Join(signals, "\n")
	// A live choice dialog still needs attention even if a spinner remains.
	if !composer && promptRe.MatchString(joined) && claudeChoice.MatchString(joined) {
		return "prompt"
	}
	if spinnerRe.MatchString(joined) {
		return "busy"
	}
	if composer {
		return "idle"
	}
	if promptRe.MatchString(joined) {
		return "prompt"
	}
	for _, line := range signals {
		if idleRe.MatchString(strings.TrimSpace(line)) {
			return "idle"
		}
	}
	return "unknown"
}
