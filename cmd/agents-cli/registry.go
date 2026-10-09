package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// ---- launch-info: the launcher's own record of task sessions ----------------

type launchInfo struct {
	Tool                string
	WindowTitle         string
	ProcessStartTimeUtc string
	SessionId           string
	TaskSlug            string
	PID                 json.Number
	Zone                *int
	ThreadId            string
}

func launchMatches(li launchInfo, start time.Time) bool {
	if start.IsZero() || li.ProcessStartTimeUtc == "" {
		return true
	}
	recorded, err := time.Parse(time.RFC3339Nano, li.ProcessStartTimeUtc)
	return err == nil && recorded.Truncate(time.Second).Equal(start.Truncate(time.Second))
}

func launchInfoDir() string {
	if d := os.Getenv("AGENTS_CLI_TASKS_DIR"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "tasks", "in-progress")
}

func readLaunchInfos() map[uint32]launchInfo {
	out := map[uint32]launchInfo{}
	entries, err := filepath.Glob(filepath.Join(launchInfoDir(), "*.launch-info.json"))
	if err != nil {
		return out
	}
	for _, path := range entries {
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		raw = bytes.TrimPrefix(raw, []byte{0xEF, 0xBB, 0xBF}) // PowerShell writes a BOM
		var li launchInfo
		if err := json.Unmarshal(raw, &li); err != nil {
			continue
		}
		pid, err := strconv.ParseUint(li.PID.String(), 10, 32)
		if err != nil || pid == 0 {
			continue
		}
		out[uint32(pid)] = li
	}
	return out
}

// ---- adopted sessions and labels -------------------------------------------

type adopted struct {
	PollingMuted bool   `json:"polling_muted,omitempty"`
	Tool         string `json:"tool,omitempty"`
	Name         string `json:"name,omitempty"`
	Start        string `json:"start,omitempty"` // process start time, guards PID reuse
	Added        string `json:"added"`
}

func dataDir() string {
	base := os.Getenv("APPDATA")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = home
	}
	return filepath.Join(base, "agents-cli")
}

func adoptedPath() string { return filepath.Join(dataDir(), "sessions.json") }

func readAdopted() map[string]adopted {
	out := map[string]adopted{}
	raw, err := os.ReadFile(adoptedPath())
	if err != nil {
		return out
	}
	_ = json.Unmarshal(raw, &out)
	return out
}

func writeAdopted(m map[string]adopted) error {
	if err := os.MkdirAll(dataDir(), 0o755); err != nil {
		return err
	}
	raw, _ := json.MarshalIndent(m, "", "  ")
	return os.WriteFile(adoptedPath(), raw, 0o644)
}

// adoptedFor returns the entry for a pid if it exists and still refers to the
// same process (start time matches when one was recorded).
func adoptedFor(m map[string]adopted, pid uint32, start time.Time) (adopted, bool) {
	a, ok := m[strconv.FormatUint(uint64(pid), 10)]
	if !ok {
		return adopted{}, false
	}
	if a.Start != "" && !start.IsZero() && !strings.EqualFold(a.Start, start.Format(time.RFC3339)) {
		return adopted{}, false
	}
	return a, true
}
