package main

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestTailLineLimit(t *testing.T) {
	screen := []string{"one", "two", "three", "", " "}
	if got := screenTail(screen, 2); !reflect.DeepEqual(got, []string{"two", "three"}) {
		t.Fatalf("limited screen = %q", got)
	}
	if got := screenTail(screen, 0); !reflect.DeepEqual(got, screen) {
		t.Fatal("unlimited tail must preserve the visible screen")
	}
	for _, args := range [][]string{{"123", "--lines", "0"}, {"123", "--lines", "-1"}, {"123", "--above", "-1"}} {
		if err := cmdTail(args); err == nil {
			t.Fatalf("invalid limits accepted: %v", args)
		}
	}
}

func TestResolveDuplicateLabels(t *testing.T) {
	_, err := resolve("test", []Session{{PID: 1, Name: "test"}, {PID: 2, Name: "TEST"}})
	if err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("duplicate exact labels must be ambiguous: %v", err)
	}
}

func TestLaunchMetadataPIDReuse(t *testing.T) {
	start := time.Date(2026, 10, 2, 8, 0, 0, 0, time.UTC)
	li := launchInfo{ProcessStartTimeUtc: start.Format(time.RFC3339Nano)}
	if !launchMatches(li, start) || !launchMatches(li, time.Time{}) {
		t.Fatal("same process and dead historical rows should be retained")
	}
	if launchMatches(li, start.Add(time.Minute)) {
		t.Fatal("stale launcher ids must not attach to a reused PID")
	}
}

func TestNoDaemonFlag(t *testing.T) {
	for _, cmd := range []string{`codex.exe --no-daemon`, `"C:\Program Files\codex.exe" "--no-daemon" --no-alt-screen`} {
		if !hasCommandFlag(cmd, "--no-daemon") {
			t.Fatalf("missed no-daemon flag: %s", cmd)
		}
	}
	if hasCommandFlag(`codex.exe --no-daemon-other`, "--no-daemon") {
		t.Fatal("flag matching must be exact")
	}
}

func TestLiteralMessageAfterSeparator(t *testing.T) {
	p, err := parseArgs([]string{"123", "--", "--no-enter", "--via", "hello"}, "no-enter")
	if err != nil || len(p.flags) != 0 || !reflect.DeepEqual(p.pos, []string{"123", "--no-enter", "--via", "hello"}) {
		t.Fatalf("literal text changed: %#v %v", p, err)
	}
}
