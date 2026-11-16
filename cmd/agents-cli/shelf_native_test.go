package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const shelfFixtureID = "12345678-1234-5678-1234-123456789abc"

func shelfFixtureMain() bool {
	if os.Getenv("AGENTS_CLI_TEST_SHELF") != "1" {
		return false
	}
	if len(os.Args) > 3 && os.Args[1] == "_attach" && os.Args[2] == "shelf-fixture-window" {
		pid, _ := strconv.ParseUint(os.Args[3], 10, 32)
		if err := attach(uint32(pid)); err != nil {
			os.Exit(2)
		}
		h, _, _ := k32.NewProc("GetConsoleWindow").Call()
		emit(map[string]uintptr{"hwnd": h})
		os.Exit(0)
	}
	if len(os.Args) > 1 && (os.Args[1] == "_shelf-fixture" || os.Args[1] == "--resume") {
		if os.Args[1] == "--resume" && (len(os.Args) < 3 || os.Args[2] != shelfFixtureID) {
			os.Exit(42)
		}
		pid := uint32(os.Getpid())
		started := startTime(pid)
		dir := os.Getenv("CLAUDE_CONFIG_DIR")
		_ = os.MkdirAll(filepath.Join(dir, "sessions"), 0700)
		cwd, _ := os.Getwd()
		b, _ := json.Marshal(map[string]any{"sessionId": shelfFixtureID, "procStart": strconv.FormatInt(started.UnixNano()/100+116444736000000000, 10), "cwd": cwd})
		_ = os.WriteFile(filepath.Join(dir, "sessions", fmt.Sprint(pid)+".json"), b, 0600)
		out, err := os.OpenFile("CONOUT$", os.O_WRONLY, 0)
		if err != nil {
			os.Exit(2)
		}
		k32.NewProc("SetConsoleTitleW").Call(uintptr(unsafe.Pointer(windows.StringToUTF16Ptr("Shelf test fixture"))))
		k32.NewProc("SetConsoleOutputCP").Call(65001)
		_, _ = out.WriteString("Claude Code\r\n────────────────────────\r\n❯ \r\n────────────────────────\r\n? for shortcuts\r\n")
		in, err := os.OpenFile("CONIN$", os.O_RDWR, 0)
		if err != nil {
			os.Exit(2)
		}
		var mode uint32
		_ = windows.GetConsoleMode(windows.Handle(in.Fd()), &mode)
		_ = windows.SetConsoleMode(windows.Handle(in.Fd()), mode|windows.ENABLE_LINE_INPUT|windows.ENABLE_ECHO_INPUT|windows.ENABLE_PROCESSED_INPUT)
		reader := bufio.NewReader(in)
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				os.Exit(0)
			}
			if strings.TrimSpace(line) == "/exit" {
				os.Exit(0)
			}
		}
	}
	if len(os.Args) > 1 && (os.Args[1] == "_attach" || os.Args[1] == "send" || os.Args[1] == "key") {
		main()
		os.Exit(0)
	}
	return false
}

func TestShelfNativeSaveCloseResumeRoundTrip(t *testing.T) {
	for _, host := range []bool{false, true} {
		t.Run(fmt.Sprint("cmd-host-", host), func(t *testing.T) { testShelfNativeRoundTrip(t, host) })
	}
}

