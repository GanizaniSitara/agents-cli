package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unsafe"

	"golang.org/x/sys/windows"
)

func watchPanelRenderKey(rows []watchEntry, summaries []watchPanelSummary, offset, capacity int, desktop string, stopped, unknown int, tick int64) string {
	// Include every painted value and target identity, not just state/PID.
	// Screen hashes are not painted; animation alone must remain cheap.
	paintRows := append([]watchEntry(nil), rows...)
	for i := range paintRows {
		paintRows[i].Sample.Signature = ""
	}
	b, _ := json.Marshal(struct {
		Rows             []watchEntry
		Summaries        []watchPanelSummary
		Offset, Capacity int
		Desktop          string
		Stopped, Unknown int
		Tick             int64
	}{paintRows, summaries, offset, capacity, desktop, stopped, unknown, tick})
	return string(b)
}

type watchPanelFrame struct {
	Rows       []watchEntry
	Summaries  []watchPanelSummary
	Offset     int
	Menu       bool
	Generation uint64
	PanelTop   int32
}

type watchPanelPress struct {
	Active     bool
	Kind       string
	X, Y       int
	Screen     watchPoint
	RowIndex   int
	Row        watchEntry
	Summary    watchPanelSummary
	Time       time.Time
	Generation uint64
	PanelTop   int32
}

func newWatchPanelFrame(rows []watchEntry, summaries []watchPanelSummary, offset, capacity int, menu bool) watchPanelFrame {
	if offset < 0 || offset > len(rows) {
		offset = 0
	}
	end := offset + capacity
	if end > len(rows) {
		end = len(rows)
	}
	return watchPanelFrame{Rows: append([]watchEntry(nil), rows[offset:end]...), Summaries: append([]watchPanelSummary(nil), summaries...), Offset: offset, Menu: menu}
}

func (f watchPanelFrame) hit(x, y int) watchPanelPress {
	p := watchPanelPress{X: x, Y: y, RowIndex: -1, Generation: f.Generation, PanelTop: f.PanelTop}
	if x < 0 || x >= 360 || y < 0 {
		return p
	}
	switch {
	case y < 34:
		p.Kind = "header"
	case f.Menu:
		p.Kind = "menu"
	case (y-34)/watchRowHeight < len(f.Rows):
		i := (y - 34) / watchRowHeight
		p.Kind, p.Row, p.RowIndex = "row", f.Rows[i], f.Offset+i
	default:
		if i := watchPanelSummaryIndex(y, len(f.Rows), len(f.Rows), len(f.Summaries)); i >= 0 {
			p.Kind, p.Summary = "summary", f.Summaries[i]
		}
	}
	p.Active = p.Kind != ""
	return p
}

func watchRowLabel(s watchSample) string { return firstNonEmpty(s.Name, s.Title, fmt.Sprint(s.PID)) }

type watchTargetState struct {
	Exists, RootAlive bool
	RootStarted       time.Time
	WindowPID         uint32
	WindowStarted     time.Time
	WindowTitle       string
}

func watchWindowTitle(hwnd uintptr) string {
	var title [1024]uint16
	watchUser.NewProc("GetWindowTextW").Call(hwnd, uintptr(unsafe.Pointer(&title[0])), uintptr(len(title)))
	return windows.UTF16ToString(title[:])
}

func watchWindowPID(hwnd uintptr) uint32 {
	var pid uint32
	watchUser.NewProc("GetWindowThreadProcessId").Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	return pid
}

func watchReadTarget(s watchSample) watchTargetState {
	exists, _, _ := watchUser.NewProc("IsWindow").Call(s.HWND)
	pid := watchWindowPID(s.HWND)
	return watchTargetState{Exists: exists != 0, RootAlive: isAlive(s.PID), RootStarted: startTime(s.PID), WindowPID: pid, WindowStarted: startTime(pid), WindowTitle: watchWindowTitle(s.HWND)}
}

func watchComparableTitle(s string) string {
	return strings.TrimSpace(strings.TrimLeftFunc(s, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && !strings.ContainsRune(`/\:.`, r)
	}))
}

func watchTargetProblem(s watchSample, state watchTargetState) string {
	if !state.Exists {
		return "window-closed"
	}
	if !state.RootAlive || state.RootStarted.IsZero() || s.RootStarted == "" || state.RootStarted.Format(time.RFC3339Nano) != s.RootStarted {
		return "root-process-changed"
	}
	if state.WindowPID == 0 || state.WindowPID != s.WindowPID || state.WindowStarted.IsZero() || state.WindowStarted.Format(time.RFC3339Nano) != s.WindowStarted {
		return "window-owner-changed"
	}
	return ""
}

