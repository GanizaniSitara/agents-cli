package main

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"time"
)

// Keep attempted episodes across reader restarts. Unknown reads cannot prove
// that a block cleared, and must not cause repeated Ctrl+C attempts.
type watchPolicyAttempts map[uint32]string

func (attempted watchPolicyAttempts) ready(s watchSample, enabled bool) bool {
	if s.Phase != "blocked" {
		if s.State != "unknown" {
			delete(attempted, s.PID)
		}
		return false
	}
	return enabled && s.RootStarted != "" && attempted[s.PID] != s.RootStarted
}

func (a *watchApp) policyInterruptLoop() {
	for {
		select {
		case <-a.stop:
			return
		case s := <-a.policyQueue:
			if !loadWatchSettings(a.dir).InterruptPolicyBlock || watchTargetProblem(s, watchReadTarget(s)) != "" {
				continue
			}
			// Re-read immediately before interrupting: an old policy message
			// must never interrupt a newer task in the same console.
			r, err := peek(s.PID, 0)
			if err != nil {
				continue
			}
			if phase, _ := watchPhase(s.Tool, r.Lines, guessState(s.Tool, r.Lines)); phase != "blocked" {
				continue
			}
			if !loadWatchSettings(a.dir).InterruptPolicyBlock || watchTargetProblem(s, watchReadTarget(s)) != "" {
				continue
			}
			select {
			case <-a.stop:
				return
			default:
			}
			exe, err := os.Executable()
			if err != nil {
				continue
			}
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			go func() {
				select {
				case <-a.stop:
					cancel()
				case <-ctx.Done():
				}
			}()
			cmd := exec.CommandContext(ctx, exe, "key", strconv.FormatUint(uint64(s.PID), 10), "ctrl-c")
			cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
			_ = cmd.Run() // key command records the attempt and outcome in sends.jsonl
			cancel()
		}
	}
}
