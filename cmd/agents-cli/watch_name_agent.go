package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

func watchCanReviewName(s watchSample) bool {
	return s.SuggestedName != "" || s.NamingAgentStarted != ""
}

func watchAgentNameReply(lines []string, token string) string {
	for _, line := range lines {
		line = strings.Trim(strings.TrimSpace(line), "`")
		if !strings.HasPrefix(line, token+":") {
			continue
		}
		name := strings.TrimSpace(strings.TrimPrefix(line, token+":"))
		if watchValidName(name) && len(strings.Fields(name)) >= 2 && !strings.ContainsAny(name, "<>{}") {
			return name
		}
	}
	return ""
}

// A request is sent once, after validation and an empty idle composer check.
// Uncertain submission never retries: it may already be running in the agent.
func watchRequestAgentName(tool, token string, validate func() error, read func() ([]string, error), send func(string) (bool, error), pause func() error) (string, error) {
	if err := validate(); err != nil {
		return "", err
	}
	lines, err := read()
	if err != nil {
		return "", err
	}
	if guessState(tool, lines) != "idle" {
		return "", errors.New("Agent is busy or waiting for input. Try Ask agent when it is idle.")
	}
	if tool == "agy" || tool == "copilot" {
		if draft, ok := copilotComposer(lines); !ok || draft != "" {
			return "", errors.New("Clear the agent's draft before asking for a name.")
		}
	} else {
		empty := false
		for i := len(lines) - 1; i >= 0; i-- {
			line := strings.TrimSpace(lines[i])
			if strings.HasPrefix(line, "›") || strings.HasPrefix(line, "❯") || strings.HasPrefix(line, ">") {
				empty = line == "›" || line == "❯" || line == ">"
				break
			}
		}
		if !empty {
			return "", errors.New("An empty agent composer could not be confirmed. No request sent.")
		}
	}
	if err := validate(); err != nil {
		return "", err
	}
	prompt := "Agents-cli naming request. Using this conversation's existing topic, suggest a specific 2-6 word window title, at most 60 characters. Reply with one plain line starting with " + token + " followed immediately by a colon, a space, and the title. Do not rename anything, run tools, or change files. This is a suggestion for the owner to review."
	submitted, err := send(prompt)
	if err != nil {
		return "", err
	}
	if !submitted {
		return "", errors.New("Submission not confirmed. Check the agent before trying again; nothing was resent.")
	}
	for {
		if err := pause(); err != nil {
			return "", errors.New("No name received in time. The agent may still reply; nothing was resent.")
		}
		if err := validate(); err != nil {
			return "", err
		}
		lines, err = read()
		if err != nil {
			return "", err
		}
		if name := watchAgentNameReply(lines, token); name != "" {
			return name, nil
		}
	}
}

func watchAskAgentName(review watchNameReview) (string, error) {
	if review.Action != "suggest" || !loadWatchSettings(review.Dir).SuggestSessionNames {
		return "", errors.New("Ask agent requires an explicit request and enabled naming suggestions.")
	}
	s := review.Sample
	ctx, cancel := context.WithTimeout(operationContext, 65*time.Second)
	defer cancel()
	validate := func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if problem := watchTargetProblem(s, watchReadTarget(s)); problem != "" {
			return errors.New(problem)
		}
		if s.NamingAgentStarted == "" || startTime(s.AgentPID).Format(time.RFC3339Nano) != s.NamingAgentStarted {
			return errors.New("Agent process changed. Reopen the name dialog.")
		}
		if watchReadTarget(s).WindowTitle != s.Title && watchWindowClass(s.HWND) != "CASCADIA_HOSTING_WINDOW_CLASS" {
			return errors.New("Window title changed. Reopen the name dialog.")
		}
		return nil
	}
	// Discovery enforces muted sessions and checks actual AGY background work,
	// rather than assuming a visible prompt means the agent is idle.
	live, procs, err := lookup(strconv.FormatUint(uint64(s.PID), 10))
	if err != nil {
		return "", err
	}
	if err := requirePolling(live); err != nil {
		return "", err
	}
	if live.AgentPID != s.AgentPID || agyRuntimeWork(*live, procs, commandLine, startTime) > 0 {
		return "", errors.New("Agent changed or has running background work. Try again when idle.")
	}
	var nonce [8]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", err
	}
	token := "AGENTSCLI_NAME_" + hex.EncodeToString(nonce[:])
	read := func() ([]string, error) {
		r, err := peek(s.PID, 0)
		if err != nil {
			return nil, err
		}
		return r.Lines, nil
	}
	send := func(prompt string) (bool, error) {
		exe, err := os.Executable()
		if err != nil {
			return false, err
		}
		cmd := helperCommand(ctx, exe, prompt) // detached, stdin-only prompt
		cmd.Args = []string{exe, "send", strconv.FormatUint(uint64(s.PID), 10), "--stdin", "--via", "inject"}
		cmd.WaitDelay = 250 * time.Millisecond
		out, err := cmd.Output()
		if err != nil {
			return false, fmt.Errorf("Naming request failed; inspect the agent before retrying: %w", err)
		}
		var receipt struct {
			Submitted bool `json:"submitted"`
		}
		if err := json.Unmarshal(out, &receipt); err != nil {
			return false, errors.New("Invalid send receipt; inspect the agent before retrying.")
		}
		return receipt.Submitted, nil
	}
	pause := func() error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
			return nil
		}
	}
	name, err := watchRequestAgentName(s.Tool, token, validate, read, send, pause)
	if err != nil {
		return "", err
	}
	// Persist as an unapproved suggestion, never as the accepted display name.
	record := watchReadNames(review.Dir)[watchNameKey(s)]
	record.Suggestion = name
	if err := watchSaveName(review.Dir, s, record); err != nil {
		return "", err
	}
	return name, nil
}
