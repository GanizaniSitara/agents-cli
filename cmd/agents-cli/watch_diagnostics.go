package main

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

type watchReaderError struct {
	Code     string    `json:"code"`
	Stage    string    `json:"stage"`
	PID      uint32    `json:"pid,omitempty"`
	Detail   string    `json:"detail"`
	Time     time.Time `json:"time"`
	ExitCode *int      `json:"exit_code,omitempty"`
}

func (e *watchReaderError) Error() string { return e.Code + ": " + e.Detail }

func watchReaderFailure(code, stage string, err error) *watchReaderError {
	return &watchReaderError{Code: code, Stage: stage, Detail: fmt.Sprint(err)}
}

func watchReaderExit(scanErr, exitErr error) *watchReaderError {
	code, detail := "WR105", "reader exited unexpectedly"
	if scanErr != nil {
		code, detail = "WR106", "reader pipe failed: "+scanErr.Error()
	}
	e := &watchReaderError{Code: code, Detail: detail}
	var exited *exec.ExitError
	if errors.As(exitErr, &exited) {
		n := exited.ExitCode()
		e.ExitCode = &n
		if scanErr == nil && n == 2 {
			e.Code, e.Detail = "WR104", "reader watchdog expired"
		} else {
			e.Detail += fmt.Sprintf(" (exit %d)", n)
		}
	}
	return e
}

// Called with a.mu held. Keep the last failure after recovery so intermittent
// faults remain diagnosable without storing console or conversation text.
func (a *watchApp) recordReaderError(e watchReaderError) {
	e.Time = time.Now().UTC()
	if e.Stage == "" {
		e.Stage = a.readerStage
	}
	if e.PID == 0 && (e.Stage == "read" || strings.HasPrefix(e.Stage, "read.")) {
		e.PID = a.readingPID
	}
	if e.Code == "WR108" {
		a.lastConsoleError = &e
	} else {
		a.lastReaderError = &e
	}
	if a.readerErrorCounts == nil {
		a.readerErrorCounts = map[string]uint64{}
	}
	a.readerErrorCounts[e.Code]++
}

func watchReaderHeader(code string) string {
	if code == "" {
		return "Agents | reader retrying"
	}
	labels := map[string]string{"WR101": "start failed", "WR102": "no heartbeat", "WR103": "bad reader data", "WR104": "watchdog timeout", "WR105": "reader exited", "WR106": "pipe failed", "WR107": "scan failed", "WR108": "console unreadable"}
	label := labels[code]
	if label == "" {
		label = "reader retrying"
	}
	return "Agents | " + code + " " + label
}
