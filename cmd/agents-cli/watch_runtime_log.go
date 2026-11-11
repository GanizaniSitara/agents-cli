package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
)

const watchOutputLimit = 1 << 20

func openWatchOutput(dir string) (*os.File, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "watcher.log")
	if info, err := os.Stat(path); err == nil && info.Size() >= watchOutputLimit {
		previous := filepath.Join(dir, "watcher.previous.log")
		_ = os.Remove(previous)
		if err := os.Rename(path, previous); err != nil {
			return nil, err
		}
	}
	return os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
}

func watchBackgroundCommand(exe string, args []string, output *os.File) *exec.Cmd {
	cmd := exec.Command(exe, args...)
	cmd.Stdout, cmd.Stderr = output, output
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.DETACHED_PROCESS | windows.BELOW_NORMAL_PRIORITY_CLASS, HideWindow: true}
	return cmd
}

func watchRuntimeEvent(dir, event, stage string, elapsed time.Duration) {
	f, err := os.OpenFile(filepath.Join(dir, "watcher.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return
	}
	defer f.Close()
	_ = json.NewEncoder(f).Encode(struct {
		Time   time.Time `json:"time"`
		PID    int       `json:"pid"`
		Build  string    `json:"build"`
		Event  string    `json:"event"`
		Stage  string    `json:"stage,omitempty"`
		Millis int64     `json:"elapsed_ms,omitempty"`
	}{time.Now(), os.Getpid(), buildCommit, event, stage, elapsed.Milliseconds()})
	if event == "panel-stall" {
		// Local diagnostic stack addresses/function names, not console buffers.
		// One bounded snapshot per blocked operation; never a polling trace.
		buf := make([]byte, 64<<10)
		n := runtime.Stack(buf, true)
		_, _ = f.Write(buf[:n])
	}
}

type watchUIStage struct {
	name string
	at   time.Time
}

type watchUIProbe struct {
	current  atomic.Pointer[watchUIStage]
	reported *watchUIStage // monitor goroutine only
}

func (p *watchUIProbe) enter(name string) func() {
	previous := p.current.Swap(&watchUIStage{name, time.Now()})
	return func() { p.current.Store(previous) }
}

func (p *watchUIProbe) step(name string) {
	p.current.Store(&watchUIStage{name, time.Now()})
}

func (p *watchUIProbe) stalled(now time.Time) *watchUIStage {
	stage := p.current.Load()
	if stage == nil || stage.name == "idle" || now.Sub(stage.at) < 5*time.Second || stage == p.reported {
		return nil
	}
	p.reported = stage
	return stage
}

func (a *watchApp) monitorPanel(ctx context.Context) {
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-tick.C:
			if stage := a.ui.stalled(now); stage != nil {
				watchRuntimeEvent(a.dir, "panel-stall", stage.name, now.Sub(stage.at))
			}
		}
	}
}
