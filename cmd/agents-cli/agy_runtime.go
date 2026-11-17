package main

import (
	"strings"
	"time"
)

// A live child command is independent evidence when AGY or Copilot leaves an idle
// composer or its console redraw is empty. Persistent MCP servers are excluded.
// Never use AGY's own CPU usage, file mtimes or old transcript text as liveness.
func agyWorkCommand(p *proc) bool {
	cmd := strings.ToLower(strings.ReplaceAll(p.Cmd, `\`, "/"))
	if cmd == "" || agentInfrastructureCommand(cmd) {
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
	case "go.exe", "cargo.exe":
		return hasCommandFlag(cmd, "build") || hasCommandFlag(cmd, "test") || hasCommandFlag(cmd, "run") || hasCommandFlag(cmd, "check")
	case "dotnet.exe":
		return hasCommandFlag(cmd, "build") || hasCommandFlag(cmd, "test") || hasCommandFlag(cmd, "publish") || hasCommandFlag(cmd, "restore")
	case "msbuild.exe", "ninja.exe":
		return true
	case "cmake.exe":
		return hasCommandFlag(cmd, "--build")
	}
	return false
}

func agentInfrastructureCommand(cmd string) bool {
	cmd = strings.ToLower(cmd)
	return strings.Contains(cmd, "mcp") || strings.Contains(cmd, "--stdio") || strings.Contains(cmd, "stdio-bridge")
}

func agentRuntimeWork(s Session, processes procMap, command func(uint32) string, started func(uint32) time.Time) int {
	if (s.Tool != "agy" && s.Tool != "copilot") || s.AgentPID == 0 || processes[s.AgentPID] == nil {
		return 0
	}
	agent := *processes[s.AgentPID]
	agent.Cmd = strings.ToLower(command(s.AgentPID))
	if classify(&agent) != s.Tool {
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
	commands := map[uint32]string{}
	readCommand := func(pid uint32) string {
		if value, ok := commands[pid]; ok {
			return value
		}
		value := command(pid)
		commands[pid] = value
		return value
	}
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
		copy.Cmd = readCommand(pid)
		infrastructure := false
		for parent, hops := processes[p.PPID], 0; parent != nil && parent.PID != s.AgentPID && hops < 32; parent, hops = processes[parent.PPID], hops+1 {
			if agentInfrastructureCommand(readCommand(parent.PID)) {
				infrastructure = true
				break
			}
		}
		if infrastructure {
			continue
		}
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
