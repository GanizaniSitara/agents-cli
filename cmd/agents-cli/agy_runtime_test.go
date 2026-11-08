package main

import (
	"testing"
	"time"
)

func TestAGYRuntimeWorkAndOwnership(t *testing.T) {
	start := time.Date(2026, 11, 8, 18, 0, 0, 0, time.UTC)
	s := Session{Tool: "agy", PID: 10, AgentPID: 11, Started: start.Format(time.RFC3339)}
	m := procMap{
		10: {PID: 10, Name: "cmd.exe"},
		11: {PID: 11, PPID: 10, Name: "agy.exe"},
		12: {PID: 12, PPID: 11, Name: "python.exe", Cmd: `python.exe c:/example/example-stdio-bridge.py`},
		13: {PID: 13, PPID: 11, Name: "pwsh.exe", Cmd: `pwsh.exe -NoProfile -Command "python example_job.py"`},
		14: {PID: 14, PPID: 13, Name: "python.exe", Cmd: `python.exe example_job.py`},
		15: {PID: 15, PPID: 11, Name: "codex.exe", Cmd: `codex.exe exec "example prompt"`},
		16: {PID: 16, PPID: 99, Name: "python.exe", Cmd: `python.exe unrelated.py`},
	}
	started := func(pid uint32) time.Time { return start.Add(time.Duration(pid-10) * time.Second) }
	command := func(pid uint32) string { return m[pid].Cmd }
	if got := agyRuntimeWork(s, m, command, started); got != 3 {
		t.Fatalf("work processes=%d, want shell, command and noninteractive agent", got)
	}
	delete(m, 13)
	delete(m, 14)
	delete(m, 15)
	if got := agyRuntimeWork(s, m, command, started); got != 0 {
		t.Fatalf("MCP bridge/unrelated process counted as work: %d", got)
	}
	m[13] = &proc{PID: 13, PPID: 11, Name: "pwsh.exe", Cmd: "pwsh.exe -File example.ps1"}
	stale := func(pid uint32) time.Time {
		if pid == 13 {
			return start.Add(-time.Hour)
		}
		return started(pid)
	}
	if got := agyRuntimeWork(s, m, command, stale); got != 0 {
		t.Fatalf("stale child ancestry counted: %d", got)
	}
	s.Started = start.Add(-time.Hour).Format(time.RFC3339)
	if got := agyRuntimeWork(s, m, command, started); got != 0 {
		t.Fatalf("reused session root counted: %d", got)
	}
}

func TestAGYRuntimeStateDoesNotLatchOrHideApproval(t *testing.T) {
	for _, state := range []string{"idle", "unknown"} {
		if got := agyRuntimeState(state, 1); got != "busy" {
			t.Fatalf("live child hidden by %s composer: %s", state, got)
		}
		if got := agyRuntimeState(state, 0); got != state {
			t.Fatalf("finished child left state latched: %s", got)
		}
	}
	if got := agyRuntimeState("prompt", 1); got != "prompt" {
		t.Fatalf("child work hid approval: %s", got)
	}
}

func TestEmptyConsoleRetryBounded(t *testing.T) {
	for _, recover := range []bool{true, false} {
		calls, sleeps := 0, 0
		result := peekWithBlankRetry(func() *peekResult {
			calls++
			if calls == 2 && recover {
				return &peekResult{Lines: []string{"⣟ Reading file..."}}
			}
			return &peekResult{Lines: []string{"", "   "}}
		}, func(d time.Duration) {
			sleeps++
			if d != 20*time.Millisecond {
				t.Fatal(d)
			}
		})
		if calls != 2 || sleeps != 1 || recover && len(trimBlankTail(result.Lines)) == 0 {
			t.Fatalf("incorrect bounded retry: calls=%d sleeps=%d result=%+v", calls, sleeps, result)
		}
	}
}
