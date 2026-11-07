package main

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

type watchAgents struct {
	Total   *int   `json:"total,omitempty"`
	Running *int   `json:"running,omitempty"`
	Source  string `json:"source"`
}

var (
	contextRemaining = regexp.MustCompile(`(?i)\b(?:context|ctx)\s+(\d{1,3})%\s+left\b|\b(\d{1,3})%\s+(?:context|ctx)\s+left\b`)
	agentFraction    = regexp.MustCompile(`(?i)\b(\d+)\s*/\s*(\d+)\s+(?:sub-?agents?|agents?)\s+running\b`)
	agentTotal       = regexp.MustCompile(`(?i)\b(\d+)\s+(?:sub-?agents?|agents?)\b`)
	agentRunning     = regexp.MustCompile(`(?i)\b(\d+)\s+(?:sub-?agents?|agents?)\s+(?:still\s+)?running\b|\b(\d+)\s+running\s+(?:sub-?agents?|agents?)\b`)
	agentSummary     = regexp.MustCompile(`(?i)\bagents?:?\s+(\d+)\s*\(\s*(\d+)\s+running\s*\)`)
	quotaAlert       = regexp.MustCompile(`(?i)^(?:you(?:'ve|’ve) (?:hit|reached) (?:your |the )?(?:usage|rate|weekly|monthly|session|token|credit) limit|you(?:'re|’re) out of (?:usage|credits|tokens)|(?:usage|rate|weekly|monthly|session|token|credit) limit (?:reached|exceeded)|quota (?:exceeded|exhausted)|out of quota|insufficient credits)`)
	completionBanner = regexp.MustCompile(`(?i)^(?:task (?:complete|completed)|all done|completed successfully|work complete)[.!]?$`)
	ownerQuestion    = regexp.MustCompile(`(?i)\b(?:do you|would you|could (?:you|i|we)|can (?:you|i|we)|may (?:i|we)|should (?:i|we)|shall (?:i|we)|which|what|how|when|where|who|does (?:that|this)|is (?:it|that|this)|are you)\b[^?]*\?`)
)

func watchContext(lines []string) string {
	tail := trimBlankTail(lines)
	for i := len(tail) - 1; i >= 0 && i >= len(tail)-3; i-- {
		text := strings.TrimSpace(tail[i])
		if strings.HasPrefix(text, "›") || strings.HasPrefix(text, "❯") || strings.HasPrefix(text, ">") {
			continue
		}
		if !strings.Contains(text, " · ") && !strings.Contains(text, " | ") {
			continue
		}
		if match := contextRemaining.FindStringSubmatch(text); match != nil {
			value := firstNonEmpty(match[1], match[2])
			if n, _ := strconv.Atoi(value); n <= 100 {
				return value + "% left"
			}
		}
	}
	return ""
}

func agentNumber(s string) *int { n, _ := strconv.Atoi(s); return &n }

// Only live footer summaries and explicitly labelled task panels are counts.
// History rows such as Codex's "Waiting for N agents" are never liveness.
func watchAgentDetails(tool string, lines []string) watchAgents {
	result := watchAgents{Source: "not shown"}
	if tool != "claude" && tool != "codex" && tool != "copilot" {
		return result
	}
	tail := trimBlankTail(lines)
	start := len(tail) - 3
	if start < 0 {
		start = 0
	}
	for _, line := range tail[start:] {
		text := strings.TrimSpace(line)
		if strings.HasPrefix(text, `"`) || strings.HasPrefix(text, "• ") || strings.HasPrefix(text, "● ") || strings.HasPrefix(text, "❯") || strings.HasPrefix(text, "›") {
			continue
		}
		if m := agentFraction.FindStringSubmatch(text); m != nil {
			return watchAgents{Total: agentNumber(m[2]), Running: agentNumber(m[1]), Source: "footer"}
		}
		if m := agentSummary.FindStringSubmatch(text); m != nil {
			return watchAgents{Total: agentNumber(m[1]), Running: agentNumber(m[2]), Source: "footer"}
		}
		if m := agentRunning.FindStringSubmatch(text); m != nil {
			n := firstNonEmpty(m[1], m[2])
			result.Running = agentNumber(n)
			result.Source = "footer"
		} else if m := agentTotal.FindStringSubmatch(text); m != nil {
			result.Total = agentNumber(m[1])
			result.Source = "footer (running not shown)"
		}
	}
	if result.Total != nil || result.Running != nil {
		return result
	}
	panel := -1
	for i, line := range tail {
		text := strings.ToLower(strings.Trim(strings.TrimSpace(line), "╭╮╰╯│─ "))
		if text == "background tasks" || text == "subagents" || text == "running agents" {
			panel = i
		}
	}
	if panel < 0 {
		return result
	}
	total, running := 0, 0
	for _, line := range tail[panel+1:] {
		text := strings.ToLower(strings.TrimSpace(line))
		if !strings.Contains(text, "agent ") && !strings.Contains(text, "[agent]") {
			continue
		}
		if strings.Contains(text, "[running]") || strings.Contains(text, "[pending]") {
			total++
			running++
		} else if strings.Contains(text, "[completed]") || strings.Contains(text, "[done]") || strings.Contains(text, "[failed]") || strings.Contains(text, "[stopped]") {
			total++
		}
	}
	if total > 0 {
		return watchAgents{Total: &total, Running: &running, Source: "visible task rows"}
	}
	return result
}

