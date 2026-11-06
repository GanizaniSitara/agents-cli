package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type watchTheme struct {
	Waiting           string  `json:"waiting"`
	Finished          string  `json:"finished"`
	Error             string  `json:"error"`
	Working           string  `json:"working"`
	Metadata          string  `json:"metadata"`
	Background        string  `json:"background"`
	FadeSeconds       int     `json:"fade_seconds"`
	MinimumBrightness float64 `json:"minimum_brightness"`
}

func defaultWatchTheme() watchTheme {
	return watchTheme{Waiting: "#E3AD55", Finished: "#88C58E", Error: "#ED7373", Working: "#81878F", Metadata: "#ADB3BB", Background: "#20252B", FadeSeconds: 600, MinimumBrightness: 0.60}
}

func loadWatchTheme(dir string) watchTheme {
	theme := defaultWatchTheme()
	path := filepath.Join(dir, "theme.json")
	if b, err := os.ReadFile(path); err == nil {
		var candidate watchTheme = theme
		if json.Unmarshal(b, &candidate) == nil {
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

func (t watchTheme) colour(hex string, age time.Duration) uintptr {
	rgb := watchRGB(hex, 0xADB3BB)
	bg := watchRGB(t.Background, 0x20252B)
	minimum := t.MinimumBrightness
	if minimum < 0.35 || minimum > 1 {
		minimum = 0.60
	}
	fraction := 0.0
	if t.FadeSeconds > 0 {
		fraction = age.Seconds() / float64(t.FadeSeconds)
		if fraction > 1 {
			fraction = 1
		}
		if fraction < 0 {
			fraction = 0
		}
	}
	strength := 1 - fraction*(1-minimum)
	var result uint32
	for _, shift := range []uint{0, 8, 16} {
		value := float64((rgb >> shift) & 255)
		base := float64((bg >> shift) & 255)
		result |= uint32(base+(value-base)*strength) << shift
	}
	// GDI COLORREF is BGR, while the editable palette uses ordinary #RRGGBB.
	return uintptr((result&255)<<16 | result&0xff00 | (result>>16)&255)
}

func (t watchTheme) rowColour(phase string, age time.Duration, disabled, trustIdle bool) uintptr {
	hex := t.Waiting
	if disabled {
		hex = t.Metadata
	} else {
		switch phase {
		case "working":
			hex = t.Working
		case "finished":
			hex = t.Finished
		case "error":
			hex = t.Error
		case "idle":
			if trustIdle {
				hex = t.Finished
			}
		}
	}
	return t.colour(hex, age)
}
