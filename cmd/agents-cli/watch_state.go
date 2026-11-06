package main

import (
	"sort"
	"time"
)

type watchSample struct {
	Session
	HWND      uintptr `json:"hwnd"`
	Signature string  `json:"signature"`
}

type watchEntry struct {
	Sample    watchSample `json:"window"`
	Since     time.Time   `json:"since"`
	Candidate time.Time   `json:"-"`
	Busy      bool        `json:"-"`
	Stopped   bool        `json:"-"`
	Dismissed bool        `json:"-"`
}

type watchTracker struct {
	stable  time.Duration
	entries map[uint32]*watchEntry
}

func newWatchTracker(stable time.Duration) *watchTracker {
	return &watchTracker{stable: stable, entries: map[uint32]*watchEntry{}}
}

func (w *watchTracker) observe(s watchSample, now time.Time) bool {
	e := w.entries[s.PID]
	if e == nil || e.Sample.Started != s.Started || e.Sample.AgentPID != s.AgentPID || e.Sample.HWND != s.HWND {
		e = &watchEntry{}
		w.entries[s.PID] = e
	}
	old := e.Sample
	e.Sample = s
	if s.State == "busy" {
		e.Busy, e.Stopped, e.Dismissed = true, false, false
		e.Candidate = time.Time{}
		return false
	}
	if s.State != "idle" && s.State != "prompt" {
		e.Candidate = time.Time{}
		e.Stopped = false
		return false
	}
	if e.Stopped {
		return false
	}
	if e.Candidate.IsZero() || old.State != s.State || old.Signature != s.Signature {
		e.Candidate = now
		return false
	}
	if now.Sub(e.Candidate) < w.stable {
		return false
	}
	e.Stopped, e.Since = true, e.Candidate
	notify := e.Busy
	e.Busy = false
	return notify
}

func (w *watchTracker) dismiss(pid uint32) {
	if e := w.entries[pid]; e != nil {
		e.Dismissed = true
	}
}

func (w *watchTracker) prune(alive map[uint32]bool) {
	for pid := range w.entries {
		if !alive[pid] {
			delete(w.entries, pid)
		}
	}
}

func (w *watchTracker) rows() []watchEntry {
	rows := []watchEntry{}
	for _, e := range w.entries {
		if e.Stopped && !e.Dismissed {
			rows = append(rows, *e)
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Since.Before(rows[j].Since) })
	return rows
}
