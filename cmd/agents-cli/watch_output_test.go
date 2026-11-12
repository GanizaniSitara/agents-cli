package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWatchBlockedDiskLeavesTrackerAndPanelResponsive(t *testing.T) {
	for _, stage := range []string{"status", "settings", "events"} {
		t.Run(stage, func(t *testing.T) {
			a := &watchApp{tracker: newWatchTracker(0), output: &watchOutput{}, dir: t.TempDir()}
			sample := watchSample{Session: Session{PID: 1, State: "idle"}, HWND: 1}
			a.tracker.observe(sample, time.Now())
			a.save()
			a.persistSettings(watchSettings{})
			a.queueEvent(sample)
			entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
			block := func(which string) {
				if stage == which {
					close(entered)
					<-release
				}
			}
			go func() {
				defer close(done)
				a.output.flush(func([]byte) { block("status") }, func(watchSettings) { block("settings") }, func([][]byte) { block("events") })
			}()
			defer func() { close(release); <-done }()
			select {
			case <-entered:
			case <-time.After(time.Second):
				t.Fatal("writer did not enter injected block")
			}
			progress := make(chan struct{})
			go func() {
				for i := 0; i < 200; i++ {
					a.mu.Lock()
					sample.State = "busy"
					a.tracker.observe(sample, time.Now())
					a.save()
					a.queueEvent(sample)
					a.mu.Unlock()
					a.persistSettings(watchSettings{SuggestSessionNames: i%2 == 0})
				}
				var bar watchTaskbarController
				a.panelSignals("", &bar, func(uintptr, bool) {}, func(uintptr, uint32) bool { return true })
				close(progress)
			}()
			select {
			case <-progress:
			case <-time.After(time.Second):
				t.Fatal("disk block reached tracker/panel")
			}
			a.output.mu.Lock()
			pending := append([]byte(nil), a.output.status...)
			events := len(a.output.events)
			settings := a.output.settings
			a.output.mu.Unlock()
			var receipt watchReceipt
			if err := json.Unmarshal(pending, &receipt); err != nil {
				t.Fatal(err)
			}
			if len(receipt.Sessions) != 1 || receipt.Sessions[0].Sample.State != "busy" {
				t.Fatalf("latest state not queued: %+v", receipt.Sessions)
			}
			if events != 64 || settings == nil || settings.SuggestSessionNames || a.currentSettings().SuggestSessionNames {
				t.Fatal("output queue not bounded/latest or setting change delayed")
			}
		})
	}
}

func TestWatchOutputWritesReceiptsSettingsAndEvents(t *testing.T) {
	a := &watchApp{tracker: newWatchTracker(0), output: &watchOutput{}, dir: t.TempDir(), stop: make(chan struct{}), started: "fixture"}
	done := make(chan struct{})
	go func() { defer close(done); a.writeOutput() }()
	defer func() { close(a.stop); <-done }()
	a.persistSettings(watchSettings{SuggestSessionNames: true, FirstRowShortcut: "Ctrl+Win+Z"})
	a.save()
	a.queueEvent(watchSample{Session: Session{PID: 123, Title: "Fixture"}})
	until := time.Now().Add(2 * time.Second)
	for time.Now().Before(until) {
		b, err := os.ReadFile(filepath.Join(a.dir, "status.json"))
		events, _ := os.ReadFile(filepath.Join(a.dir, "events.jsonl"))
		if err == nil && json.Valid(b) && len(events) > 0 && loadWatchSettings(a.dir).SuggestSessionNames {
			var receipt watchReceipt
			if err = json.Unmarshal(b, &receipt); err != nil || receipt.Started != "fixture" {
				t.Fatal("receipt identity lost")
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("background output was not persisted")
}

func TestWatchStopPollingKeepsOldRunTokenInert(t *testing.T) {
	a := &watchApp{dir: t.TempDir(), started: "current-run", stop: make(chan struct{})}
	if err := os.WriteFile(filepath.Join(a.dir, "stop"), []byte("old-run"), 0600); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { defer close(done); a.monitorStop() }()
	defer func() { close(a.stop); <-done }()
	time.Sleep(550 * time.Millisecond)
	if a.exitRequested.Load() {
		t.Fatal("old stop token stopped new run")
	}
	if err := os.WriteFile(filepath.Join(a.dir, "stop"), []byte(a.started), 0600); err != nil {
		t.Fatal(err)
	}
	until := time.Now().Add(time.Second)
	for !a.exitRequested.Load() && time.Now().Before(until) {
		time.Sleep(10 * time.Millisecond)
	}
	if !a.exitRequested.Load() {
		t.Fatal("current stop token ignored")
	}
}
