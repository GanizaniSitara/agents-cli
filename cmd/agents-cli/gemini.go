package main

import "time"

// Google Gemini CLI and agy use different composers, but both accept bracketed
// paste. Keep Google's existing submission checks; change its text transport.
type geminiConsole struct {
	paste func(uint32, string) error
	key   func(uint32, string, string) error
	sleep func(time.Duration)
}

var liveGeminiConsole = geminiConsole{
	paste: liveCopilotConsole.paste, key: keyFor, sleep: time.Sleep,
}

func (c geminiConsole) inject(pid uint32, text string, enter bool) error {
	if err := c.paste(pid, text); err != nil {
		return err
	}
	if !enter {
		return nil
	}
	// Separate Enter from the paste burst. Bound the wait outside the helper's
	// lifetime, as for Copilot; never grow past the helper's 15-second timeout.
	delay := 800*time.Millisecond + time.Duration(len([]rune(text))/200)*100*time.Millisecond
	if delay > 4*time.Second {
		delay = 4 * time.Second
	}
	c.sleep(delay)
	return c.key(pid, "gemini", "enter")
}
