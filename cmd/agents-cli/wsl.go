package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode/utf16"
)

// Terminal exports WT_SESSION into WSL. Match a foreground Copilot process
// to the Windows client for that pane, even when it was started from bash.
const wslCopilotProbe = `for p in $(ps -C copilot,MainThread -o pid=); do
 set -- $(cat "/proc/$p/stat" 2>/dev/null)
 [ "$7" != 0 ] && [ "$5" = "$8" ] || continue
 exe=$(readlink "/proc/$p/exe") || continue
 [ "${exe##*/}" = copilot ] || continue
 tr '\000' '\n' < "/proc/$p/environ" 2>/dev/null | sed -n 's/^WT_SESSION=//p'
done`

func wslSessionKey(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if !subscriptionUUID.MatchString(s) {
		return ""
	}
	return s
}

func wslText(b []byte) string {
	if len(b) > 1 && (b[1] == 0 || b[0] == 0xff && b[1] == 0xfe) {
		var u []uint16
		for i := 0; i+1 < len(b); i += 2 {
			u = append(u, uint16(b[i])|uint16(b[i+1])<<8)
		}
		return strings.TrimPrefix(string(utf16.Decode(u)), "\ufeff")
	}
	return string(b)
}

func wslCopilotSessions(procs procMap, byRoot map[uint32]*Session) {
	hosts := map[string][]uint32{}
	for _, p := range procs {
		if p.Name != "wsl.exe" {
			continue
		}
		if parent := procs[p.PPID]; parent != nil && parent.Name == "wsl.exe" {
			continue
		}
		key := wslSessionKey(envVar(p.PID, "WT_SESSION"))
		if key == "" {
			continue
		}
		hosts[key] = append(hosts[key], p.PID)
	}
	if len(hosts) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	run := func(args ...string) (string, error) {
		cmd := exec.CommandContext(ctx, filepath.Join(os.Getenv("SystemRoot"), "System32", "wsl.exe"), args...)
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		cmd.WaitDelay = 100 * time.Millisecond
		b, err := cmd.Output()
		return wslText(b), err
	}
	names, err := run("--list", "--running", "--quiet")
	if err != nil {
		return
	}
	for _, line := range strings.Split(names, "\n") {
		distro := strings.TrimSpace(line)
		if distro == "" {
			continue
		}
		out, err := run("--distribution", distro, "--exec", "/bin/sh", "-c", wslCopilotProbe)
		if err != nil {
			continue
		}
		for _, value := range strings.Fields(out) {
			ids := hosts[wslSessionKey(value)]
			if len(ids) != 1 || byRoot[ids[0]] != nil {
				continue
			}
			pid := ids[0]
			byRoot[pid] = &Session{PID: pid, Tool: "copilot", Variant: "wsl", Source: "wsl", Kind: "window", Alive: true}
		}
	}
}
