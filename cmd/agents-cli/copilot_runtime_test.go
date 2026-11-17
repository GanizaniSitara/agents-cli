package main

import (
	"testing"
	"time"
)

func TestCopilotAutopilotFooterDoesNotBecomeIdle(t *testing.T) {
	for _, tc := range []struct {
		name   string
		footer []string
		want   string
		agents int
	}{
		{"wrapped progress", []string{"Working esc to interrupt", "Auto → model", "/ commands · ? help"}, "busy", -1},
		{"parallel workers", []string{"3 agents running", "Autopilot", "model", "/ commands · ? help"}, "busy", 3},
		{"agents finished", []string{"0/3 agents running", "Autopilot", "/ commands · ? help"}, "idle", 0},
		{"enabled only", []string{"Autopilot", "/ commands · ? help"}, "idle", -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lines := append(copilotScreen("", "")[:4], tc.footer...)
			if got := copilotState(lines); got != tc.want {
				t.Fatalf("got %s want %s", got, tc.want)
			}
			counts := watchAgentDetails("copilot", lines)
			if tc.agents >= 0 && (counts.Running == nil || *counts.Running != tc.agents) {
				t.Fatalf("live counts missed: %+v", counts)
			}
			if tc.agents < 0 && counts.Running != nil {
				t.Fatal("invented worker count")
			}
		})
	}
	lines := append([]string{"Old answer: Working esc to interrupt, 3 agents running"}, copilotScreen("", "/ commands · ? help")...)
	if copilotState(lines) != "idle" || watchAgentDetails("copilot", lines).Running != nil {
		t.Fatal("historical work treated as active")
	}
	lines = append([]string{"Running agents", "agent old [running]"}, copilotScreen("", "/ commands · ? help")...)
	if copilotState(lines) != "idle" || watchAgentDetails("copilot", lines).Running != nil {
		t.Fatal("historical task panel became current work")
	}
	if copilotState(copilotScreen(" yes", "Enter to select · esc to cancel")) != "prompt" {
		t.Fatal("approval request hidden")
	}
	w := newWatchTracker(time.Second)
	s := watchSample{Session: Session{PID: 1, Tool: "copilot", State: "prompt"}, Agents: watchAgents{Running: agentNumber("2")}, Phase: "waiting"}
	w.observe(s, time.Now())
	if e := w.entries[1]; !e.Stopped || !e.Sample.Working || e.Sample.State != "prompt" {
		t.Fatal("parallel agents hid actionable input")
	}
}

func TestCopilotLiveBuildOwnershipAndInfrastructure(t *testing.T) {
	now := time.Date(2026, 11, 17, 9, 0, 0, 0, time.UTC)
	s := Session{Tool: "copilot", PID: 10, AgentPID: 11, Started: now.Format(time.RFC3339)}
	m := procMap{
		10: {PID: 10, Name: "cmd.exe"},
		11: {PID: 11, PPID: 10, Name: "copilot.exe"},
		12: {PID: 12, PPID: 11, Name: "dotnet.exe", Cmd: "dotnet.exe build app.csproj"},
		13: {PID: 13, PPID: 11, Name: "node.exe", Cmd: "node.exe mcp-server.js"},
		14: {PID: 14, PPID: 13, Name: "python.exe", Cmd: "python.exe helper.py"},
		15: {PID: 15, PPID: 99, Name: "go.exe", Cmd: "go.exe build ./..."},
	}
	started := func(pid uint32) time.Time { return now.Add(time.Duration(pid-10) * time.Second) }
	command := func(pid uint32) string { return m[pid].Cmd }
	if got := agentRuntimeWork(s, m, command, started); got != 1 {
		t.Fatalf("work=%d, want only owned build", got)
	}
	delete(m, 12)
	if got := agentRuntimeWork(s, m, command, started); got != 0 {
		t.Fatalf("finished build or MCP work latched: %d", got)
	}
	m[12] = &proc{PID: 12, PPID: 11, Name: "go.exe", Cmd: "go.exe test ./..."}
	stale := func(pid uint32) time.Time {
		if pid == 12 {
			return now.Add(-time.Hour)
		}
		return started(pid)
	}
	if got := agentRuntimeWork(s, m, command, stale); got != 0 {
		t.Fatal("reused child counted")
	}
	s.Started = now.Add(-time.Hour).Format(time.RFC3339)
	if got := agentRuntimeWork(s, m, command, started); got != 0 {
		t.Fatal("reused root counted")
	}
}
