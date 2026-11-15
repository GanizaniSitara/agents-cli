package main

import (
	"fmt"
	"os"
	"testing"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

func TestWatchAutostartRegistration(t *testing.T) {
	path := fmt.Sprintf(`Software\AgentsCLI-test-%d-%d`, os.Getpid(), time.Now().UnixNano())
	defer registry.DeleteKey(registry.CURRENT_USER, path)
	command := watchAutostartCommand(`C:\Program Files\Agents CLI\agents-cli.exe`, `C:\Users\Test User\watch`)
	args, err := windows.DecomposeCommandLine(command)
	if err != nil || len(args) != 5 || args[0] != `C:\Program Files\Agents CLI\agents-cli.exe` || args[1] != "watch" || args[2] != "start" || args[3] != "--state-dir" || args[4] != `C:\Users\Test User\watch` {
		t.Fatalf("unsafe startup command: %q (%v)", args, err)
	}
	for _, tc := range []struct {
		mode string
		want bool
	}{{"status", false}, {"enable", true}, {"status", true}, {"disable", false}, {"disable", false}} {
		got, err := watchAutostartRegistry(path, command, tc.mode)
		if err != nil || got != tc.want {
			t.Fatalf("%s: enabled=%v error=%v", tc.mode, got, err)
		}
	}
	if _, err := watchAutostartRegistry(path, command, "invalid"); err == nil {
		t.Fatal("invalid action accepted")
	}
}
