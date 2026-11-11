package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestWatchForegroundSubmissionBypassesScreenCache(t *testing.T) {
	if watchScreenCacheLifetime(10, 10) != 0 {
		t.Fatal("focused session can retain stale waiting state after submission")
	}
	for _, pair := range [][2]uintptr{{10, 20}, {10, 0}, {0, 0}} {
		if watchScreenCacheLifetime(pair[0], pair[1]) != 10*time.Second {
			t.Fatal("background or unknown window lost its bounded cache")
		}
	}
}

// Ten slow synthetic reads exceed the former whole-scan four-second limit.
// This child never discovers, attaches to, or writes to a real console.
func watchSlowScanFixture() int {
	deadline := time.AfterFunc(watchReadDeadline, func() { os.Exit(12) })
	defer deadline.Stop()
	encoder := json.NewEncoder(os.Stdout)
	batch := watchBatch{Alive: map[uint32]string{}}
	published := 0
	for i := 1; i <= 10; i++ {
		if watchPublishProgress(batch, &published, encoder.Encode, deadline.Reset) != nil {
			return 13
		}
		time.Sleep(500 * time.Millisecond)
		pid := uint32(i)
		batch.Alive[pid] = "fixture"
		batch.Samples = append(batch.Samples, watchSample{Session: Session{PID: pid, Tool: "copilot", State: "busy", Started: "fixture"}, HWND: uintptr(i), RootStarted: "fixture", Phase: "working"})
	}
	batch.Alive[88] = "fixture"
	batch.Alive[77] = "replacement"
	batch.Unavailable = []uint32{88, 77}
	batch.Samples = batch.Samples[published:]
	if encoder.Encode(batch) != nil {
		return 14
	}
	return 0
}

func TestWatchSlowProgressingScanKeepsRows(t *testing.T) {
	t.Setenv("AGENTS_CLI_TEST_WATCH_STREAM", "1")
	a := &watchApp{stop: make(chan struct{}), dir: t.TempDir(), tracker: newWatchTracker(time.Second)}
	a.tracker.observe(watchSample{Session: Session{PID: 99, State: "busy"}, HWND: 99}, time.Now())
	a.tracker.observe(watchSample{Session: Session{PID: 88, State: "busy"}, RootStarted: "fixture", HWND: 88}, time.Now())
	a.tracker.observe(watchSample{Session: Session{PID: 77, State: "busy"}, RootStarted: "old", HWND: 77}, time.Now())
	done := make(chan error, 1)
	go func() { done <- a.readWorker() }()
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	seenPartial := false
	for {
		select {
		case err := <-done:
			if err == nil || strings.Contains(err.Error(), "heartbeat timed out") {
				t.Fatalf("progress was lost: %v", err)
			}
			a.mu.Lock()
			defer a.mu.Unlock()
			if !seenPartial || len(a.tracker.entries) != 11 || a.tracker.entries[99] != nil || a.tracker.entries[77] != nil {
				t.Fatalf("incomplete scan handling: partial=%v rows=%d", seenPartial, len(a.tracker.entries))
			}
			if e := a.tracker.entries[88]; e == nil || e.Sample.State != "unknown" || e.Sample.HWND != 88 {
				t.Fatal("live unread console disappeared or claimed an agent error")
			}
			for pid := uint32(1); pid <= 10; pid++ {
				if e := a.tracker.entries[pid]; e == nil || e.Sample.State != "busy" {
					t.Fatalf("lost Copilot row %d", pid)
				}
			}
			return
		case <-tick.C:
			a.mu.Lock()
			if a.tracker.entries[1] != nil && a.tracker.entries[10] == nil {
				seenPartial = true
				if a.tracker.entries[99] == nil {
					a.mu.Unlock()
					t.Fatal("partial scan pruned an unread session")
				}
			}
			a.mu.Unlock()
		case <-timer.C:
			close(a.stop)
			<-done
			t.Fatal("bounded slow-scan fixture did not finish")
		}
	}
}

func TestWatchProgressDoesNotRepeatObservations(t *testing.T) {
	batch := watchBatch{}
	published, resets, received := 0, 0, 0
	encode := func(value any) error {
		b := value.(watchBatch)
		if !b.Partial || b.Alive != nil {
			t.Fatal("progress pretends to be a complete census")
		}
		received += len(b.Samples)
		return nil
	}
	reset := func(d time.Duration) bool {
		if d != watchReadDeadline {
			t.Fatal(d)
		}
		resets++
		return true
	}
	for i := 0; i < 10; i++ {
		if err := watchPublishProgress(batch, &published, encode, reset); err != nil {
			t.Fatal(err)
		}
		batch.Samples = append(batch.Samples, watchSample{})
	}
	received += len(batch.Samples[published:])
	if received != 10 || resets != 10 {
		t.Fatalf("duplicate/missing reads: %d, resets %d", received, resets)
	}
	oldPublished := published
	if err := watchPublishProgress(batch, &published, func(any) error { return fmt.Errorf("pipe closed") }, reset); err == nil || published != oldPublished || resets != 10 {
		t.Fatal("failed publish reset watchdog or dropped samples")
	}
}

func TestWatchReaderFailureRetainsIdentityWithoutFalseState(t *testing.T) {
	w := newWatchTracker(time.Second)
	now := time.Now()
	for pid := uint32(1); pid <= 10; pid++ {
		s := watchSample{Session: Session{PID: pid, State: "busy", Started: "root"}, RootStarted: "root", HWND: uintptr(pid), PendingQuestions: 1, Working: true, Agents: watchAgents{Running: agentNumber("2")}}
		w.observe(s, now)
	}
	w.entries[3].Parked, w.entries[4].Dismissed = true, true
	w.unavailable("reader reconnecting", now.Add(time.Second))
	if len(w.entries) != 10 {
		t.Fatal("reader failure removed sessions")
	}
	for pid, e := range w.entries {
		if e.Sample.PID != pid || e.Sample.RootStarted != "root" || e.Sample.HWND != uintptr(pid) {
			t.Fatal("identity lost")
		}
		if e.Sample.State != "unknown" || e.Sample.Phase != "unknown" || e.Stopped || e.Attention || e.Sample.Working || e.Sample.PendingQuestions != 0 || e.Sample.Agents.Running != nil {
			t.Fatalf("unavailable reader claims live status: %+v", e)
		}
	}
	if !w.entries[3].Parked || !w.entries[4].Dismissed {
		t.Fatal("reader failure reset owner choices")
	}
	w.observe(watchSample{Session: Session{PID: 1, State: "busy", Started: "root"}, RootStarted: "root", HWND: 1}, now.Add(2*time.Second))
	if w.entries[1].Sample.State != "busy" {
		t.Fatal("fresh read did not recover")
	}
}

func TestWatchRestartSkipsHungConsoleWithoutDroppingSessions(t *testing.T) {
	sessions := []Session{{PID: 1}, {PID: 2}, {PID: 3}, {PID: 4}}
	ordered := watchReadOrder(sessions, 2)
	for i, pid := range []uint32{3, 4, 1, 2} {
		if ordered[i].PID != pid {
			t.Fatalf("restart order %v", ordered)
		}
	}
	if sessions[0].PID != 1 || sessions[1].PID != 2 {
		t.Fatal("discovery list was mutated")
	}
	for _, absentOrLast := range []uint32{0, 4, 99} {
		ordered = watchReadOrder(sessions, absentOrLast)
		if len(ordered) != 4 || ordered[0].PID != 1 {
			t.Fatal("missing/last cursor lost sessions")
		}
	}
}
