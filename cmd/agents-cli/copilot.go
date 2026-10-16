package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/windows"
)

// Copilot keeps its live status below the composer. Transcript text (including
// echoed prompts and old permission questions) is not a status indicator.
func copilotState(lines []string) string {
	tail := trimBlankTail(lines)
	if len(tail) == 0 {
		return "unknown"
	}
	footer := strings.ToLower(strings.TrimSpace(tail[len(tail)-1]))
	if copilotMenu(tail) {
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
		draft := []string{strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(first, "❯"), ">"))}
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

// Bracketed paste prevents embedded newlines and slash-command completion from
// being interpreted midway through delivery. Enter is a separate named key.
func copilotPasteRecords(text string) []inputRecord {
	text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
	var records []inputRecord
	for _, r := range "\x1b[200~" + text + "\x1b[201~" {
		for _, u := range utf16Units(r) {
			records = append(records, keyPair(0, u, 0)...)
		}
	}
	return records
}

func doCopilotPaste(pid uint32, text string) error {
	if err := attach(pid); err != nil {
		return err
	}
	h, err := openConsole("CONIN$")
	if err != nil {
		return err
	}
	defer windows.CloseHandle(h)
	return writeInput(h, copilotPasteRecords(text))
}

type copilotConsole struct {
	paste func(uint32, string) error
	peek  func(uint32, int) (*peekResult, error)
	key   func(uint32, string, string) error
	sleep func(time.Duration)
	now   func() time.Time
}

var liveCopilotConsole = copilotConsole{
	paste: func(pid uint32, text string) error {
		out, err := runHelper("copilot-paste", strconv.FormatUint(uint64(pid), 10), text)
		if err != nil {
			return err
		}
		return helperErr(out)
	},
	peek: peek, key: keyFor, sleep: time.Sleep, now: time.Now,
}

func (c copilotConsole) send(s *Session, text string, enter bool) (bool, error) {
	if !s.PollingMuted {
		r, err := c.peek(s.PID, 0)
		if err != nil {
			return false, err
		}
		if copilotMenu(r.Lines) || copilotState(r.Lines) == "busy" {
			return false, fmt.Errorf("Copilot is busy or showing a menu; inspect tail and use wait or key before sending")
		}
	}
	if err := c.paste(s.PID, text); err != nil {
		return false, err
	}
	if !enter {
		return false, nil
	}
	// Allow paste processing to finish, including long-text redraws. Bound the
	// delay independently of the detached helper's 15-second lifetime.
	delay := 800*time.Millisecond + time.Duration(len([]rune(text))/200)*100*time.Millisecond
	if delay > 4*time.Second {
		delay = 4 * time.Second
	}
	c.sleep(delay)
	if !s.PollingMuted {
		deadline := c.now().Add(5 * time.Second)
		last := ""
		stable := 0
		for {
			r, err := c.peek(s.PID, 0)
			if err != nil {
				return false, err
			}
			draft, ok := copilotComposer(r.Lines)
			if ok && draft != "" && draft == last {
				stable++
			} else {
				stable = 0
			}
			if stable >= 2 {
				break
			}
			if !c.now().Before(deadline) {
				return false, fmt.Errorf("Copilot input did not settle; text may remain in the composer; inspect tail before retrying")
			}
			last = draft
			c.sleep(150 * time.Millisecond)
		}
	}
	if err := c.key(s.PID, "copilot", "enter"); err != nil {
		return false, err
	}
	if s.PollingMuted {
		return false, nil
	} // delivered, but cannot verify by polling
	deadline := c.now().Add(3 * time.Second)
	for {
		c.sleep(150 * time.Millisecond)
		r, err := c.peek(s.PID, 0)
		if err != nil {
			return false, err
		}
		state := copilotState(r.Lines)
		draft, ok := copilotComposer(r.Lines)
		if (ok && draft == "" && (state == "busy" || state == "idle")) || copilotMenu(r.Lines) {
			return true, nil
		}
		if !c.now().Before(deadline) {
			return false, nil
		}
		// Never blindly retry Enter: it could approve a permission or select a
		// model after the first Enter already opened a dialog.
	}
}

func copilotMenu(lines []string) bool {
	tail := trimBlankTail(lines)
	if len(tail) == 0 {
		return false
	}
	footer := strings.ToLower(tail[len(tail)-1])
	return strings.Contains(footer, "enter") && (strings.Contains(footer, "select") || strings.Contains(footer, "confirm"))
}
