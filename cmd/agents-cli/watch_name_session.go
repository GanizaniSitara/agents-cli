package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const watchRenameBusy = "Rename queued until the session is idle."

// A draft in the composer is never overwritten; the dialog queues and retries
// until the owner sends or clears it.
const watchRenameDraft = "Rename queued until the prompt is empty."

func watchHarnessRenameSupported(tool string) bool {
	switch tool {
	case "agy", "claude", "copilot", "codex":
		return true
	}
	return false
}

func watchRenameComposerEmpty(tool string, lines []string) bool {
	if guessState(tool, lines) != "idle" {
		return false
	}
	if tool == "claude" {
		return framedDraftState(tool, lines) == "idle"
	}
	if tool == "agy" || tool == "copilot" {
		draft, ok := copilotComposer(lines)
		return ok && draft == ""
	}
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if strings.HasPrefix(line, "›") {
			return line == "›" || line == "› Ask Codex to do anything"
		}
	}
	return false
}

func watchHarnessRenameConfirmed(tool, name string, lines []string) bool {
	if tool == "codex" {
		tail := trimBlankTail(lines)
		if len(tail) == 0 {
			return false
		}
		parts := strings.Split(tail[len(tail)-1], " · ")
		for i := 1; i < len(parts); i++ {
			if !strings.Contains(strings.ToLower(parts[i-1]), "context") || !strings.Contains(parts[i-1], "% left") {
				continue
			}
			part := strings.TrimSpace(parts[i])
			if !strings.HasPrefix(part, name) {
				continue
			}
			rest := strings.TrimSpace(strings.TrimPrefix(part, name))
			return rest == "" || strings.HasPrefix(rest, "⚠")
		}
		return false
	}
	// The live composer is not transcript: Copilot's empty prompt shows a
	// "❯ Type @ to mention files…" placeholder that would read as a new command.
	if _, bottom, ok := copilotComposerEnd(lines); ok {
		for top := bottom - 1; top >= 0; top-- {
			if t := strings.TrimSpace(lines[top]); len([]rune(t)) >= 3 && strings.Trim(t, "─━") == "" {
				lines = lines[:top]
				break
			}
		}
	}
	command := ""
	confirmed := false
	for _, line := range lines {
		text := strings.TrimSpace(line)
		for _, marker := range []string{">", "❯", "›"} {
			if strings.HasPrefix(text, marker) && strings.TrimSpace(strings.TrimPrefix(text, marker)) != "" {
				command = strings.TrimSpace(strings.TrimPrefix(text, marker))
				confirmed = false
			}
		}
		if command != "/rename" && command != "/rename "+name {
			continue
		}
		line = strings.Trim(strings.TrimSpace(line), "│ ║●•⎿✓✔ ")
		for _, prefix := range []string{"conversation renamed to", "session renamed to", "session renamed", "renamed session to"} {
			if !strings.HasPrefix(strings.ToLower(line), prefix) {
				continue
			}
			value := strings.TrimSpace(line[len(prefix):])
			value = strings.TrimSpace(strings.TrimPrefix(value, ":"))
			if strings.Trim(value, `"`) == name {
				confirmed = true
			}
		}
	}
	return confirmed
}

// Confirmation is distinct from send submission. An echoed command or cleared
// composer never proves the harness saved the session name.
func watchRenameHarness(tool, name string, validate func() error, read func() (*peekResult, error), send func(string) (bool, error), pause func() error) (string, error) {
	if !watchHarnessRenameSupported(tool) || !watchValidName(name) {
		return "", errors.New("Session rename is unavailable or name is invalid.")
	}
	if err := validate(); err != nil {
		return "", err
	}
	before, err := read()
	if err != nil {
		return "", err
	}
	if !(tool == "claude" && claudeRenameReady(before)) && !watchRenameComposerEmpty(tool, before.Lines) {
		if guessState(tool, before.Lines) == "busy" {
			return "", errors.New(watchRenameBusy)
		}
		return "", errors.New(watchRenameDraft)
	}
	if err := validate(); err != nil {
		return "", err
	}
	submitted, err := send("/rename " + name)
	if err != nil {
		return "", err
	}
	if !submitted {
		return "", errors.New("Session rename submission is uncertain. Inspect the session before retrying; nothing was resent.")
	}
	var last *peekResult
	for {
		if err := pause(); err != nil {
			// The harness retitling its own window to exactly the new name is
			// confirmation too; leaving the dialog up after that just looks broken.
			if last != nil && last.Title == name && before.Title != name {
				return last.Title, nil
			}
			return "", errors.New("Session rename was not confirmed. Inspect the session before retrying; window title was not changed by agents-cli.")
		}
		if err := validate(); err != nil {
			return "", err
		}
		after, err := read()
		if err != nil {
			return "", err
		}
		last = after
		if watchHarnessRenameConfirmed(tool, name, after.Lines) {
			if after.Title != before.Title && after.Title != name {
				return "", errors.New("Session renamed, but the window title changed independently. Reopen Rename to review it.")
			}
			return after.Title, nil
		}
	}
}

