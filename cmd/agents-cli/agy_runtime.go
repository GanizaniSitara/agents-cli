package main

import (
	"strings"
	"time"
)

// A live child command is independent evidence when AGY leaves an idle
// composer or its console redraw is empty. Persistent MCP servers are excluded.
// Never use AGY's own CPU usage, file mtimes or old transcript text as liveness.
func agyWorkCommand(p *proc) bool {
	cmd := strings.ToLower(strings.ReplaceAll(p.Cmd, `\`, "/"))
	if cmd == "" || strings.Contains(cmd, "mcp") || strings.Contains(cmd, "--stdio") || strings.Contains(cmd, "stdio-bridge") {
		return false
	}
	switch p.Name {
	case "pwsh.exe", "powershell.exe":
		return hasCommandFlag(cmd, "-command") || hasCommandFlag(cmd, "-file") || hasCommandFlag(cmd, "-c") || hasCommandFlag(cmd, "-encodedcommand")
	case "cmd.exe":
		return hasCommandFlag(cmd, "/c")
	case "bash.exe", "sh.exe":
		return hasCommandFlag(cmd, "-c") || hasCommandFlag(cmd, "-lc")
	case "python.exe", "pythonw.exe":
		return strings.Contains(cmd, ".py") || hasCommandFlag(cmd, "-c")
	case "codex.exe":
		return hasCommandFlag(cmd, "exec")
	}
	return false
}

func agyRuntimeWork(s Session, processes procMap, command func(uint32) string, started func(uint32) time.Time) int {
	if s.Tool != "agy" || s.AgentPID == 0 || processes[s.AgentPID] == nil || processes[s.AgentPID].Name != "agy.exe" {
		return 0
	}
	rootTime := started(s.PID)
	if rootTime.IsZero() || rootTime.Format(time.RFC3339) != normalizeWatchStart(s.Started) {
		return 0
	}
	agentTime := started(s.AgentPID)
	if agentTime.Before(rootTime) {
		return 0
	}
	work := 0
	children := processes.descendants(s.AgentPID)
	if len(children) > 32 {
		children = children[:32]
	}
	for _, pid := range children {
		p := processes[pid]
		if p.Name == "conhost.exe" {
			continue
		}
		childTime := started(pid)
		parentTime := started(p.PPID)
		if childTime.IsZero() || parentTime.IsZero() || childTime.Before(agentTime) || childTime.Before(parentTime) {
			continue // stale/reused parent IDs do not prove ownership
		}
		copy := *p
		copy.Cmd = command(pid)
		if agyWorkCommand(&copy) {
			work++
		}
	}
	return work
}

func agyRuntimeState(state string, work int) string {
	if work > 0 && (state == "idle" || state == "unknown") {
		return "busy"
	}
	return state // an explicit approval/question still needs the owner
}
