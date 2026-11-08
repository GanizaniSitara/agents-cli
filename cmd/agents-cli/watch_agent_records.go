package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Read local lifecycle markers only. Never copy transcript content into watcher state.
type watchAgentRecords struct {
	codex map[string]*watchCodexIndex
}

type watchCodexIndex struct {
	checked  time.Time
	parents  map[string]string
	children map[string][]string
}

func (r *watchAgentRecords) counts(s Session) (watchAgents, bool) {
	if s.Tool == "claude" {
		home, err := os.UserHomeDir()
		if err != nil {
			return watchAgents{}, false
		}
		base := filepath.Join(home, ".claude")
		var identity struct {
			SessionID string `json:"sessionId"`
			ProcStart string `json:"procStart"`
		}
		b, err := os.ReadFile(filepath.Join(base, "sessions", strconv.FormatUint(uint64(s.AgentPID), 10)+".json"))
		if err != nil || json.Unmarshal(b, &identity) != nil || !watchRecordID(identity.SessionID) {
			return watchAgents{}, false
		}
		started := startTime(s.AgentPID)
		if started.IsZero() || identity.ProcStart != strconv.FormatInt(started.UnixNano()/100+116444736000000000, 10) {
			return watchAgents{}, false
		}
		roots, _ := filepath.Glob(filepath.Join(base, "projects", "*", identity.SessionID+".jsonl"))
		if len(roots) != 1 {
			return watchAgents{}, false
		}
		paths, err := filepath.Glob(filepath.Join(strings.TrimSuffix(roots[0], ".jsonl"), "subagents", "agent-*.jsonl"))
		if err != nil {
			return watchAgents{}, false
		}
		return watchCountAgentRecords(paths, "claude", started), true
	}
	if s.Tool != "codex" || s.Home == "" {
		return watchAgents{}, false
	}
	id := firstNonEmpty(s.ThreadID, s.SessionID)
	if !watchRecordID(id) {
		return watchAgents{}, false
	}
	if r.codex == nil {
		r.codex = map[string]*watchCodexIndex{}
	}
	index := r.codex[s.Home]
	if index == nil {
		index = &watchCodexIndex{parents: map[string]string{}, children: map[string][]string{}}
		r.codex[s.Home] = index
	}
	if time.Since(index.checked) >= 10*time.Second {
		root := filepath.Join(s.Home, "sessions")
		err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
				return nil
			}
			if _, known := index.parents[path]; known {
				return nil
			}
			f, err := os.Open(path)
			if err != nil {
				return nil
			}
			scan := bufio.NewScanner(io.LimitReader(f, 1<<20))
			scan.Buffer(make([]byte, 4096), 1<<20)
			var meta struct {
				Type    string `json:"type"`
				Payload struct {
					ID     string          `json:"id"`
					Source json.RawMessage `json:"source"`
				} `json:"payload"`
			}
			ok := scan.Scan() && json.Unmarshal(scan.Bytes(), &meta) == nil && meta.Type == "session_meta"
			f.Close()
			if !ok {
				return nil
			}
			var source struct {
				Subagent struct {
					Spawn struct {
						Parent string `json:"parent_thread_id"`
					} `json:"thread_spawn"`
				} `json:"subagent"`
			}
			_ = json.Unmarshal(meta.Payload.Source, &source)
			parent := source.Subagent.Spawn.Parent
			index.parents[path] = meta.Payload.ID
			if parent != "" {
				index.children[parent] = append(index.children[parent], path)
			}
			return nil
		})
		if err != nil {
			return watchAgents{}, false
		}
		index.checked = time.Now()
	}
	// An absent mapping is unknown, not a zero-agent session.
	found := false
	for _, rootID := range index.parents {
		if rootID == id {
			found = true
			break
		}
	}
	if !found {
		return watchAgents{}, false
	}
	started, _ := time.Parse(time.RFC3339Nano, s.Started)
	return watchCountAgentRecords(index.children[id], "codex", started), true
}

func watchRecordID(id string) bool {
	if id == "" {
		return false
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
			return false
		}
	}
	return true
}

func watchCountAgentRecords(paths []string, tool string, started time.Time) watchAgents {
	total, running := len(paths), 0
	known := true
	for _, path := range paths {
		state := watchAgentRecordState(path, tool, started)
		if state == "running" {
			running++
		} else if state == "unknown" {
			known = false
		}
	}
	result := watchAgents{Total: &total, Source: "session sub-agent lifecycle records"}
	if known {
		result.Running = &running
	}
	return result
}

func watchAgentRecordState(path, tool string, started time.Time) string {
	f, err := os.Open(path)
	if err != nil {
		return "unknown"
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "unknown"
	}
	offset := info.Size() - 256*1024
	if offset < 0 {
		offset = 0
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return "unknown"
	}
	b, err := io.ReadAll(io.LimitReader(f, 256*1024))
	if err != nil {
		return "unknown"
	}
	state := "unknown"
	for _, line := range bytes.Split(b, []byte{'\n'}) {
		var event struct {
			Type         string    `json:"type"`
			Timestamp    time.Time `json:"timestamp"`
			ToolEndsTurn bool      `json:"toolEndsTurn"`
			Payload      struct {
				Type string `json:"type"`
			} `json:"payload"`
			Message struct {
				StopReason string `json:"stop_reason"`
			} `json:"message"`
		}
		if json.Unmarshal(line, &event) != nil {
			continue
		}
		if tool == "codex" && event.Type == "event_msg" {
			switch event.Payload.Type {
			case "task_complete", "turn_aborted":
				state = "done"
			case "task_started":
				if !event.Timestamp.Before(started) {
					state = "running"
				} else {
					state = "unknown"
				}
			}
		}
		if tool == "claude" {
			if event.ToolEndsTurn || event.Message.StopReason == "end_turn" || event.Message.StopReason == "stop_sequence" {
				state = "done"
			} else if event.Type == "user" || event.Type == "assistant" {
				if !event.Timestamp.Before(started) {
					state = "running"
				} else {
					state = "unknown"
				}
			}
		}
	}
	return state
}
