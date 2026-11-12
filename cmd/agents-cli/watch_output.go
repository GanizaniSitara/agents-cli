package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"golang.org/x/sys/windows"
)

// A blocked filesystem call must own neither the tracker mutex nor the panel
// thread. One writer keeps only the newest receipt/settings and 64 events.
// It never creates a goroutine per save or waits for disk from a producer.
type watchOutput struct {
	mu       sync.Mutex
	status   []byte
	settings *watchSettings
	events   [][]byte
	probe    watchUIProbe
}

func (o *watchOutput) queueStatus(b []byte)          { o.mu.Lock(); o.status = b; o.mu.Unlock() }
func (o *watchOutput) queueSettings(s watchSettings) { o.mu.Lock(); o.settings = &s; o.mu.Unlock() }
func (o *watchOutput) queueEvent(b []byte) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if len(o.events) == 64 {
		copy(o.events, o.events[1:])
		o.events = o.events[:63]
	}
	o.events = append(o.events, b)
}

func (o *watchOutput) flush(writeStatus func([]byte), writeSettings func(watchSettings), writeEvents func([][]byte)) {
	o.mu.Lock()
	b, s, events := o.status, o.settings, o.events
	o.status, o.settings, o.events = nil, nil, nil
	o.mu.Unlock()
	defer o.probe.step("idle")
	if s != nil {
		o.probe.step("settings.write")
		writeSettings(*s)
	}
	if b != nil {
		o.probe.step("status.write")
		writeStatus(b)
	}
	if len(events) > 0 {
		o.probe.step("events.write")
		writeEvents(events)
	}
}

func (a *watchApp) writeOutput() {
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	flush := func() {
		a.output.flush(func(b []byte) {
			tmp := filepath.Join(a.dir, "status.tmp")
			if err := os.WriteFile(tmp, b, 0600); err == nil {
				_ = windows.MoveFileEx(windows.StringToUTF16Ptr(tmp), windows.StringToUTF16Ptr(filepath.Join(a.dir, "status.json")), windows.MOVEFILE_REPLACE_EXISTING)
			}
		}, func(s watchSettings) { _ = saveWatchSettings(a.dir, s) }, func(events [][]byte) {
			f, err := os.OpenFile(filepath.Join(a.dir, "events.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
			if err != nil {
				return
			}
			defer f.Close()
			for _, b := range events {
				if _, err = f.Write(append(b, '\n')); err != nil {
					return
				}
			}
		})
	}
	for {
		select {
		case <-a.stop:
			flush()
			return
		case <-tick.C:
			flush()
		}
	}
}

func (a *watchApp) monitorStop() {
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-a.stop:
			return
		case <-tick.C:
			if token, err := os.ReadFile(filepath.Join(a.dir, "stop")); err == nil && string(token) == a.started {
				a.exitRequested.Store(true)
				return
			}
		}
	}
}

func (a *watchApp) currentSettings() watchSettings {
	if s := a.settings.Load(); s != nil {
		return *s
	}
	return watchSettings{FirstRowShortcut: watchDefaultFirstRowShortcut}
}

func (a *watchApp) rememberSettings(s watchSettings) {
	s.IncludedDesktops = append([]string(nil), s.IncludedDesktops...)
	a.settings.Store(&s)
}

func (a *watchApp) persistSettings(s watchSettings) {
	a.rememberSettings(s)
	if a.output != nil {
		a.output.queueSettings(a.currentSettings())
	}
}

func (a *watchApp) queueEvent(s watchSample) {
	if a.output == nil {
		return
	}
	b, _ := json.Marshal(map[string]interface{}{"time": time.Now().Format(time.RFC3339), "pid": s.PID, "name": firstNonEmpty(s.Name, s.Title, strconv.Itoa(int(s.PID))), "waiting_on": firstNonEmpty(s.Reason, watchReason(s.State))})
	a.output.queueEvent(b)
}
