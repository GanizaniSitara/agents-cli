package main

// Polling must not restart a flash that Windows stopped when read. Start only
// on a new stop or when entering a desktop with stopped sessions.
type watchFlashController struct {
	desktop string
	ready   bool
	applied map[uintptr]bool
}

func (c *watchFlashController) update(entries map[uint32]*watchEntry, current string, flash func(uintptr, bool)) {
	if c.applied == nil {
		c.applied = map[uintptr]bool{}
	}
	changed := c.ready && current != c.desktop
	c.desktop, c.ready = current, true
	alive := map[uintptr]bool{}
	for _, entry := range entries {
		s := entry.Sample
		if s.HWND == 0 {
			continue
		}
		alive[s.HWND] = true
		onDesktop := current != "" && s.DesktopID == current
		if changed && onDesktop && entry.Stopped && !entry.Dismissed {
			entry.Attention = true
		}
		waiting := s.State == "prompt" || s.Phase == "waiting" || s.Phase == "question"
		on := onDesktop && waiting && entry.Stopped && !entry.Dismissed && entry.Attention
		previous, known := c.applied[s.HWND]
		if !known || previous != on {
			// Initial false clears flags left by an older watcher as well.
			flash(s.HWND, on)
			c.applied[s.HWND] = on
		}
	}
	for hwnd, on := range c.applied {
		if !alive[hwnd] {
			if on {
				flash(hwnd, false)
			}
			delete(c.applied, hwnd)
		}
	}
}
