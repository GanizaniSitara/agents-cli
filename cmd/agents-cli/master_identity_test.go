package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

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