func watchApplyReviewedName(review watchNameReview) error {
	if review.Action != "accept" {
		return errors.New("Rename requires explicit confirmation.")
	}
	s := review.Sample
	if !review.RenameSession {
		return watchDoRename(s, review.Name)
	}
	if !watchHarnessRenameSupported(s.Tool) {
		return errors.New("This harness supports window/tab title rename only.")
	}
	ctx, cancel := context.WithTimeout(operationContext, 20*time.Second)
	defer cancel()
	validate := func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if problem := watchTargetProblem(s, watchReadTarget(s)); problem != "" {
			return errors.New(problem)
		}
		if s.NamingAgentStarted == "" || startTime(s.AgentPID).Format(time.RFC3339Nano) != s.NamingAgentStarted {
			return errors.New("Agent process changed. Reopen Rename.")
		}
		if s.Tool == "claude" && review.Conversation != "" {
			_, record, err := claudeSessionRecord(s)
			if err != nil || record.SessionID != review.Conversation {
				return errors.New("Claude conversation changed. Reopen Rename.")
			}
		}
		return nil
	}
	if err := validate(); err != nil {
		return err
	}
	live, procs, err := lookup(strconv.FormatUint(uint64(s.PID), 10))
	if err != nil {
		return err
	}
	if live.AgentPID != s.AgentPID || live.Tool != s.Tool {
		return errors.New("Agent changed. Reopen Rename.")
	}
	if err := requirePolling(live); err != nil {
		return err
	}
	if agentRuntimeWork(*live, procs, commandLine, startTime) > 0 {
		return errors.New(watchRenameBusy)
	}
	read := func() (*peekResult, error) { return peek(s.PID, 0) }
	before, err := read()
	if err != nil {
		return err
	}
	if before.Title != s.Title {
		return errors.New("Title changed. Reopen Rename.")
	}
	state := guessState(s.Tool, before.Lines)
	if state == "busy" {
		return errors.New(watchRenameBusy)
	}
	send := func(text string) (bool, error) {
		if s.Tool == "claude" {
			if err := inject(s.PID, text, false); err != nil {
				return false, err
			}
			for attempt := 0; attempt < 8; attempt++ {
				time.Sleep(100 * time.Millisecond)
				if err := validate(); err != nil {
					return false, err
				}
				r, err := read()
				if err != nil {
					return false, err
				}
				draft, _, ok := claudeRenameFrame(r.Lines)
				if ok && draft == text && claudeState(r.Lines) == "idle" {
					return true, sendKey(s.PID, "enter")
				}
			}
			return false, errors.New("Claude input differed from the rename command. Inspect the composer; Enter was not sent.")
		}
		exe, err := os.Executable()
		if err != nil {
			return false, err
		}
		cmd := helperCommand(ctx, exe, text)
		cmd.Args = []string{exe, "send", strconv.FormatUint(uint64(s.PID), 10), "--stdin", "--via", "inject"}
		cmd.WaitDelay = 250 * time.Millisecond
		out, err := cmd.Output()
		if err != nil {
			return false, fmt.Errorf("Session rename failed; inspect it before retrying: %w", err)
		}
		var receipt struct {
			Submitted bool `json:"submitted"`
		}
		if err := json.Unmarshal(out, &receipt); err != nil {
			return false, errors.New("Invalid session rename receipt. Inspect it before retrying.")
		}
		return receipt.Submitted, nil
	}
	deadline := time.Now().Add(8 * time.Second)
	pause := func() error {
		if time.Now().After(deadline) {
			return context.DeadlineExceeded
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(250 * time.Millisecond):
			return nil
		}
	}
	title, err := watchRenameHarness(s.Tool, review.Name, validate, read, send, pause)
	if err != nil {
		return err
	}
	s.Title = title // The harness itself may already have changed its title.
	if err := watchDoRename(s, review.Name); err != nil {
		return fmt.Errorf("Session renamed; window/tab rename failed: %w", err)
	}
	return nil
}

// Only the live framed prompt can supply a ghost-text candidate. A real
// draft with the caret at Home will also pass this first check, so the
// sender must verify the entire staged command before pressing Enter.
func claudeRenameFrame(lines []string) (string, int, bool) {
	normalized := append([]string(nil), lines...)
	for i, line := range normalized {
		text := strings.TrimSpace(line)
		if strings.HasPrefix(text, "───") && strings.HasSuffix(text, "─") {
			normalized[i] = "────────"
		}
	}
	draft, bottom, ok := copilotComposerEnd(normalized)
	if !ok {
		return "", 0, false
	}
	for i := bottom - 1; i >= 0; i-- {
		if normalized[i] == "────────" {
			return draft, i + 2, true
		}
	}
	return "", 0, false
}
func claudeRenameReady(r *peekResult) bool {
	draft, row, ok := claudeRenameFrame(r.Lines)
	return ok && claudeState(r.Lines) == "idle" && (draft == "" || row == r.ComposerStartRow)
}

func watchRenameStillBusy(r watchNameReview) bool {
	b, err := os.ReadFile(filepath.Join(r.Dir, "status.json"))
	if err != nil {
		return false
	}
	var receipt watchReceipt
	if json.Unmarshal(b, &receipt) != nil || time.Since(receipt.Updated) > 10*time.Second {
		return false
	}
	for _, e := range receipt.Sessions {
		if watchSameClickSession(e.Sample, r.Sample) {
			return e.Sample.State == "busy" || e.Sample.WorkProcesses > 0
		}
	}
	return false
}
