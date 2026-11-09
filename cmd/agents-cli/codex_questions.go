package main

import (
	"regexp"
	"strconv"
	"strings"
)

var codexQueuedCount = regexp.MustCompile(`^\? ([1-9][0-9]*) questions?$`)

// Only the live queue immediately above the composer is actionable. Transcript
// prose, quoted UI examples and draft text must not request owner attention.
func codexQueuedQuestions(lines []string) (count int, working bool) {
	if codexComposerState(lines) == "" {
		return 0, false
	}
	body := withoutCodexDraft(lines)
	end := len(body)
	previous := func() string {
		for end > 0 {
			end--
			if text := strings.TrimSpace(body[end]); text != "" {
				return text
			}
		}
		return ""
	}
	hint := previous()
	if hint != "shift+← to answer" && hint != "shift+left to answer" {
		return 0, false
	}
	match := codexQueuedCount.FindStringSubmatch(previous())
	if match == nil || previous() != "• Queued follow-up inputs" {
		return 0, false
	}
	count, _ = strconv.Atoi(match[1])
	// The running status precedes the queue, possibly with a terminal command
	// detail below it. Do not interpret older assistant prose as current work.
	for i := 0; i < 3 && end > 0; i++ {
		text := previous()
		if strings.Contains(text, "esc to interrupt") {
			working = true
			break
		}
	}
	return count, working
}
