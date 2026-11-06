package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestWatchRenderKeyTracksTargetAndLabel(t *testing.T) {
	row := watchEntry{Sample: watchSample{Session: Session{PID: 10, Name: "Alpha", Title: "Alpha", State: "prompt"}, HWND: 100}}
	key := func(r watchEntry) string { return watchPanelRenderKey([]watchEntry{r}, nil, 0, 6, "one", 1, 0, 10) }
	for _, field := range []string{"name", "title", "hwnd", "started", "reason"} {
		changed := row
		switch field {
		case "name":
			changed.Sample.Name = "Beta"
		case "title":
			changed.Sample.Title = "Beta"
		case "hwnd":
			changed.Sample.HWND = 200
		case "started":
			changed.Sample.Started = "new process"
		case "reason":
			changed.Sample.Reason = "approval required"
		}
		if key(changed) == key(row) {
			t.Errorf("changed %s can replace click target without repainting", field)
		}
	}
}

func TestWatchRenderKeyIgnoresScreenAnimation(t *testing.T) {
	row := watchEntry{Sample: watchSample{Session: Session{PID: 1, State: "busy"}, Signature: "spinner-one"}}
	before := watchPanelRenderKey([]watchEntry{row}, nil, 0, 6, "one", 0, 0, 10)
	row.Sample.Signature = "spinner-two"
	if watchPanelRenderKey([]watchEntry{row}, nil, 0, 6, "one", 0, 0, 10) != before {
		t.Fatal("unpainted screen animation triggers panel repaint")
	}
}