func (a watchAgents) label() string {
	if a.Total == nil && a.Running == nil {
		return "agents: not shown"
	}
	total, running := "?", "?"
	if a.Total != nil {
		total = fmt.Sprint(*a.Total)
	}
	if a.Running != nil {
		running = fmt.Sprint(*a.Running)
	}
	return fmt.Sprintf("agents: %s visible | %s running", total, running)
}

// Bare idle is deliberately distinct from an explicit completion. Each TUI
// can also use its empty composer after asking a natural-language question.
func watchPhase(tool string, lines []string, state string) (string, string) {
	tail := trimBlankTail(lines)
	start := len(tail) - 18
	if start < 0 {
		start = 0
	}
	for _, line := range tail[start:] {
		text := strings.Trim(strings.TrimSpace(line), "│ ║!✕×■⚠")
		if quotaAlert.MatchString(text) {
			return "error", "usage limit / quota"
		}
		lower := strings.ToLower(text)
		if state != "busy" && (strings.HasPrefix(lower, "error:") || strings.HasPrefix(lower, "api error:") || strings.HasPrefix(lower, "fatal:") || strings.HasPrefix(lower, "connection failed")) {
			return "error", "error on screen"
		}
	}
	if state == "busy" {
		return "working", ""
	}
	if tool == "codex" && (state == "idle" || state == "prompt") && watchCodexQuestion(lines) {
		return "question", "question to answer"
	}
	if state == "prompt" {
		return "waiting", "needs input"
	}
	if state == "idle" {
		if completionBanner.MatchString(watchLastOutput(tail)) {
			return "finished", "finished"
		}
		return "idle", "idle / check"
	}
	return "unknown", "state unavailable"
}

// Questions must belong to the latest visible assistant reply, above a live
// composer. A question in a command, past turn or input draft is not a signal.
func watchCodexQuestion(lines []string) bool {
	if codexComposerState(lines) == "" {
		return false
	}
	tail := trimBlankTail(lines)
	end := -1
	for i := len(tail) - 2; i >= 0; i-- {
		if strings.HasPrefix(strings.TrimSpace(tail[i]), "›") {
			end = i
			break
		}
	}
	if end < 0 {
		return false
	}
	start := -1
	for i := 0; i < end; i++ {
		text := strings.TrimSpace(tail[i])
		if strings.HasPrefix(text, "›") {
			start = -1
		}
		if strings.HasPrefix(text, "• ") {
			if strings.HasPrefix(text, "• Ran ") || strings.HasPrefix(text, "• Model changed ") {
				start = -1
				continue
			}
			start = i
		}
	}
	if start < 0 {
		return false
	}
	return ownerQuestion.MatchString(strings.Join(tail[start:end], " "))
}

func watchLastOutput(lines []string) string {
	end := len(lines)
	for i := len(lines) - 1; i >= 0; i-- {
		text := strings.TrimSpace(lines[i])
		if text == ">" || strings.HasPrefix(text, "❯") || strings.HasPrefix(text, "›") {
			end = i
			if i > 0 && strings.HasPrefix(strings.TrimSpace(lines[i-1]), "───") {
				end = i - 1
			}
			break
		}
	}
	for i := end - 1; i >= 0; i-- {
		text := strings.TrimSpace(lines[i])
		if text == "" || strings.HasPrefix(text, "───") || strings.Contains(text, " · done ") || strings.Contains(text, "new task? /clear") {
			continue
		}
		return strings.Trim(text, "●• ")
	}
	return ""
}
