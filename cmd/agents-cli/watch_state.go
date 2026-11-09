package main

import (
	"sort"
	"time"
)

type watchSample struct {
	Session
	HWND             uintptr     `json:"hwnd"`
	Signature        string      `json:"signature"`
	DesktopID        string      `json:"desktop_id"`
	Desktop          string      `json:"desktop"`
	Agents           watchAgents `json:"agents"`
	Phase            string      `json:"phase"`
	Reason           string      `json:"reason,omitempty"`
	Context          string      `json:"context,omitempty"`
	RootStarted      string      `json:"root_started"`
	WindowPID        uint32      `json:"window_pid"`
	WindowStarted    string      `json:"window_started"`
	WorkProcesses    int         `json:"work_processes,omitempty"`
	PendingQuestions int         `json:"pending_questions,omitempty"`
	Working          bool        `json:"working,omitempty"`
}

type watchEntry struct {
	Sample    watchSample `json:"window"`
	Since     time.Time   `json:"since"`
	Candidate time.Time   `json:"-"`
	Busy      bool        `json:"-"`
	Stopped   bool        `json:"-"`
	Attention bool        `json:"-"`
	Dismissed bool        `json:"-"`
	Order     uint64      `json:"-"`
	Changed   time.Time   `json:"changed"`
}

type watchTracker struct {
	stable    time.Duration
	entries   map[uint32]*watchEntry
	nextOrder uint64
}

// Focus is presentation, not proof that an agent is running. Preserve the
// underlying state so leaving the window reveals any outstanding request.
func watchActiveRows(rows []watchEntry, foreground uintptr) []watchEntry {
	result := append([]watchEntry(nil), rows...)
	for i := range result {
		s := &result[i].Sample
		if foreground != 0 && s.HWND == foreground && s.State != "busy" && s.Phase != "error" {
			s.Phase, s.Reason = "active", "active window"
			result[i].Stopped = false
		}
	}
	return result
}

func newWatchTracker(stable time.Duration) *watchTracker {
	return &watchTracker{stable: stable, entries: map[uint32]*watchEntry{}}
}

func (w *watchTracker) observe(s watchSample, now time.Time) bool {
	if s.Agents.Running != nil && *s.Agents.Running > 0 {
		s.Working = true
	}
	if s.Agents.Running != nil && *s.Agents.Running > 0 && s.PendingQuestions == 0 {
		s.State = "busy"
		if s.Phase != "error" {
			s.Phase = "working"
			s.Reason = "sub-agents running"
		}
	}
	e := w.entries[s.PID]
	if e == nil || e.Sample.Started != s.Started || e.Sample.RootStarted != s.RootStarted || e.Sample.AgentPID != s.AgentPID || e.Sample.HWND != s.HWND {
		if e != nil && e.Sample.Started == s.Started && e.Sample.RootStarted == s.RootStarted && e.Sample.HWND == s.HWND {
			// Child discovery can change without replacing the console window.
			// Reset stop tracking, but keep its place within the same group.
			e = &watchEntry{Sample: e.Sample, Order: e.Order}
		} else {
			e = &watchEntry{}
		}
		w.entries[s.PID] = e
	}
	old := e.Sample
	if e.Order == 0 || watchPanelGroup(old) != watchPanelGroup(s) {
		w.nextOrder++
		e.Order = w.nextOrder
	}
	if e.Changed.IsZero() || old.State != s.State || old.Phase != s.Phase {
		e.Changed = now
	}
	e.Sample = s
	if s.PendingQuestions > 0 {
		// A live queue is already an explicit input request, even while its
		// running footer changes. Screen stability cannot debounce this case.
		notify := old.PendingQuestions == 0
		if notify {
			e.Dismissed = false
			e.Since = now
			e.Attention = true
		}
		e.Stopped = true
		e.Busy = s.Working
		return notify
	}
	if old.PendingQuestions > 0 {
		e.Stopped, e.Attention, e.Dismissed = false, false, false
		e.Candidate = time.Time{}
	}
	if s.State == "busy" {
		e.Busy, e.Stopped = true, false
		e.Attention = false
		e.Candidate = time.Time{}
		return false
	}
	if s.State != "idle" && s.State != "prompt" && s.State != "error" {
		e.Candidate = time.Time{}
		e.Stopped = false
		e.Attention = false
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
	e.Dismissed = false
	notify := e.Busy
	e.Attention = notify
	e.Busy = false
	return notify
}

func (w *watchTracker) dismiss(pid uint32) {
	if e := w.entries[pid]; e != nil {
		e.Dismissed = true
		e.Attention = false
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
	sort.Slice(rows, func(i, j int) bool { return rows[i].Order < rows[j].Order })
	return rows
}

// Group priority changes only when the session changes group. Within a group,
// arrival order stays stable through state, age and screen-content updates.
func watchPanelGroup(s watchSample) int {
	if s.State == "error" || s.Phase == "error" {
		return 1
	}
	if s.State == "prompt" || s.Phase == "waiting" || s.Phase == "question" || s.Phase == "review" {
		return 0
	}
	if s.Phase == "finished" {
		return 2
	}
	if s.State == "busy" || s.Phase == "working" {
		return 3
	}
	return 4 // idle/check and unavailable belong below confirmed states
}

func (w *watchTracker) panelRows() []watchEntry {
	rows := []watchEntry{}
	for _, e := range w.entries {
		if e.Order > 0 && !e.Dismissed {
			rows = append(rows, *e)
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		a, b := watchPanelGroup(rows[i].Sample), watchPanelGroup(rows[j].Sample)
		if a != b {
			return a < b
		}
		return rows[i].Order < rows[j].Order
	})
	return rows
}
