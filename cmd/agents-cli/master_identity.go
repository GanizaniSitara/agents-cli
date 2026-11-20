package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"golang.org/x/sys/windows"
)

func subscriptionFileTail(path string, limit int64) []byte {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil
	}
	if info.Size() > limit {
		if _, err = f.Seek(-limit, io.SeekEnd); err != nil {
			return nil
		}
	}
	b, _ := io.ReadAll(io.LimitReader(f, limit))
	return b
}

func resumeThread(cmd string) string {
	args, err := windows.DecomposeCommandLine(cmd)
	if err == nil && len(args) >= 3 && args[1] == "resume" && subscriptionUUID.MatchString(args[2]) {
		return args[2]
	}
	return ""
}

func normalizedWords(s string) string {
	return strings.Join(strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }), " ")
}

// A title only selects a candidate. Require a recent completed turn and
// matching rendered output before treating an anonymous TUI as that worker.
func completedThread(s watchSample, lines []string, master string, now time.Time) string {
	if s.Home == "" || !filepath.IsAbs(s.Home) || s.Title == "" {
		return ""
	}
	names := map[string]string{}
	days := map[string]time.Time{}
	for _, line := range bytes.Split(subscriptionFileTail(filepath.Join(s.Home, "session_index.jsonl"), 512<<10), []byte{'\n'}) {
		var entry struct {
			ID      string
			Name    string    `json:"thread_name"`
			Updated time.Time `json:"updated_at"`
		}
		if json.Unmarshal(line, &entry) == nil && subscriptionUUID.MatchString(entry.ID) {
			names[entry.ID] = entry.Name
			if days[entry.ID].IsZero() {
				days[entry.ID] = entry.Updated
			}
		}
	}
	title := strings.TrimSpace(strings.SplitN(s.Title, " | ", 2)[0])
	id := ""
	for k, v := range names {
		if strings.EqualFold(v, title) {
			if id != "" {
				return ""
			}
			id = k
		}
	}
	if id == "" || strings.EqualFold(id, master) {
		return id
	}
	paths, _ := filepath.Glob(filepath.Join(s.Home, "sessions", days[id].Local().Format("2006/01/02"), "rollout-*-"+id+".jsonl"))
	if len(paths) != 1 {
		return ""
	}
	data := bytes.Split(subscriptionFileTail(paths[0], 256<<10), []byte{'\n'})
	for i := len(data) - 1; i >= 0; i-- {
		var rec struct {
			Timestamp time.Time
			Type      string
			Payload   struct {
				Type   string
				Answer string `json:"last_agent_message"`
			}
		}
		if json.Unmarshal(data[i], &rec) != nil || rec.Type != "event_msg" {
			continue
		}
		if rec.Payload.Type == "task_started" {
			return ""
		}
		if rec.Payload.Type != "task_complete" {
			continue
		}
		age := now.Sub(rec.Timestamp)
		if age < 0 || age > 2*time.Minute {
			return ""
		}
		answer := " " + normalizedWords(rec.Payload.Answer) + " "
		for _, line := range lines {
			w := normalizedWords(line)
			if len(strings.Fields(w)) >= 8 && len(w) >= 40 && strings.Contains(answer, " "+w+" ") {
				return id
			}
		}
		return ""
	}
	return ""
}

func mirrorCompletion(lines []string) string {
	nonblank := 0
	for i := len(lines) - 1; i >= 0 && nonblank < 10; i-- {
		l := strings.TrimSpace(lines[i])
		if l == "" {
			continue
		}
		nonblank++
		if strings.HasPrefix(l, "Worked for ") && strings.Contains(l, "•") {
			return l
		}
	}
	return ""
}

func subscriptionAudit(dir string, ev masterEvent, disposition string) {
	path := filepath.Join(dir, "master-dispositions.jsonl")
	if info, err := os.Stat(path); err == nil && info.Size() > 1<<20 {
		_ = os.Rename(path, path+".previous")
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return
	}
	defer f.Close()
	_ = json.NewEncoder(f).Encode(map[string]any{"time": time.Now().UTC(), "event": ev.ID, "pid": ev.Session.PID, "disposition": disposition})
}
