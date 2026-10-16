package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const privateTestText = "privacy-test-only: £漢😀\n" + "not-a-real-credential"

// Exercise the real detached-helper launch and stdin pipe without attaching
// to a user's console. No actual credentials are used in these tests.
func TestMain(m *testing.M) {
	if os.Getenv("AGENTS_CLI_TEST_PIPE") == "1" {
		text, err := io.ReadAll(os.Stdin)
		if err != nil || string(text) != strings.Repeat(privateTestText, 100) || strings.Contains(strings.Join(os.Args, " "), privateTestText) {
			os.Exit(2)
		}
		if len(os.Args) < 4 || os.Args[1] != "_attach" {
			os.Exit(3)
		}
		_, _ = os.Stdout.WriteString("{}")
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestDetachedPromptUsesPipe(t *testing.T) {
	t.Setenv("AGENTS_CLI_TEST_PIPE", "1")
	text := strings.Repeat(privateTestText, 100)
	for _, args := range [][]string{{"inject", "123", "0"}, {"copilot-paste", "123"}} {
		out, err := runHelperInput(text, args...)
		if err != nil {
			t.Fatal(err)
		}
		if err := helperErr(out); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPrivateSendInputAndReceipt(t *testing.T) {
	p, err := parseArgs([]string{"123", "--stdin"}, "stdin")
	if err != nil {
		t.Fatal(err)
	}
	got, err := sendInput(p, strings.NewReader(privateTestText))
	if err != nil || got != privateTestText {
		t.Fatal("stdin changed message")
	}
	p.pos = append(p.pos, privateTestText)
	if _, err := sendInput(p, strings.NewReader("")); err == nil || strings.Contains(err.Error(), privateTestText) {
		t.Fatal("mixed stdin/argv accepted or echoed in error")
	}
	s := &Session{PID: 123, Tool: "copilot", Name: privateTestText, Title: privateTestText, Home: privateTestText}
	for _, via := range []string{"native", "inject"} {
		raw, err := json.Marshal(sendReceipt(s, via, true, true))
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(raw, []byte("sent")) || bytes.Contains(raw, []byte("privacy-test-only")) {
			t.Fatal("send receipt includes private text")
		}
	}
	if err := helperErr([]byte(privateTestText)); err == nil || strings.Contains(err.Error(), "privacy-test-only") {
		t.Fatal("malformed helper output echoed")
	}
}

func TestStopLogScrubsLegacyMetadata(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())
	now := time.Now().UTC().Truncate(time.Second)
	if err := os.MkdirAll(filepath.Dir(stopsPath()), 0700); err != nil {
		t.Fatal(err)
	}
	var legacy []byte
	for _, age := range []time.Duration{time.Hour, 31 * 24 * time.Hour} {
		raw, _ := json.Marshal(map[string]interface{}{"ts": now.Add(-age).Format(time.RFC3339), "pid": 42, "tool": "copilot", "result": "ok", "name": privateTestText, "title": privateTestText, "caller_cmdline": privateTestText})
		legacy = append(legacy, append(raw, '\n')...)
	}
	if err := os.WriteFile(stopsPath(), legacy, 0600); err != nil {
		t.Fatal(err)
	}
	if err := logStop(stopRecord{TS: now.Format(time.RFC3339), PID: 43, Tool: "copilot", Result: "ok"}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(stopsPath())
	if err != nil {
		t.Fatal(err)
	}
	for _, unwanted := range []string{"privacy-test-only", "caller_cmdline", "title", "name"} {
		if bytes.Contains(raw, []byte(unwanted)) {
			t.Fatalf("retained %s", unwanted)
		}
	}
	records, err := retainedStops(now)
	if err != nil || len(records) != 2 || records[0].PID != 42 || records[1].PID != 43 {
		t.Fatalf("audit metadata not preserved: %+v %v", records, err)
	}
	if err := os.WriteFile(stopsPath(), []byte("malformed"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := logStop(stopRecord{}); err == nil {
		t.Fatal("corrupt log silently overwritten")
	}
	raw, _ = os.ReadFile(stopsPath())
	if string(raw) != "malformed" {
		t.Fatal("corrupt log modified")
	}
}

func TestConcurrentStopLogWritesPreserveRecords(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())
	var wg sync.WaitGroup
	errors := make(chan error, 12)
	for pid := uint32(1); pid <= 12; pid++ {
		wg.Add(1)
		go func(pid uint32) {
			defer wg.Done()
			errors <- logStop(stopRecord{TS: time.Now().UTC().Format(time.RFC3339), PID: pid, Result: "ok"})
		}(pid)
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	records, err := retainedStops(time.Now())
	if err != nil || len(records) != 12 {
		t.Fatalf("lost concurrent records: %d %v", len(records), err)
	}
}