func testShelfNativeRoundTrip(t *testing.T, host bool) {
	t.Setenv("AGENTS_CLI_TEST_SHELF", "1")
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(t.TempDir(), "claude-profile"))
	t.Setenv("APPDATA", t.TempDir())
	t.Setenv("LOCALAPPDATA", t.TempDir())
	exe, _ := os.Executable()
	cmd := exec.Command(exe, "_shelf-fixture")
	if host {
		cmd = exec.Command("cmd.exe", "/d", "/k", exe, "_shelf-fixture")
	}
	cmd.Dir = t.TempDir()
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_CONSOLE, HideWindow: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()
	pid := uint32(cmd.Process.Pid)
	agentPID := pid
	if host {
		until := time.Now().Add(3 * time.Second)
		agentPID = 0
		for time.Now().Before(until) {
			m, _ := snapshot()
			for _, child := range m.descendants(pid) {
				if strings.EqualFold(m[child].Name, filepath.Base(exe)) {
					agentPID = child
					break
				}
			}
			if agentPID != 0 {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		if agentPID == 0 {
			t.Fatal("fixture agent was not launched by host shell")
		}
		agentHandle, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, agentPID)
		if err != nil {
			t.Fatal(err)
		}
		defer windows.CloseHandle(agentHandle)
		defer windows.TerminateProcess(agentHandle, 0)
	}
	var sample watchSample
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		p, err := peek(pid, 0)
		if err == nil && p.Title == "Shelf test fixture" && watchRenameComposerEmpty("claude", p.Lines) {
			started := startTime(pid).Format(time.RFC3339Nano)
			// Read the real console HWND through a detached fixture helper.
			handle := shelfFixtureWindow(pid)
			if handle == 0 {
				time.Sleep(20 * time.Millisecond)
				continue
			}
			if watchWindowTitle(handle) != p.Title {
				time.Sleep(20 * time.Millisecond)
				continue
			}
			wp := watchWindowPID(handle)
			sample = watchSample{Session: Session{PID: pid, AgentPID: agentPID, Tool: "claude", Title: p.Title, Started: startTime(pid).Format(time.RFC3339)}, HWND: handle, RootStarted: started, NamingAgentStarted: startTime(agentPID).Format(time.RFC3339Nano), WindowPID: wp, WindowStarted: startTime(wp).Format(time.RFC3339Nano)}
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if sample.PID == 0 {
		p, err := peek(pid, 0)
		t.Fatalf("fixture console did not become ready: %+v %v", p, err)
	}
	if err := writeAdopted(map[string]adopted{fmt.Sprint(pid): {Tool: "claude", Name: "Shelf test fixture", Start: startTime(pid).Format(time.RFC3339)}}); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	foreground, _, _ := watchUser.NewProc("GetForegroundWindow").Call()
	if err := shelfSaveClose(dir, sample); err != nil {
		t.Fatal(err)
	}
	if isAlive(pid) {
		t.Fatal("fixture did not close gracefully")
	}
	entries, err := shelfRead(dir)
	if err != nil || len(entries) != 1 || entries[0].State != "shelved" || entries[0].Conversation != shelfFixtureID {
		t.Fatalf("bad shelf: %+v %v", entries, err)
	}
	if entries[0].Directory != cmd.Dir {
		t.Fatal("working folder was not saved")
	}
	if err = shelfResume(dir, entries[0].ID); err != nil {
		t.Fatal(err)
	}
	entries, _ = shelfRead(dir)
	restored := entries[0]
	defer func() {
		if p, err := os.FindProcess(int(restored.ResumedPID)); err == nil {
			p.Kill()
		}
	}()
	if !shelfAlive(restored.ResumedPID, restored.ResumedStart) {
		t.Fatal("resumed fixture is not alive")
	}
	if err = shelfResume(dir, restored.ID); err == nil {
		t.Fatal("repeated Resume created duplicate")
	}
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(filepath.Join(os.Getenv("CLAUDE_CONFIG_DIR"), "sessions", fmt.Sprint(restored.ResumedPID)+".json")); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if got := envVar(restored.ResumedPID, "CLAUDE_CONFIG_DIR"); got != os.Getenv("CLAUDE_CONFIG_DIR") {
		t.Fatal("profile lost")
	}
	if got := shelfWorkingDirectory(restored.ResumedPID); filepath.Clean(got) != filepath.Clean(cmd.Dir) {
		t.Fatalf("restored directory %q", got)
	}
	if len(watchDesktops()) >= 2 {
		b, _ := json.Marshal(restored)
		out, err := runHelperInput(string(b), "shelf-present", fmt.Sprint(restored.ResumedPID))
		if err == nil && len(out) > 0 {
			err = helperErr(out)
		}
		if err != nil {
			t.Fatalf("present resumed fixture: %v output=%q", err, out)
		}
		time.Sleep(100 * time.Millisecond)
	}
	after, _, _ := watchUser.NewProc("GetForegroundWindow").Call()
	if after != foreground {
		t.Fatal("shelf/resume stole foreground focus")
	}
	t.Log("real hidden console saved current PID/session identity, gracefully exited, resumed same ID/profile/folder, duplicate refused; no owner session touched")
}

func shelfFixtureWindow(pid uint32) uintptr {
	// EnumWindows is read-only and excludes unrelated processes by PID.
	var result uintptr
	callback := syscall.NewCallback(func(h, lp uintptr) uintptr {
		if watchWindowPID(h) == pid {
			result = h
			return 0
		}
		return 1
	})
	watchUser.NewProc("EnumWindows").Call(callback, 0)
	if result != 0 {
		return result
	}
	// Conhost owns the console HWND; the fixture's private PID map is not enough.
	return shelfConsoleHWND(pid)
}

func shelfConsoleHWND(pid uint32) uintptr {
	b, err := runHelper("shelf-fixture-window", fmt.Sprint(pid))
	if err != nil {
		return 0
	}
	var r struct {
		HWND uintptr `json:"hwnd"`
	}
	_ = json.Unmarshal(b, &r)
	return r.HWND
}
