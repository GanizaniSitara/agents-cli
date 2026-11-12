package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestConsoleHelpIsTextAndNeverStartsWatcher(t *testing.T) {
	for _, args := range [][]string{nil, {"help"}, {"--help"}, {"-h"}, {"ls", "--help"}, {"send", "--help"}, {"key", "-h"}, {"wait", "--help"}, {"watch"}, {"watch", "help"}, {"watch", "--help"}, {"watch", "-h"}, {"watch", "start", "--help"}, {"watch", "run", "-h"}} {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		cmd := exec.CommandContext(ctx, os.Args[0], args...)
		cmd.Env = append(os.Environ(), "AGENTS_CLI_TEST_HELP=1")
		out, err := cmd.CombinedOutput()
		cancel()
		if err != nil {
			t.Fatalf("help %v: %v: %s", args, err, out)
		}
		text := string(out)
		if json.Valid(out) || !strings.HasPrefix(text, "agents-cli -") || strings.Contains(text, `\n`) {
			t.Fatalf("help %v is not plain text: %q", args, text)
		}
		background, foreground := strings.Index(text, "agents-cli watch start"), strings.Index(text, "agents-cli watch run")
		if background < 0 || foreground <= background {
			t.Fatal("background and foreground watcher examples must come first, in that order")
		}
	}
}
