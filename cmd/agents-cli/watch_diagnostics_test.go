package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWatchReaderNativeFailureCodes(t *testing.T) {
	for _, tc := range []struct {
		mode, code string
		exit       int
	}{{"watchdog", "WR104", 2}, {"protocol", "WR103", 0}, {"exit", "WR105", 7}, {"pipe", "WR106", 0}} {
		t.Run(tc.mode, func(t *testing.T) {
			t.Setenv("AGENTS_CLI_TEST_READER_ERROR", tc.mode)
			a := &watchApp{stop: make(chan struct{}), dir: t.TempDir(), tracker: newWatchTracker(time.Second)}
			err := a.readWorker()
			var failure *watchReaderError
			if !errors.As(err, &failure) || failure.Code != tc.code {
				t.Fatalf("code: %v", err)
			}
			if (tc.mode == "watchdog" || tc.mode == "exit") && (failure.ExitCode == nil || *failure.ExitCode != tc.exit) {
				t.Fatalf("lost process exit status: %+v", failure)
			}
			a.recordReaderError(*failure)
			if tc.mode == "watchdog" && (a.lastReaderError.Stage != "read" || a.lastReaderError.PID != 123) {
				t.Fatalf("lost last read identity: %+v", a.lastReaderError)
			}
			if strings.Contains(err.Error(), "<nil>") {
				t.Fatal("uninformative nil error")
			}
		})
	}
}

func TestWatchReaderDiagnosticsSurviveRecovery(t *testing.T) {
	a := &watchApp{dir: t.TempDir(), tracker: newWatchTracker(time.Second), readerStage: "read", readingPID: 77}
	a.recordReaderError(watchReaderError{Code: "WR104", Detail: "reader watchdog expired"})
	a.recordReaderError(watchReaderError{Code: "WR108", Stage: "read", PID: 88, Detail: "console unavailable"})
	a.problem, a.problemCode = "", "" // successful reader recovery
	a.save()
	var r watchReceipt
	b, err := os.ReadFile(filepath.Join(a.dir, "status.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &r); err != nil {
		t.Fatal(err)
	}
	if r.ErrorCode != "" || r.LastReaderError == nil || r.LastReaderError.Code != "WR104" || r.LastReaderError.PID != 77 || r.ReaderErrorCounts["WR104"] != 1 || r.LastReaderError.Time.IsZero() {
		t.Fatalf("lost recovered failure: %+v", r)
	}
	if r.LastConsoleError == nil || r.LastConsoleError.PID != 88 {
		t.Fatal("console error overwrote reader failure or was lost")
	}
	if watchReaderHeader("WR104") != "Agents | WR104 watchdog timeout" {
		t.Fatal("header missing actionable code")
	}
	if got := watchReaderExit(errors.New("broken pipe"), nil); got.Code != "WR106" {
		t.Fatal(got)
	}
}
