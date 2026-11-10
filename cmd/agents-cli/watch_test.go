package main

import (
	"strings"
	"testing"
	"time"
)

func TestWatchWorkerHeartbeatBoundsStuckRead(t *testing.T) {
	// Reuse TECH-262's detached child fixture. It hangs before any console read;
	// the watcher's independent heartbeat must kill it and return promptly.
	t.Setenv("AGENTS_CLI_TEST_HELPER_HANG", "1")
	a := &watchApp{stop: make(chan struct{}), dir: t.TempDir(), tracker: newWatchTracker(time.Second)}
	started := time.Now()
	err := a.readWorker()
	if err == nil || !strings.Contains(err.Error(), "heartbeat timed out") {
		t.Fatalf("missing timeout: %v", err)
	}
	if failure, ok := err.(*watchReaderError); !ok || failure.Code != "WR102" {
		t.Fatalf("missing heartbeat code: %v", err)
	}
	if time.Since(started) > watchHeartbeatTimeout+2*time.Second {
		t.Fatal("stuck reader outlived watchdog budget")
	}
}

func TestWatchStableTransitions(t *testing.T) {
	now := time.Unix(100, 0)
	w := newWatchTracker(3 * time.Second)
	s := watchSample{Session: Session{PID: 1, Started: "a", State: "busy"}, HWND: 9}
	step := func(d time.Duration, state, sig string) bool {
		s.State, s.Signature = state, sig
		return w.observe(s, now.Add(d))
	}
	step(0, "busy", "a")
	step(time.Second, "idle", "b")
	step(2*time.Second, "busy", "c")
	step(3*time.Second, "idle", "d")
	if step(5*time.Second, "idle", "d") {
		t.Fatal("brief pause notified")
	}
	if !step(6*time.Second, "idle", "d") {
		t.Fatal("stable completion missed")
	}
	if step(7*time.Second, "idle", "d") {
		t.Fatal("duplicate notification")
	}
	w.dismiss(1)
	if len(w.rows()) != 0 {
		t.Fatal("dismiss failed")
	}
	step(8*time.Second, "idle", "d")
	if len(w.rows()) != 0 {
		t.Fatal("dismissal did not stick")
	}
	step(9*time.Second, "busy", "e")
	step(10*time.Second, "prompt", "f")
	if !step(13*time.Second, "prompt", "f") {
		t.Fatal("new input wait missed")
	}
	step(14*time.Second, "busy", "g")
	if len(w.rows()) != 0 {
		t.Fatal("resumed work still displayed")
	}
}

func TestWatchBaselineUnknownChangingAndPIDReuse(t *testing.T) {
	now := time.Unix(100, 0)
	w := newWatchTracker(3 * time.Second)
	s := watchSample{Session: Session{PID: 1, Started: "a", State: "idle"}, HWND: 9, Signature: "a"}
	w.observe(s, now)
	if w.observe(s, now.Add(3*time.Second)) {
		t.Fatal("startup must not flash old idle windows")
	}
	if len(w.rows()) != 1 {
		t.Fatal("startup idle should appear in list")
	}
	s.State = "busy"
	w.observe(s, now.Add(4*time.Second))
	s.State = "idle"
	w.observe(s, now.Add(5*time.Second))
	s.Signature = "redraw"
	w.observe(s, now.Add(7*time.Second))
	if w.observe(s, now.Add(9*time.Second)) {
		t.Fatal("changing screen must reset debounce")
	}
	s.State = "unknown"
	w.observe(s, now.Add(10*time.Second))
	if len(w.rows()) != 0 {
		t.Fatal("unreadable window shown as stopped")
	}
	s.Started = "b"
	s.State = "idle"
	w.observe(s, now.Add(11*time.Second))
	if w.observe(s, now.Add(14*time.Second)) {
		t.Fatal("reused PID inherited busy state")
	}
	w.prune(nil)
	if len(w.rows()) != 0 {
		t.Fatal("closed window retained")
	}
}

func TestWatchPanelKeepsSessionSlotsWithinGroups(t *testing.T) {
	w := newWatchTracker(time.Second)
	now := time.Unix(100, 0)
	for _, pid := range []uint32{30, 10, 20} {
		s := watchSample{Session: Session{PID: pid, State: "prompt"}}
		w.observe(s, now)
		w.observe(s, now.Add(time.Second))
	}
	w.observe(watchSample{Session: Session{PID: 10, State: "busy"}}, now.Add(2*time.Second))
	rows := w.panelRows()
	for i, pid := range []uint32{30, 20, 10} {
		if rows[i].Sample.PID != pid {
			t.Fatal("sessions reshuffled")
		}
	}
	if rows[2].Sample.State != "busy" {
		t.Fatal("working session did not move to working group")
	}
	w.dismiss(10)
	rows = w.panelRows()
	if len(rows) != 2 || rows[0].Sample.PID != 30 || rows[1].Sample.PID != 20 {
		t.Fatal("dismiss reshuffled surviving rows")
	}
}

func TestWatchPanelGroupsIncludeUnknownAndStopped(t *testing.T) {
	w := newWatchTracker(time.Second)
	now := time.Unix(100, 0)
	samples := []watchSample{
		{Session: Session{PID: 1, State: "busy"}, Phase: "working"},
		{Session: Session{PID: 2, State: "idle"}, Phase: "finished"},
		{Session: Session{PID: 3, State: "unknown"}, Phase: "unknown"},
		{Session: Session{PID: 4, State: "error"}, Phase: "error"},
		{Session: Session{PID: 5, State: "prompt"}, Phase: "waiting"},
		{Session: Session{PID: 6, State: "idle"}, Phase: "idle"},
	}
	for _, s := range samples {
		s.Desktop = "Desktop 2"
		w.observe(s, now)
		w.observe(s, now.Add(time.Second))
	}
	check := func(want []uint32) {
		t.Helper()
		rows := w.panelRows()
		if len(rows) != len(want) {
			t.Fatalf("rows: got %d want %d", len(rows), len(want))
		}
		for i, pid := range want {
			if rows[i].Sample.PID != pid {
				t.Fatalf("row %d: got %d want %d", i, rows[i].Sample.PID, pid)
			}
			if rows[i].Sample.Desktop != "Desktop 2" {
				t.Fatal("desktop lost")
			}
		}
	}
	check([]uint32{5, 4, 2, 1, 3, 6})
	// Refreshing the agent child identity in the same console must not move
	// its row when its group remains unchanged.
	s := samples[4]
	s.Desktop = "Desktop 2"
	s.AgentPID = 99
	w.observe(s, now.Add(2*time.Second))
	check([]uint32{5, 4, 2, 1, 3, 6})
	// Unknown becoming error moves to the error group, behind its existing
	// row. New needs-input arrivals likewise join after existing input rows.
	s = samples[2]
	s.Desktop = "Desktop 2"
	s.State = "error"
	s.Phase = "error"
	w.observe(s, now.Add(2*time.Second))
	check([]uint32{5, 4, 3, 2, 1, 6})
	s = samples[0]
	s.Desktop = "Desktop 2"
	s.State = "prompt"
	s.Phase = "waiting"
	w.observe(s, now.Add(3*time.Second))
	check([]uint32{5, 1, 4, 3, 2, 6})
	// An unreadable or failed session stays a real row, not just a count.
	w.dismiss(4)
	w.prune(map[uint32]bool{1: true, 2: true, 3: true, 5: true, 6: true})
	check([]uint32{5, 1, 3, 2, 6})
}
