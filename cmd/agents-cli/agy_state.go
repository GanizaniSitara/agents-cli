package main

import (
	"regexp"
	"strings"
)

var (
	agyRunningTask = regexp.MustCompile(`^\s*●\s+\[\d{2}:\d{2}:\d{2}\]\s+.+\s+running\s*$`)
	agyLiveSpinner = regexp.MustCompile(`(?i)^\s*[\x{2800}-\x{28FF}]\s+(?:running command|thinking|generating|working)\b`)
)

// AGY keeps an idle composer while background commands run. Only the live
// task strip below that composer is liveness; transcript prose is not.
func agyState(lines []string) string {
	tail := trimBlankTail(lines)
	if copilotMenu(tail) {
		return "prompt"
	}
	if agyBusy(tail) {
		return "busy"
	}
	for i := len(tail) - 2; i >= 0; i-- {
		if !agyBorder(tail[i]) || !strings.HasPrefix(strings.TrimSpace(tail[i+1]), ">") {
			continue
		}
		bottom := i + 2
		for bottom < len(tail) && !agyBorder(tail[bottom]) {
			bottom++
		}
		if bottom == len(tail) {
			continue
		}
		// A foreground progress line belongs immediately above the composer.
		if agyLiveSpinner.MatchString(watchLastOutput(tail[:i])) {
			return "busy"
		}
		if ownerQuestion.MatchString(watchLastOutput(tail[:i])) {
			return "prompt"
		}
		for _, line := range tail[bottom+1:] {
			if agyRunningTask.MatchString(line) {
				return "busy"
			}
		}
		if draft, ok := copilotComposer(tail[i : bottom+1]); ok && draft != "" {
			return "prompt"
		}
		return "idle"
	}
	// Redraws can temporarily expose only the current spinner, with no footer.
	if agyLiveSpinner.MatchString(watchLastOutput(tail)) {
		return "busy"
	}
	// Startup trust dialogs can omit their navigation footer and composer.
	for _, line := range tail {
		if strings.HasPrefix(strings.TrimSpace(line), "> Yes,") {
			return "prompt"
		}
	}
	return "unknown"
}

func agyBorder(line string) bool {
	text := strings.TrimSpace(line)
	return len([]rune(text)) >= 3 && strings.Trim(text, "─━") == ""
}