func watchSameClickSession(pressed, current watchSample) bool {
	return pressed.PID == current.PID && pressed.RootStarted == current.RootStarted && pressed.HWND == current.HWND && pressed.AgentPID == current.AgentPID
}

type watchClickRecord struct {
	Time                   time.Time `json:"time"`
	PressTime              time.Time `json:"press_time"`
	Y                      int       `json:"y"`
	ReleaseY               int       `json:"release_y"`
	ScreenY                int32     `json:"screen_y"`
	RowIndex               int       `json:"row_index"`
	Label                  string    `json:"label"`
	PID                    uint32    `json:"pid"`
	HWND                   uintptr   `json:"hwnd"`
	WindowTitle            string    `json:"window_title"`
	SampleTitle            string    `json:"sample_title"`
	CaptionMatchesSample   bool      `json:"caption_matches_sample"`
	Source                 string    `json:"source"`
	RootStarted            string    `json:"root_started"`
	LiveRootStarted        string    `json:"live_root_started"`
	WindowPID              uint32    `json:"window_pid"`
	ExpectedWindowPID      uint32    `json:"expected_window_pid"`
	WindowStarted          string    `json:"window_started"`
	ExpectedWindowStarted  string    `json:"expected_window_started"`
	Outcome                string    `json:"outcome"`
	ActivationRequested    bool      `json:"activation_requested"`
	SetForegroundSucceeded bool      `json:"set_foreground_succeeded"`
	ForegroundAfter        uintptr   `json:"foreground_after"`
	ForegroundTitle        string    `json:"foreground_title"`
	ForegroundPID          uint32    `json:"foreground_pid"`
	PaintGeneration        uint64    `json:"paint_generation"`
	PaintedPanelTop        int32     `json:"painted_panel_top"`
}

func (a *watchApp) logClick(p watchPanelPress, releaseY int, state watchTargetState, outcome string, attempted, succeeded bool) {
	foreground, _, _ := watchUser.NewProc("GetForegroundWindow").Call()
	_ = watchAppendClick(a.dir, watchClickRecord{
		Time: time.Now(), PressTime: p.Time,
		Y: p.Y, ReleaseY: releaseY, ScreenY: p.Screen.Y, RowIndex: p.RowIndex,
		Label: watchRowLabel(p.Row.Sample), PID: p.Row.Sample.PID, HWND: p.Row.Sample.HWND,
		WindowTitle: state.WindowTitle, SampleTitle: p.Row.Sample.Title,
		CaptionMatchesSample: strings.EqualFold(watchComparableTitle(p.Row.Sample.Title), watchComparableTitle(state.WindowTitle)),
		Source:               p.Row.Sample.Source,
		RootStarted:          p.Row.Sample.RootStarted, LiveRootStarted: state.RootStarted.Format(time.RFC3339Nano),
		WindowPID: state.WindowPID, ExpectedWindowPID: p.Row.Sample.WindowPID,
		WindowStarted: state.WindowStarted.Format(time.RFC3339Nano), ExpectedWindowStarted: p.Row.Sample.WindowStarted,
		Outcome: outcome, ActivationRequested: attempted, SetForegroundSucceeded: succeeded,
		ForegroundAfter: foreground, ForegroundTitle: watchWindowTitle(foreground), ForegroundPID: watchWindowPID(foreground),
		PaintGeneration: p.Generation, PaintedPanelTop: p.PanelTop,
	})
}

const watchClickLogLimit = 256 * 1024

func watchAppendClick(dir string, record watchClickRecord) error {
	bound := func(s string) string {
		r := []rune(s)
		if len(r) > 512 {
			r = r[:512]
		}
		return string(r)
	}
	record.Label, record.WindowTitle, record.SampleTitle, record.ForegroundTitle = bound(record.Label), bound(record.WindowTitle), bound(record.SampleTitle), bound(record.ForegroundTitle)
	b, err := json.Marshal(record)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	path := filepath.Join(dir, "clicks.jsonl")
	if info, err := os.Stat(path); err == nil && info.Size()+int64(len(b)) > watchClickLogLimit {
		previous := filepath.Join(dir, "clicks.previous.jsonl")
		_ = os.Remove(previous)
		if err := os.Rename(path, previous); err != nil {
			return err
		}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(b)
	return err
}
