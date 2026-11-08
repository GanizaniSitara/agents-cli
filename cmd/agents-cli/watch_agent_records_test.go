package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSubAgentLifecycleCounts(t *testing.T) {
	started := time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct{ name, tool, text, want string }{
		{"codex running", "codex", `{"type":"event_msg","timestamp":"2026-11-07T10:00:00Z","payload":{"type":"task_started"}}`, "running"},
		{"codex complete", "codex", `{"type":"event_msg","timestamp":"2026-11-05T10:00:00Z","payload":{"type":"task_complete"}}`, "done"},
		{"old interrupted run", "codex", `{"type":"event_msg","timestamp":"2026-11-05T10:00:00Z","payload":{"type":"task_started"}}`, "unknown"},
		{"claude tool ends turn", "claude", `{"type":"user","timestamp":"2026-11-05T10:00:00Z","toolEndsTurn":true}`, "done"},
		{"claude final", "claude", `{"type":"assistant","timestamp":"2026-11-07T10:00:00Z","message":{"stop_reason":"end_turn"}}`, "done"},
		{"claude running", "claude", `{"type":"assistant","timestamp":"2026-11-07T10:00:00Z","message":{"stop_reason":"tool_use"}}`, "running"},
		{"truncated write", "codex", `{"type":"event_msg"`, "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "agent.jsonl")
			if err := os.WriteFile(path, []byte(tc.text+"\n"), 0600); err != nil {
				t.Fatal(err)
			}
			if got := watchAgentRecordState(path, tc.tool, started); got != tc.want {
				t.Fatalf("got %s want %s", got, tc.want)
			}
		})
	}
}

func TestCodexCountsOnlyLinkedChildren(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, "sessions")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"root.jsonl":  `{"type":"session_meta","payload":{"id":"root","source":"cli"}}`,
		"child.jsonl": `{"type":"session_meta","payload":{"id":"child","source":{"subagent":{"thread_spawn":{"parent_thread_id":"root"}}}}}` + "\n" + `{"type":"event_msg","timestamp":"2026-11-07T10:00:00Z","payload":{"type":"task_complete"}}`,
		"other.jsonl": `{"type":"session_meta","payload":{"id":"other","source":{"subagent":{"thread_spawn":{"parent_thread_id":"different-root"}}}}}` + "\n" + `{"type":"event_msg","timestamp":"2026-11-07T10:00:00Z","payload":{"type":"task_started"}}`,
	}
	for name, text := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	r := &watchAgentRecords{}
	a, ok := r.counts(Session{Tool: "codex", Home: home, ThreadID: "root", Started: "2026-11-06T00:00:00Z"})
	if !ok || a.Total == nil || *a.Total != 1 || a.Running == nil || *a.Running != 0 {
		t.Fatalf("incorrect linked count: %+v", a)
	}
	if _, ok := r.counts(Session{Tool: "codex", Home: home, ThreadID: "missing"}); ok {
		t.Fatal("unknown session reported as zero")
	}
}
