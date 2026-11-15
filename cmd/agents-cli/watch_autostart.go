package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"

	"golang.org/x/sys/windows/registry"
)

const watchAutostartKey = `Software\Microsoft\Windows\CurrentVersion\Run`

type watchAutostartResult struct {
	Enabled bool   `json:"enabled"`
	Error   string `json:"error,omitempty"`
}

func watchAutostartCommand(exe, dir string) string {
	return syscall.EscapeArg(exe) + " watch start --state-dir " + syscall.EscapeArg(dir)
}

func watchAutostartRegistry(path, command, mode string) (bool, error) {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, path, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return false, err
	}
	defer k.Close()
	switch mode {
	case "enable":
		if err := k.SetStringValue("AgentsCLI", command); err != nil {
			return false, err
		}
	case "disable":
		if err := k.DeleteValue("AgentsCLI"); err != nil && err != registry.ErrNotExist {
			return false, err
		}
	case "status":
	default:
		return false, fmt.Errorf("invalid autostart action")
	}
	value, _, err := k.GetStringValue("AgentsCLI")
	if err == registry.ErrNotExist {
		return false, nil
	}
	return value == command, err
}

// Isolate registry calls in a bounded helper; never block the panel thread.
func watchAutostartQuery(dir, mode string) watchAutostartResult {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	exe, err := os.Executable()
	if err != nil {
		return watchAutostartResult{Error: err.Error()}
	}
	cmd := exec.CommandContext(ctx, exe, "watch", "autostart", "--action", mode, "--state-dir", dir)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	cmd.WaitDelay = 100 * time.Millisecond
	out, err := cmd.Output()
	var result watchAutostartResult
	if err != nil || json.Unmarshal(out, &result) != nil {
		return watchAutostartResult{Error: "Startup registration failed or timed out"}
	}
	return result
}
