package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestCommandLimits(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want time.Duration
	}{
		{[]string{"ls", "--fast"}, 60 * time.Second},
		{[]string{"tail", "123"}, 30 * time.Second},
		{[]string{"send", "123", "text", "--force"}, 30 * time.Second},
		{[]string{"_attach", "peek", "123"}, 15 * time.Second},
		{[]string{"wait", "123", "--timeout", "2s"}, 3 * time.Second},
		{[]string{"ls", "--timeout=50ms", "--fast"}, 50 * time.Millisecond},
	} {
		got, err := commandLimit(tc.args)
		if err != nil || got != tc.want {
			t.Fatalf("%v: got %s, %v; want %s", tc.args, got, err, tc.want)
		}
	}
	for _, value := range []string{"0", "-1s", "broken"} {
		if _, err := commandLimit([]string{"send", "123", "text", "--timeout", value}); err == nil {
			t.Fatalf("accepted invalid timeout %q", value)
		}
	}
}

func TestCommandWatchdogEndsSpin(t *testing.T) {
	for _, verb := range []string{"ls", "tail", "send", "key", "wait"} {
		t.Run(verb, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], verb, "--timeout", "100ms")
			cmd.Env = append(os.Environ(), "AGENTS_CLI_TEST_SPIN=1")
			out, err := cmd.Output()
			if err == nil || ctx.Err() != nil || !strings.Contains(string(out), "agents_command_timeout") {
				t.Fatalf("watchdog did not terminate spin: err=%v output=%s", err, out)
			}
		})
	}
}

func TestAttachWatchdogIncludesStdin(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 18*time.Second)
	defer cancel()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	cmd := exec.CommandContext(ctx, os.Args[0], "_attach", "inject", "0", "0")
	cmd.Env = append(os.Environ(), "AGENTS_CLI_TEST_DEADLINE=1")
	cmd.Stdin = r
	start := time.Now()
	out, err := cmd.Output()
	if err == nil || ctx.Err() != nil || time.Since(start) > 17*time.Second || !strings.Contains(string(out), "agents_command_timeout") {
		t.Fatalf("orphan helper did not exit: elapsed=%s err=%v output=%s", time.Since(start), err, out)
	}
}

func TestConsoleHelperHonoursParentDeadline(t *testing.T) {
	t.Setenv("AGENTS_CLI_TEST_HELPER_HANG", "1")
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	previous := operationContext
	operationContext = ctx
	defer func() { operationContext = previous }()
	start := time.Now()
	_, err := runHelper("peek", "0")
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > time.Second {
		t.Fatalf("helper did not honour overall deadline: elapsed=%s err=%v", time.Since(start), err)
	}
}

// A writer that never closes stdin used to strand send before discovery.
func TestSendDeadlineIncludesStdin(t *testing.T) {
	t.Setenv("LOCALAPPDATA",t.TempDir())
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	cmd := exec.CommandContext(ctx, os.Args[0], "send", "0", "--stdin", "--timeout", "100ms")
	cmd.Env = append(os.Environ(), "AGENTS_CLI_TEST_DEADLINE=1")
	cmd.Stdin = r
	start := time.Now()
	out, err := cmd.Output()
	var result struct {
		Error struct{ Code, Message string }
	}
	if err == nil || ctx.Err() != nil || time.Since(start) > time.Second || json.Unmarshal(out, &result) != nil ||
		result.Error.Code != "agents_command_timeout" || !strings.Contains(result.Error.Message, "send") {
		t.Fatalf("send deadline did not end blocked stdin: elapsed=%s err=%v output=%s", time.Since(start), err, out)
	}
}
