package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const watchRenameBusy = "Rename queued until the session is idle."

// A draft in the composer is never overwritten; the dialog queues and retries
// until the owner sends or clears it.
const watchRenameDraft = "Rename queued until the prompt is empty."

var codexFooterWarnings = regexp.MustCompile(`^\s+⚠\s+\d+\s+warnings?(?:\s+·\s+f2 to view)?$`)

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
			if line == "› Ask Codex to do anything" {
				return true
			}
			if line != "›" {
				return false
			}
			// A multiline draft can start on the row below an empty marker.
			for _, next := range lines[i+1:] {
				if row := strings.TrimSpace(next); row != "" {
					return strings.HasPrefix(strings.ToLower(row), "gpt-") || strings.Contains(row, "? for shortcuts")
				}
			}
			return true
		}
	}
	return false
}

func watchHarnessRenameConfirmed(tool, name string, lines []string) bool {
	if tool == "codex" {
		tail := trimBlankTail(lines)
		// Current Codex adds a shortcuts/warnings row below the model row.
		// Never search above the live composer for a matching old footer.
		for i := len(tail) - 1; i >= 0 && i >= len(tail)-3; i-- {
			row := strings.TrimSpace(tail[i])
			if strings.HasPrefix(row, "›") {
				break
			}
			if !strings.HasPrefix(strings.ToLower(row), "gpt-") {
				continue
			}
			for field, rest, ok := strings.Cut(row, " · "); ok; field, rest, ok = strings.Cut(rest, " · ") {
				if contextRemaining.FindString(field) != field || field == "" {
					continue
				}
				suffix, match := strings.CutPrefix(strings.TrimSpace(rest), name)
				return match && (suffix == "" || codexFooterWarnings.MatchString(suffix))
			}
			return false
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
	for {
		if err := pause(); err != nil {
			if !submitted {
				return "", errRenameUncertain
			}
			return "", errRenameUnconfirmed
		}
		if err := validate(); err != nil {
			return "", err
		}
		after, err := read()
		if err != nil {
			return "", err
		}
		if after.Title == name && before.Title != name {
			return name, nil
		}
		if watchHarnessRenameConfirmed(tool, name, after.Lines) && (submitted || !watchHarnessRenameConfirmed(tool, name, before.Lines)) {
			if !watchRenameTitleExpected(tool, before.Title, after.Title, name) {
				return "", errors.New("Session renamed, but the window title changed independently. Reopen Rename to review it.")
			}
			return after.Title, nil
		}
	}
}

var errRenameUnconfirmed = errors.New("Session rename was not confirmed. Inspect the session before retrying; window title was not changed by agents-cli.")
var errRenameUncertain = errors.New("Session rename submission is uncertain. Inspect the session before retrying; nothing was resent.")

// Codex shortens its own Windows title separately from the footer. A clipped
// prefix is an expected title transition, never proof of the saved name.
func watchRenameTitleExpected(tool, before, after, name string) bool {
	if before == after || after == name {
		return true
	}
	if tool == "codex" {
		for _, end := range []string{"...", "…"} {
			if prefix, ok := strings.CutSuffix(after, end); ok && prefix != "" && strings.HasPrefix(name, prefix) {
				return true
			}
		}
	}
	return false
}

// The current TUI prints /status and its native identity panel into history.
// Compare the history through the command so a changing footer cannot refresh
// a stale panel. The thread's latest index entry preserves even wrapped names.
func watchCodexStatus(lines []string) (history, id string) {
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) != "/status" {
			continue
		}
		for j := i + 1; j < len(lines); j++ {
			row := strings.TrimSpace(lines[j])
			if row == "" {
				continue
			}
			if strings.HasPrefix(row, ">_ OpenAI Codex") {
				return strings.Join(lines[:i+1], "\n"), shelfStatusID(lines[j:])
			}
			break
		}
		break
	}
	return "", ""
}

func watchCodexSavedName(home, id, name string) bool {
	if home == "" || id == "" {
		return false
	}
	rows := bytes.Split(subscriptionFileTail(filepath.Join(home, "session_index.jsonl"), 512<<10), []byte{'\n'})
	for i := len(rows) - 1; i >= 0; i-- {
		var entry struct {
			ID   string
			Name string `json:"thread_name"`
		}
		if json.Unmarshal(rows[i], &entry) == nil && entry.ID == id {
			return entry.Name == name
		}
	}
	return false
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
		cmd.Args = []string{exe, "send", strconv.FormatUint(uint64(s.PID), 10), "--stdin", "--via", "inject", "--expect-started", live.Started}
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
	confirmationWait := 8 * time.Second
	if s.Tool == "codex" {
		confirmationWait = 2 * time.Second
	}
	var deadline time.Time
	pause := func() error {
		if deadline.IsZero() {
			deadline = time.Now().Add(confirmationWait)
		}
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
	if s.Tool == "codex" && (errors.Is(err, errRenameUnconfirmed) || errors.Is(err, errRenameUncertain)) {
		// A narrow footer and a pinned or abbreviated window title can hide
		// the full name. Ask this same idle console for its current thread.
		probe := func() (*peekResult, error) { return peek(s.PID, 80) }
		screen, e := probe()
		if e != nil {
			return e
		}
		if !watchRenameComposerEmpty(s.Tool, screen.Lines) || !watchRenameTitleExpected(s.Tool, before.Title, screen.Title, review.Name) {
			return err
		}
		prior, _ := watchCodexStatus(screen.Lines)
		if e = validate(); e != nil {
			return e
		}
		if _, e = send("/status"); e != nil {
			return e
		}
		deadline = time.Now().Add(3 * time.Second)
		for pause() == nil {
			if e = validate(); e != nil {
				return e
			}
			screen, e = probe()
			if e != nil {
				return e
			}
			history, id := watchCodexStatus(screen.Lines)
			if history != prior && watchCodexSavedName(live.Home, id, review.Name) && watchRenameTitleExpected(s.Tool, before.Title, screen.Title, review.Name) {
				title, err = screen.Title, nil
				break
			}
		}
	}
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
