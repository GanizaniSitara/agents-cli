package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMasterVibePermissionLifetime(t *testing.T) {
	lines := []string{"⠇ Running command (1s)", "│ Permission for the file_system.bash tool (uv *) │", "│ uv tool list │", "│ › 1. Allow once │", "│ 4. Deny │", "│ Enter select  Esc reject │"}
	key := vibePermissionKey(lines)
	if key == "" {
		t.Fatal("missing dialog")
	}
	lines[0] = "Different spinner"
	lines[3] = "│ 1. Allow once │"
	lines[4] = "│ › 4. Deny │"
	if vibePermissionKey(lines) != key {
		t.Fatal("cosmetic changes split permission")
	}
	f := newMasterFeed()
	f.ready = true
	e := watchEntry{Attention: true, Stopped: true, Sample: watchSample{Session: Session{PID: 12, Tool: "vibe", State: "prompt"}, Phase: "waiting", MasterPrompt: key}}
	now := time.Now()
	observe := func(seconds int) { f.observeAt(e, lines, false, now.Add(time.Duration(seconds)*time.Second)) }
	observe(0)
	observe(1)
	if f.next != 0 {
		t.Fatal("transient wake")
	}
	observe(3)
	observe(4)
	if f.next != 1 {
		t.Fatal("stable prompt should wake once")
	}
	lines[2] = "│ uv tool update │"
	e.Sample.MasterPrompt = vibePermissionKey(lines)
	observe(5)
	if _, err := f.claim(12, 1); err == nil {
		t.Fatal("old command reply accepted")
	}
	observe(8)
	if f.next != 2 {
		t.Fatal("new command missed")
	}
	e.Stopped = false
	observe(9)
	if _, err := f.more(12, 2); err == nil {
		t.Fatal("expired prompt readable")
	}
	e.Stopped = true
	observe(10)
	observe(13)
	if f.next != 3 {
		t.Fatal("real repeated command missed")
	}
}

func TestMasterScannedIdentity(t *testing.T) {
	id := "11111111-1111-4111-8111-111111111111"
	if resumeThread("codex.exe resume "+id) != id || resumeThread("codex.exe exec resume "+id) != "" {
		t.Fatal("resume selection")
	}
	home := t.TempDir()
	now := time.Now()
	index, _ := json.Marshal(map[string]any{"id": id, "thread_name": "worker", "updated_at": now})
	if err := os.WriteFile(filepath.Join(home, "session_index.jsonl"), index, 0600); err != nil {
		t.Fatal(err)
	}
	day := filepath.Join(home, "sessions", now.Format("2006/01/02"))
	if err := os.MkdirAll(day, 0700); err != nil {
		t.Fatal(err)
	}
	answer := "The worker checked all twelve windows and verified each individual receipt."
	record, _ := json.Marshal(map[string]any{"type": "event_msg", "timestamp": now, "payload": map[string]string{"type": "task_complete", "last_agent_message": answer}})
	path := filepath.Join(day, "rollout-test-"+id+".jsonl")
	if err := os.WriteFile(path, record, 0600); err != nil {
		t.Fatal(err)
	}
	s := watchSample{Session: Session{Title: "worker", Home: home}}
	if completedThread(s, []string{"• " + answer}, "other", now) != id || completedThread(s, []string{"different active thread"}, "other", now) != "" || completedThread(s, []string{answer}, "other", now.Add(3*time.Minute)) != "" {
		t.Fatal("completion proof")
	}
	if mirrorCompletion([]string{"Worked for 1m • 08:32", "› Ask Codex"}) == "" || mirrorCompletion([]string{"Working (2m)"}) != "" {
		t.Fatal("mirror completion")
	}
}
