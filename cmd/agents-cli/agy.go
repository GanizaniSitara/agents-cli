package main

import (
	"fmt"
	"strings"
	"time"
)

// agy needs bracketed paste: plain character events trigger a redraw for every
// character, even after its console input queue has already drained. Reuse the
// existing stdin-only bracketed-paste helper and framed composer parser.
type agyConsole copilotConsole

var liveAGYConsole = agyConsole(liveCopilotConsole)

func agyBusy(lines []string) bool {
	tail := trimBlankTail(lines)
	return len(tail) > 0 && strings.Contains(strings.ToLower(tail[len(tail)-1]), "esc to cancel")
}

// force skips only the empty-composer check; busy and menu screens still refuse.
func (c agyConsole) send(s *Session, text string, enter, force bool) (bool, error) {
	if !s.PollingMuted {
		r, err := c.peek(s.PID, 0)
		if err != nil {
			return false, err
		}
		if agyBusy(r.Lines) || copilotMenu(r.Lines) {
			return false, fmt.Errorf("agy must have an empty idle composer before sending; inspect tail")
		}
		draft, ok := copilotComposer(r.Lines)
		if !force && (!ok || draft != "") {
			return false, fmt.Errorf("agy must have an empty idle composer before sending; inspect tail")
		}
	}
	if err := c.paste(s.PID, text); err != nil {
		return false, err
	}
	if !enter {
		return false, nil
	}
	// A write only acknowledges enqueueing, not application processing. Wait for
	// a nonempty stable draft before sending Enter, outside the paste burst.
	c.sleep(150 * time.Millisecond)
	if !s.PollingMuted {
		deadline := c.now().Add(5 * time.Second)
		last := ""
		stable := 0
		for {
			r, err := c.peek(s.PID, 0)
			if err != nil {
				return false, err
			}
			if agyBusy(r.Lines) || copilotMenu(r.Lines) {
				return false, fmt.Errorf("agy changed state during paste; inspect tail before submitting")
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
				return false, fmt.Errorf("agy paste did not settle; text may remain in the composer; inspect tail")
			}
			last = draft
			c.sleep(150 * time.Millisecond)
		}
	}
	if err := c.key(s.PID, s.Tool, "enter"); err != nil {
		return false, err
	}
	if s.PollingMuted {
		return false, nil
	}
	deadline := c.now().Add(3 * time.Second)
	for {
		c.sleep(150 * time.Millisecond)
		r, err := c.peek(s.PID, 0)
		if err != nil {
			return false, err
		}
		draft, ok := copilotComposer(r.Lines)
		if ok && draft == "" || copilotMenu(r.Lines) {
			return true, nil
		}
		if !c.now().Before(deadline) {
			return false, nil
		}
		// An uncertain result never causes a second Enter: the first could have
		// opened an approval dialog, or queued a turn while output is streaming.
	}
}
