package main

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func watchNameFixtureMain() bool {
	if os.Getenv("AGENTS_CLI_TEST_NAME_HELPER") == "1" {
		if err := helperMain(os.Args[2:]); err != nil {
			emitError(err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	if os.Getenv("AGENTS_CLI_TEST_NAME_CONSOLE") != "1" {
		return false
	}
	pid := uint32(os.Getpid())
	started := startTime(pid).Format(time.RFC3339Nano)
	title := windows.StringToUTF16Ptr("codex")
	k32.NewProc("SetConsoleTitleW").Call(uintptr(unsafe.Pointer(title)))
	h, _, _ := k32.NewProc("GetConsoleWindow").Call()
	wp := watchWindowPID(h)
	_ = json.NewEncoder(os.Stdout).Encode(watchSample{Session: Session{PID: pid, AgentPID: pid, Title: "codex", Tool: "codex"}, HWND: h, RootStarted: started, WindowPID: wp, WindowStarted: startTime(wp).Format(time.RFC3339Nano), NamingAgentStarted: started})
	_, _ = io.Copy(io.Discard, os.Stdin)
	os.Exit(0)
	return true
}

func TestWatchNameDialogNearPanel(t *testing.T) {
	for _, tc := range []struct {
		name         string
		anchor, area watchRect
		want         watchPoint
	}{
		{"right", watchRect{20, 40, 380, 440}, watchRect{0, 0, 1920, 1040}, watchPoint{388, 40}},
		{"left", watchRect{1550, 40, 1910, 440}, watchRect{0, 0, 1920, 1040}, watchPoint{1022, 40}},
		{"bottom", watchRect{20, 800, 380, 1040}, watchRect{0, 0, 1920, 1040}, watchPoint{388, 770}},
		{"negative monitor", watchRect{-370, -700, -10, -300}, watchRect{-1080, -1000, 0, 920}, watchPoint{-898, -700}},
		{"narrow monitor", watchRect{40, 20, 400, 500}, watchRect{0, 0, 800, 600}, watchPoint{0, 20}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := watchNameDialogPosition(tc.anchor, tc.area, 520, 270); got != tc.want {
				t.Fatalf("got %+v want %+v", got, tc.want)
			}
		})
	}
}

func TestWatchNameSuggestionConservative(t *testing.T) {
	s := Session{Title: "Command Prompt - codex", Tool: "codex"}
	for _, tc := range []struct {
		lines []string
		want  string
	}{
		{[]string{"› investigate watcher refresh hangs", "• I will inspect the refresh."}, "Investigate watcher refresh hangs"},
		{[]string{"Assistant says investigate watcher refresh hangs"}, ""},
		{[]string{"› Ask Codex to do anything"}, ""},
		{[]string{"❯ git status --short"}, ""},
		{[]string{"› /rename this session"}, ""},
		{[]string{"> Please rename title of this window to whichever conversation", "● Ran 3 commands"}, ""},
		{[]string{"› https://private.example/path investigate this"}, ""},
	} {
		if got := watchSuggestName(s, tc.lines); got != tc.want {
			t.Fatalf("%q => %q, want %q", tc.lines, got, tc.want)
		}
	}
	for _, title := range []string{"AGENTS-CLI", "TECH-264", "Investigate refresh hangs", "Review Monzo application"} {
		s.Title = title
		if watchSuggestName(s, []string{"› replace this useful existing title"}) != "" {
			t.Fatal("overrode useful title", title)
		}
	}
	s.Title = "codex"
	s.Name = "Ticket name"
	if watchSuggestName(s, []string{"› replace ticket name please"}) != "" {
		t.Fatal("ticket name eligible")
	}
}

func TestWatchNameAGYLaunchTitle(t *testing.T) {
	lines := []string{"> prepare piano arpeggios for practice", "  continued request", "○ Thought for 3s", "● Ran lilypond", ">"}
	for _, title := range []string{"Command Prompt - agy  --dangerously-skip-permissions", "Administrator: agy.exe --dangerously-skip-permissions", "agy"} {
		s := Session{Tool: "agy", Title: title}
		if got := watchSuggestName(s, lines); got != "Prepare piano arpeggios for practice" {
			t.Fatalf("%q suggestion %q", title, got)
		}
		if got := watchSuggestName(s, []string{"> unsubmitted piano arpeggios request", "? for shortcuts"}); got != "" {
			t.Fatalf("draft suggested %q", got)
		}
	}
	for _, s := range []Session{
		{Tool: "agy", Title: "LapCasa Release & QA"},
		{Tool: "agy", Title: "agy --dangerously-skip-permissions", Name: "Existing task"},
		{Tool: "agy", Title: "agy --dangerously-skip-permissions piano practice"},
	} {
		if got := watchSuggestName(s, lines); got != "" {
			t.Fatalf("named session %+v suggested %q", s, got)
		}
	}
}

func TestWatchNameSettingsAndDecisionsPersist(t *testing.T) {
	dir := t.TempDir()
	if loadWatchSettings(dir).SuggestSessionNames {
		t.Fatal("must default off")
	}
	settings := watchSettings{SuggestSessionNames: true, FirstRowShortcut: "Ctrl+Win+Z", Shortcuts: "Ctrl+Alt+Shift"}
	if err := saveWatchSettings(dir, settings); err != nil {
		t.Fatal(err)
	}
	if got := loadWatchSettings(dir); !got.SuggestSessionNames || got.FirstRowShortcut != settings.FirstRowShortcut || got.Shortcuts != settings.Shortcuts {
		t.Fatalf("lost settings %+v", got)
	}
	s := watchSample{Session: Session{PID: 1, AgentPID: 2, Title: "codex"}, RootStarted: "root", NamingAgentStarted: "agent", SuggestedName: "Fix refresh hangs"}
	if err := watchSaveName(dir, s, watchNameRecord{Dismissed: true}); err != nil {
		t.Fatal(err)
	}
	names := watchReadNames(dir)
	watchApplyNameRecord(&s, names, true)
	if s.SuggestedName != "" {
		t.Fatal("dismissed suggestion returned")
	}
	s.AgentPID = 3
	s.SuggestedName = "Another task"
	watchApplyNameRecord(&s, names, true)
	if s.SuggestedName == "" {
		t.Fatal("dismissal leaked to replacement agent")
	}
	if err := watchSaveName(dir, s, watchNameRecord{Name: "Approved title"}); err != nil {
		t.Fatal(err)
	}
	watchApplyNameRecord(&s, watchReadNames(dir), false)
	if s.Name != "Approved title" || s.SuggestedName != "" {
		t.Fatal("disabling must retain approved label")
	}
	s.Name = "Manual override"
	watchApplyNameRecord(&s, watchReadNames(dir), true)
	if s.Name != "Manual override" {
		t.Fatal("lost manual name")
	}
}

func TestWatchNameHitIsNotActivation(t *testing.T) {
	row := watchEntry{Sample: watchSample{Session: Session{Title: "Command Prompt - agy --dangerously-skip-permissions", Tool: "agy"}, SuggestedName: "Fix refresh hangs"}}
	if got := watchRowDescription(row.Sample); got != "Suggested: Fix refresh hangs" {
		t.Fatalf("suggestion not displayed: %q", got)
	}
	f := newWatchPanelFrame([]watchEntry{row}, nil, 0, 1, false)
	if p := f.hit(100, 60); p.Kind != "name" {
		t.Fatalf("clicking displayed suggestion must review, got %s", p.Kind)
	}
	if p := f.hit(290, 74); p.Kind != "name" {
		t.Fatalf("got %s", p.Kind)
	}
	if p := f.hit(100, 74); p.Kind != "row" {
		t.Fatal("ordinary row click changed")
	}
	if p := f.hit(340, 74); p.Kind != "row" {
		t.Fatal("park click changed")
	}
}

func TestWatchNameDialogDismissAndCancelNative(t *testing.T) {
	for _, tc := range []struct {
		id   uintptr
		want string
	}{{3, "dismiss"}, {2, ""}} {
		done := make(chan struct{})
		go func(id uintptr) {
			defer close(done)
			deadline := time.Now().Add(2 * time.Second)
			for time.Now().Before(deadline) {
				h, _, _ := watchUser.NewProc("FindWindowW").Call(uintptr(unsafe.Pointer(windows.StringToUTF16Ptr("AgentsCLINameReview"))), 0)
				if h != 0 && watchWindowPID(h) == uint32(os.Getpid()) && watchDialogItem(h, 5) != 0 {
					var rect watchRect
					watchUser.NewProc("GetWindowRect").Call(h, uintptr(unsafe.Pointer(&rect)))
					want := watchNameDialogOrigin(520, 214)
					if rect.Left != want.X || rect.Top != want.Y {
						t.Errorf("native rename dialog at %+v, want %+v", rect, want)
					}
					var start, end uint32
					next, _, _ := watchUser.NewProc("GetNextDlgTabItem").Call(h, watchDialogItem(h, 10), 0)
					if next != watchDialogItem(h, 1) {
						t.Error("Tab from name must go directly to Rename")
					}
					watchUser.NewProc("SendMessageW").Call(watchDialogItem(h, 10), 0xb0, uintptr(unsafe.Pointer(&start)), uintptr(unsafe.Pointer(&end))) // EM_GETSEL
					if start != 0 || end != uint32(len("Fix refresh hangs")) {
						t.Errorf("name must start selected for overwrite: %d..%d", start, end)
					}
					checked, _, _ := watchUser.NewProc("SendMessageW").Call(watchDialogItem(h, 5), 0xf0, 0, 0)
					if checked != 1 {
						t.Error("requested native session rename not selected")
					}
					watchUser.NewProc("PostMessageW").Call(h, 0x111, id, 0)
					return
				}
				time.Sleep(10 * time.Millisecond)
			}
		}(tc.id)
		got := watchNameDialog(watchNameReview{Name: "Fix refresh hangs", Sample: watchSample{Session: Session{Tool: "agy"}}, RenameSession: true}, false)
		<-done
		if got.Action != tc.want {
			t.Fatalf("action %q want %q", got.Action, tc.want)
		}
	}
}

func TestWatchNameActualConsoleAndStaleIdentity(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	fixture := exec.CommandContext(ctx, os.Args[0])
	fixture.Env = append(os.Environ(), "AGENTS_CLI_TEST_NAME_CONSOLE=1")
	fixture.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_CONSOLE, HideWindow: true}
	stdout, _ := fixture.StdoutPipe()
	stdin, _ := fixture.StdinPipe()
	if err := fixture.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { stdin.Close(); _ = fixture.Wait() }()
	var s watchSample
	if err := json.NewDecoder(stdout).Decode(&s); err != nil {
		t.Fatal(err)
	}
	if s.HWND == 0 {
		t.Fatal("fixture has no real console")
	}
	// Conhost updates its caption asynchronously after SetConsoleTitleW.
	initialDeadline := time.Now().Add(time.Second)
	for watchWindowTitle(s.HWND) != s.Title && time.Now().Before(initialDeadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := watchWindowTitle(s.HWND); got != s.Title {
		t.Fatalf("fixture initial caption %q, want %q", got, s.Title)
	}
	dir := t.TempDir()
	if err := saveWatchSettings(dir, watchSettings{SuggestSessionNames: true}); err != nil {
		t.Fatal(err)
	}
	applyReview := func(sample watchSample, name, action string) error {
		r := watchNameReview{Dir: dir, Sample: sample, Name: name, Action: action}
		input, _ := json.Marshal(r)
		cmd := helperCommand(ctx, os.Args[0], string(input), "name-apply", strconv.FormatUint(uint64(sample.PID), 10))
		cmd.Env = append(os.Environ(), "AGENTS_CLI_TEST_NAME_HELPER=1")
		out, err := cmd.Output()
		if err != nil {
			return err
		}
		return helperErr(out)
	}
	for _, action := range []string{"", "dismiss", "cancel"} {
		if err := applyReview(s, "Unapproved title", action); err == nil || !strings.Contains(err.Error(), "explicit confirmation") {
			t.Fatalf("unapproved action %q: %v", action, err)
		}
		if got := watchWindowTitle(s.HWND); got != "codex" {
			t.Fatalf("unapproved rename changed title to %q", got)
		}
	}
	apply := func(sample watchSample, name string) error { return applyReview(sample, name, "accept") }
	stale := s
	stale.RootStarted = "reused"
	if apply(stale, "Wrong session") == nil {
		t.Fatal("accepted stale identity")
	}
	stale = s
	stale.NamingAgentStarted = "reused"
	if apply(stale, "Wrong agent") == nil {
		t.Fatal("accepted replaced agent")
	}
	if err := apply(s, "Investigate refresh hangs"); err != nil {
		t.Fatal(err)
	}
	// Check the real conhost caption, not just the watcher's persisted label.
	deadline := time.Now().Add(time.Second)
	for watchWindowTitle(s.HWND) != "Investigate refresh hangs" && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := watchWindowTitle(s.HWND); got != "Investigate refresh hangs" {
		t.Fatalf("real caption %q", got)
	}
	if err := apply(s, "Overwrite later title"); err == nil || !strings.Contains(err.Error(), "title changed") {
		t.Fatalf("old approval must fail: %v", err)
	}
	// Exercise the real editable native review, hidden so validation cannot
	// steal focus. Only its own controls receive messages.
	s.Title = "Investigate refresh hangs"
	t.Setenv("AGENTS_CLI_TEST_NAME_HELPER", "1")
	clickDone := make(chan struct{})
	go func() {
		defer close(clickDone)
		until := time.Now().Add(2 * time.Second)
		for time.Now().Before(until) {
			h, _, _ := watchUser.NewProc("FindWindowW").Call(uintptr(unsafe.Pointer(windows.StringToUTF16Ptr("AgentsCLINameReview"))), 0)
			if h != 0 && watchWindowPID(h) == uint32(os.Getpid()) {
				edit := watchDialogItem(h, 10)
				if edit != 0 && watchDialogItem(h, 3) != 0 {
					if got := watchWindowTitle(edit); got != s.Title {
						t.Errorf("editable current name %q, want %q", got, s.Title)
					}
					watchUser.NewProc("SetWindowTextW").Call(edit, uintptr(unsafe.Pointer(windows.StringToUTF16Ptr("Edited approved title"))))
					def, _, _ := watchUser.NewProc("SendMessageW").Call(h, 0x400, 0, 0)
					if def != 0x534b0001 {
						t.Errorf("Rename is not the default: %x", def)
					}
					// Send Enter through the real dialog keyboard path, not WM_COMMAND.
					watchUser.NewProc("PostMessageW").Call(edit, 0x100, 13, 1)
					return
				}
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()
	review := watchNameDialog(watchNameReview{Dir: dir, Sample: s, Name: s.Title}, false)
	<-clickDone
	if review.Action != "accept" || review.Name != "Edited approved title" {
		t.Fatalf("review %+v", review)
	}
	if watchWindowTitle(s.HWND) != "Edited approved title" {
		t.Fatal("edited approval did not change actual title")
	}
	if err := saveWatchSettings(dir, watchSettings{}); err != nil {
		t.Fatal(err)
	}
	s.Title = "Edited approved title"
	if err := apply(s, "Manual rename with suggestions off"); err != nil {
		t.Fatalf("manual rename must remain available with suggestions off: %v", err)
	}
	t.Log("real hidden conhost title changed; stale root, agent and title refused; explicit manual rename works with suggestions off; no activation")
}
