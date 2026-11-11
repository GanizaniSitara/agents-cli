package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWatchPanelMonitorWritesBoundedLocalStallEvidence(t *testing.T) {
	dir := t.TempDir()
	a := &watchApp{dir: dir}
	a.ui.current.Store(&watchUIStage{"synthetic-stall", time.Now().Add(-6 * time.Second)})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); a.monitorPanel(ctx) }()
	deadline := time.Now().Add(3 * time.Second)
	var data []byte
	for time.Now().Before(deadline) {
		data, _ = os.ReadFile(filepath.Join(dir, "watcher.log"))
		if strings.Contains(string(data), "goroutine") {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	<-done
	if !strings.Contains(string(data), `"event":"panel-stall"`) || !strings.Contains(string(data), "synthetic-stall") || !strings.Contains(string(data), "goroutine") || len(data) > 70<<10 {
		t.Fatalf("stall evidence absent or unbounded: %d bytes", len(data))
	}
}

func TestWatchDetachedCrashOutputSurvivesParentHandleClose(t *testing.T) {
	dir := t.TempDir()
	output, err := openWatchOutput(dir)
	if err != nil {
		t.Fatal(err)
	}
	cmd := watchBackgroundCommand(os.Args[0], nil, output)
	cmd.Env = append(os.Environ(), "AGENTS_CLI_TEST_WATCH_CRASH=1")
	if err := cmd.Start(); err != nil {
		output.Close()
		t.Fatal(err)
	}
	output.Close()
	timer := time.AfterFunc(3*time.Second, func() { _ = cmd.Process.Kill() })
	defer timer.Stop()
	if cmd.Wait() == nil {
		t.Fatal("crash fixture exited successfully")
	}
	b, err := os.ReadFile(filepath.Join(dir, "watcher.log"))
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range []string{"watcher-startup-fixture", "panic: intentional-watcher-crash-fixture", "goroutine"} {
		if !strings.Contains(string(b), marker) {
			t.Fatalf("missing captured output: %s", marker)
		}
	}
}

func TestWatchOutputRetainsPreviousCrashOnRotation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "watcher.log")
	old := strings.Repeat("x", watchOutputLimit)
	if err := os.WriteFile(path, []byte(old), 0600); err != nil {
		t.Fatal(err)
	}
	f, err := openWatchOutput(dir)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	b, err := os.ReadFile(filepath.Join(dir, "watcher.previous.log"))
	if err != nil || string(b) != old {
		t.Fatal("previous crash output not retained", err)
	}
}

func TestWatchUIStallProbeHandlesNestedMessagesAndReportsOnce(t *testing.T) {
	var p watchUIProbe
	old := &watchUIStage{"timer.taskbar", time.Now().Add(-6 * time.Second)}
	p.current.Store(old)
	leave := p.enter("nested-paint")
	if p.stalled(time.Now()) != nil {
		t.Fatal("new nested call reported as stalled")
	}
	leave()
	if p.stalled(time.Now()) != old {
		t.Fatal("outer stalled operation lost")
	}
	if p.stalled(time.Now()) != nil {
		t.Fatal("unchanged stall repeatedly dumped stacks")
	}
	p.step("idle")
	if p.stalled(time.Now().Add(time.Hour)) != nil {
		t.Fatal("idle message loop reported as stalled")
	}
	p.step("timer.tracker-lock")
	if p.stalled(time.Now().Add(6*time.Second)) == nil {
		t.Fatal("subsequent stall not reported")
	}
}
