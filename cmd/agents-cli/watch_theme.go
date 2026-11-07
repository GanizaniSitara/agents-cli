package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type watchTheme struct {
	Idle       string `json:"idle"`
	Waiting    string `json:"waiting"`
	Finished   string `json:"finished"`
	Error      string `json:"error"`
	Working    string `json:"working"`
	Metadata   string `json:"metadata"`
	Background string `json:"background"`
}

func defaultWatchTheme() watchTheme {
	return watchTheme{Idle: "#54B7FF", Waiting: "#FFC247", Finished: "#88C58E", Error: "#ED7373", Working: "#D4D9DE", Metadata: "#9299A1", Background: "#20252B"}
}

func loadWatchTheme(dir string) watchTheme {
	theme := defaultWatchTheme()
	path := filepath.Join(dir, "theme.json")
	if b, err := os.ReadFile(path); err == nil {
		var candidate watchTheme = theme
		if json.Unmarshal(b, &candidate) == nil {
			// Upgrade the former default shades; preserve custom palette values.
			if candidate.Waiting == "#E3AD55" {
				candidate.Waiting = theme.Waiting
			}
			if candidate.Working == "#81878F" {
				candidate.Working = theme.Working
			}
			if candidate.Metadata == "#ADB3BB" {
				candidate.Metadata = theme.Metadata
			}
			theme = candidate
		}
	} else if os.IsNotExist(err) {
		b, _ := json.MarshalIndent(theme, "", "  ")
		_ = os.WriteFile(path, b, 0600)
	}
	return theme
}

func watchRGB(hex string, fallback uint32) uint32 {
	if len(hex) != 7 || !strings.HasPrefix(hex, "#") {
		return fallback
	}
	n, err := strconv.ParseUint(hex[1:], 16, 24)
	if err != nil {
		return fallback
	}
	return uint32(n)
}

func (t watchTheme) colour(hex string) uintptr {
	result := watchRGB(hex, 0x9299A1)
	// GDI COLORREF is BGR, while the editable palette uses ordinary #RRGGBB.
	return uintptr((result&255)<<16 | result&0xff00 | (result>>16)&255)
}

func (t watchTheme) rowColour(phase string) uintptr {
	hex := t.Metadata
	switch phase {
	case "idle", "unknown":
		hex = t.Idle
	case "waiting":
		hex = t.Waiting
	case "working":
		hex = t.Working
	case "finished":
		hex = t.Finished
	case "error":
		hex = t.Error
	}
	return t.colour(hex)
}

func watchProminentStatus(phase string) string {
	switch phase {
	case "waiting":
		return "NEEDS INPUT"
	case "idle":
		return "IDLE"
	case "unknown":
		return "UNKNOWN"
	case "finished":
		return "FINISHED"
	case "error":
		return "ERROR"
	}
	return ""
}
