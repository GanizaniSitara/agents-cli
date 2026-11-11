package main

// Polling must not restart a flash that Windows stopped when read. Start only
// on a new stop or when entering a desktop with stopped sessions.
type watchFlashController struct {
	desktop string
	ready   bool
	applied map[uintptr]bool
}

func (c *watchFlashController) update(entries map[uint32]*watchEntry, current string, flash func(uintptr, bool)) {
	c.apply(c.states(entries, current), flash)
}

func (c *watchFlashController) states(entries map[uint32]*watchEntry, current string) map[uintptr]bool {
	changed := c.ready && current != c.desktop
	c.desktop, c.ready = current, true
	states := map[uintptr]bool{}
	for _, entry := range entries {
		s := entry.Sample
		if s.HWND == 0 {
			continue
		}
		onDesktop := current != "" && s.DesktopID == current
		if changed && onDesktop && entry.Stopped && !entry.Dismissed && !entry.Parked {
			entry.Attention = true
		}
		waiting := s.State == "prompt" || s.Phase == "waiting" || s.Phase == "question" || s.Phase == "review"
		on := onDesktop && waiting && entry.Stopped && !entry.Dismissed && !entry.Parked && entry.Attention
		states[s.HWND] = states[s.HWND] || on
	}
	return states
}

func (c *watchFlashController) apply(states map[uintptr]bool, flash func(uintptr, bool)) {
	if c.applied == nil {
		c.applied = map[uintptr]bool{}
	}
	for hwnd, on := range states {
		previous, known := c.applied[hwnd]
		if !known || previous != on {
			// Initial false clears flags left by an older watcher as well.
			flash(hwnd, on)
			c.applied[hwnd] = on
		}
	}
	for hwnd, on := range c.applied {
		if _, alive := states[hwnd]; !alive {
			if on {
				flash(hwnd, false)
			}
			delete(c.applied, hwnd)
		}
	}
}

// Snapshot tracker state under the lock, then call Windows without holding it.
// A stuck Explorer/console call must not block reader progress or status saves.
// Both signal controllers belong exclusively to the panel thread.
func (a *watchApp) panelSignals(current string, taskbar *watchTaskbarController, flash func(uintptr, bool), set func(uintptr, uint32) bool) []watchEntry {
	a.ui.step("timer.tracker-lock")
	a.mu.Lock()
	flashes := a.flashes.states(a.tracker.entries, current)
	states := watchTaskbarStates(a.tracker.entries, current)
	rows := a.tracker.panelRows()
	a.mu.Unlock()
	a.ui.step("timer.flash")
	a.flashes.apply(flashes, flash)
	a.ui.step("timer.taskbar")
	taskbar.apply(states, set)
	return rows
}