func TestWatchClickOwnHiddenWindowsAndFallbackLabel(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	var owned []uintptr
	defer func() {
		for _, hwnd := range owned {
			watchUser.NewProc("DestroyWindow").Call(hwnd)
		}
	}()
	foreground, _, _ := watchUser.NewProc("GetForegroundWindow").Call()
	create := func(title string) uintptr {
		t.Helper()
		hwnd, _, err := watchUser.NewProc("CreateWindowExW").Call(0x08000080, uintptr(unsafe.Pointer(windows.StringToUTF16Ptr("STATIC"))), uintptr(unsafe.Pointer(windows.StringToUTF16Ptr(title))), 0x80000000, 0, 0, 20, 20, 0, 0, 0, 0)
		if hwnd == 0 {
			t.Fatal(err)
		}
		owned = append(owned, hwnd)
		if visible, _, _ := watchUser.NewProc("IsWindowVisible").Call(hwnd); visible != 0 {
			t.Fatal("disposable test window became visible")
		}
		return hwnd
	}
	alpha, beta := create("TECH-264 disposable Alpha"), create("TECH-264 disposable Beta")
	pid := uint32(os.Getpid())
	started := startTime(pid).Format(time.RFC3339Nano)
	s := watchSample{Session: Session{PID: pid, Title: "TECH-264 disposable Alpha", Source: "scan"}, HWND: alpha, RootStarted: started, WindowPID: pid, WindowStarted: started}
	state := watchReadTarget(s)
	if problem := watchTargetProblem(s, state); problem != "" {
		t.Fatal(problem)
	}
	wrong := s
	wrong.HWND = beta
	if watchSameClickSession(s, wrong) || watchReadTarget(wrong).WindowTitle == s.Title {
		t.Fatal("different HWND with same owning process was not distinguished")
	}
	p := newWatchPanelFrame([]watchEntry{{Sample: s}}, nil, 0, 6, false).hit(40, 60)
	p.Time = time.Now()
	dir := t.TempDir()
	app := &watchApp{dir: dir}
	app.logClick(p, 60, state, "test-no-activation", false, false)
	b, err := os.ReadFile(filepath.Join(dir, "clicks.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var record watchClickRecord
	if err := json.Unmarshal(b, &record); err != nil {
		t.Fatal(err)
	}
	if record.Label != s.Title || record.WindowTitle != s.Title || record.PID != pid || record.HWND != alpha || record.RowIndex != 0 || record.Y != 60 || record.ForegroundAfter != foreground || record.ActivationRequested {
		t.Fatalf("wrong click trace: %+v", record)
	}
	after, _, _ := watchUser.NewProc("GetForegroundWindow").Call()
	if after != foreground {
		t.Fatal("hidden disposable-window checks changed foreground")
	}
}

func TestWatchClickLogRotationIsBounded(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "clicks.jsonl")
	if err := os.WriteFile(path, bytes.Repeat([]byte("x"), watchClickLogLimit), 0600); err != nil {
		t.Fatal(err)
	}
	if err := watchAppendClick(dir, watchClickRecord{Label: string(bytes.Repeat([]byte("long"), 1000)), Outcome: "rejected"}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"clicks.jsonl", "clicks.previous.jsonl"} {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if info.Size() > watchClickLogLimit {
			t.Fatal("click log grew beyond its bound")
		}
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var record watchClickRecord
	if err := json.Unmarshal(b, &record); err != nil {
		t.Fatal(err)
	}
	if len([]rune(record.Label)) > 512 || record.Outcome != "rejected" {
		t.Fatal("bounded record lost its diagnostic fields")
	}
}

func TestWatchPressUsesPaintedRowsThroughPromotionAndResize(t *testing.T) {
	alpha := watchEntry{Sample: watchSample{Session: Session{PID: 1, Name: "Alpha"}, HWND: 11}}
	beta := watchEntry{Sample: watchSample{Session: Session{PID: 2, Name: "Beta"}, HWND: 22}}
	painted := newWatchPanelFrame([]watchEntry{alpha, beta}, nil, 0, 6, false)
	painted.PanelTop = 1056
	press := painted.hit(50, 78)
	// A timer promotes Beta, and bottom anchoring shifts the panel by 56px.
	// The same screen position now has release Y=22 (the header).
	live := []watchEntry{beta, alpha}
	repainted := newWatchPanelFrame(live, nil, 0, 6, false)
	repainted.PanelTop = 1112
	if repainted.hit(50, 78).Row.Sample.PID != 2 {
		t.Fatal("fixture did not promote Beta")
	}
	if !press.Active || press.Kind != "row" || press.Row.Sample.PID != 1 || press.RowIndex != 0 || press.Y != 78 {
		t.Fatal("mouse press did not retain the painted Alpha target")
	}
	live[1].Sample.Name = "replacement"
	if press.Row.Sample.Name != "Alpha" {
		t.Fatal("press aliases mutable row storage")
	}
	if painted.hit(350, 34+2*watchRowHeight+1).Active {
		t.Fatal("empty space became a row")
	}
}

func TestWatchPressDistinguishesSummariesAndScrolledRows(t *testing.T) {
	rows := []watchEntry{{Sample: watchSample{Session: Session{PID: 1}}}, {Sample: watchSample{Session: Session{PID: 2}}}}
	f := newWatchPanelFrame(rows, []watchPanelSummary{{DesktopID: "two", Toggle: true}}, 1, 1, false)
	if p := f.hit(20, 40); p.Row.Sample.PID != 2 || p.RowIndex != 1 {
		t.Fatal("scroll offset ignored")
	}
	if p := f.hit(20, 34+watchRowHeight+5); p.Kind != "summary" || p.Summary.DesktopID != "two" {
		t.Fatal("summary hit could activate a row")
	}
	if p := newWatchPanelFrame(rows, nil, 0, 6, true).hit(20, 40); p.Kind != "menu" {
		t.Fatal("menu became session click")
	}
}

func TestWatchTargetRejectsPIDAndWindowReuse(t *testing.T) {
	root := time.Unix(100, 100)
	owner := time.Unix(90, 200)
	s := watchSample{Session: Session{PID: 1, Title: "Alpha"}, HWND: 11, RootStarted: root.Format(time.RFC3339Nano), WindowPID: 2, WindowStarted: owner.Format(time.RFC3339Nano)}
	state := watchTargetState{Exists: true, RootAlive: true, RootStarted: root, WindowPID: 2, WindowStarted: owner, WindowTitle: "⠂ Alpha"}
	if reason := watchTargetProblem(s, state); reason != "" {
		t.Fatal(reason)
	}
	state.RootStarted = root.Add(100 * time.Millisecond)
	if watchTargetProblem(s, state) == "" {
		t.Fatal("same-second PID reuse accepted")
	}
	state.RootStarted = root
	state.WindowStarted = owner.Add(time.Second)
	if watchTargetProblem(s, state) == "" {
		t.Fatal("reused window owner accepted")
	}
	state.WindowStarted = owner
	state.WindowTitle = "Beta"
	if watchTargetProblem(s, state) != "" {
		t.Fatal("a mutable caption alone must not invalidate an otherwise matching window")
	}
}
