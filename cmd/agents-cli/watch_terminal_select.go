package main

import (
	"context"
	"encoding/json"
	"os"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// UI Automation can block in another application's provider. Isolate it from
// both the panel thread and the sequential status reader, on explicit selection
// only. The helper never types keys, changes titles or launches a Terminal.
func watchSelectTerminal(parent context.Context, s watchSample) string {
	exe, err := os.Executable()
	if err != nil {
		return "terminal-helper-unavailable"
	}
	ctx, cancel := context.WithTimeout(parent, 2*time.Second)
	defer cancel()
	input, _ := json.Marshal(s)
	cmd := helperCommand(ctx, exe, string(input), "terminal-select", strconv.FormatUint(uint64(s.PID), 10))
	cmd.WaitDelay = 100 * time.Millisecond
	out, err := cmd.Output()
	if ctx.Err() != nil {
		return "terminal-tab-timeout"
	}
	var result struct{ Problem string }
	if err != nil || json.Unmarshal(out, &result) != nil {
		return "terminal-helper-failed"
	}
	return result.Problem
}

type watchTerminalRequest struct {
	Press      watchPanelPress
	ReleaseY   int
	Generation uint64
}

type watchTerminalResult struct {
	Request watchTerminalRequest
	Problem string
}

// Titles are not global identities. Only an exact, unique match inside the
// console's verified owner is eligible. Duplicates and renamed tabs fail closed.
func watchTerminalTabIndex(title string, names []string) (int, string) {
	if strings.TrimSpace(title) == "" {
		return -1, "terminal-tab-title-empty"
	}
	index := -1
	for i, name := range names {
		if name != title {
			continue
		}
		if index >= 0 {
			return -1, "terminal-tab-ambiguous"
		}
		index = i
	}
	if index < 0 {
		return -1, "terminal-tab-not-found"
	}
	return index, ""
}

func watchTerminalConsoleTitle() string {
	var title [2048]uint16
	n, _, _ := pGetConsoleTitleW.Call(uintptr(unsafe.Pointer(&title[0])), uintptr(len(title)))
	if n == 0 || n >= uintptr(len(title)-1) {
		return ""
	}
	return windows.UTF16ToString(title[:n])
}

func watchTerminalOwnerValid(s watchSample) bool {
	hwnd, _, _ := k32.NewProc("GetConsoleWindow").Call()
	var cloaked uint32
	hr, _, _ := watchDWM.NewProc("DwmGetWindowAttribute").Call(s.HWND, 14, uintptr(unsafe.Pointer(&cloaked)), 4)
	return int32(hr) >= 0 && cloaked == 0 && watchWindowClass(s.HWND) == "CASCADIA_HOSTING_WINDOW_CLASS" &&
		watchTargetProblem(s, watchReadTarget(s)) == "" && watchConsolePresentation(hwnd) == s.HWND
}

// The vtable slots below follow the Windows SDK UIAutomationClient.h. Using
// native UIA keeps the shipped exe independent of PowerShell or extra packages.
type watchUIAObject struct{ table *[85]uintptr }

func (o *watchUIAObject) call(slot int, args ...uintptr) bool {
	args = append([]uintptr{uintptr(unsafe.Pointer(o))}, args...)
	hr, _, _ := syscall.SyscallN(o.table[slot], args...)
	return int32(hr) >= 0
}

func (o *watchUIAObject) release() { o.call(2) }

func (o *watchUIAObject) name() (string, bool) {
	var bstr *uint16
	if !o.call(23, uintptr(unsafe.Pointer(&bstr))) {
		return "", false
	}
	if bstr == nil {
		return "", true
	}
	defer windows.NewLazySystemDLL("oleaut32.dll").NewProc("SysFreeString").Call(uintptr(unsafe.Pointer(bstr)))
	return windows.UTF16PtrToString(bstr), true
}

func watchDoSelectTerminal(s watchSample) string {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if attach(s.PID) != nil || !watchTerminalOwnerValid(s) {
		return "terminal-console-changed"
	}
	title := watchTerminalConsoleTitle()
	ole := windows.NewLazySystemDLL("ole32.dll")
	hr, _, _ := ole.NewProc("CoInitializeEx").Call(0, 0) // MTA, separate helper
	if int32(hr) < 0 {
		return "terminal-accessibility-unavailable"
	}
	defer ole.NewProc("CoUninitialize").Call()
	clsid, _ := windows.GUIDFromString("{FF48DBA4-60EF-4201-AA87-54103EEF594E}")
	iid, _ := windows.GUIDFromString("{30CBE57D-D9D0-452A-AB13-7AC5AC4825EE}")
	var automation *watchUIAObject
	hr, _, _ = ole.NewProc("CoCreateInstance").Call(uintptr(unsafe.Pointer(&clsid)), 0, 1, uintptr(unsafe.Pointer(&iid)), uintptr(unsafe.Pointer(&automation)))
	if int32(hr) < 0 || automation == nil {
		return "terminal-accessibility-unavailable"
	}
	defer automation.release()
	var window, condition, tabs *watchUIAObject
	if !automation.call(6, s.HWND, uintptr(unsafe.Pointer(&window))) || window == nil {
		return "terminal-accessibility-unavailable"
	}
	defer window.release()
	// VARIANT(VT_I4, UIA_TabItemControlTypeId). Windows x64/ARM64 pass this
	// 24-byte value by address. Fail closed on x86 rather than use its ABI.
	if unsafe.Sizeof(uintptr(0)) != 8 {
		return "terminal-accessibility-architecture"
	}
	variant := struct {
		vt      uint16
		padding [3]uint16
		value   [2]uintptr
	}{vt: 3, value: [2]uintptr{50019, 0}}
	if !automation.call(23, 30003, uintptr(unsafe.Pointer(&variant)), uintptr(unsafe.Pointer(&condition))) || condition == nil {
		return "terminal-accessibility-unavailable"
	}
	defer condition.release()
	if !window.call(6, 4, uintptr(unsafe.Pointer(condition)), uintptr(unsafe.Pointer(&tabs))) || tabs == nil { // descendants
		return "terminal-accessibility-unavailable"
	}
	defer tabs.release()
	var count int32
	if !tabs.call(3, uintptr(unsafe.Pointer(&count))) || count < 1 || count > 128 {
		return "terminal-tab-count-unavailable"
	}
	names := make([]string, count)
	elements := make([]*watchUIAObject, 0, count)
	defer func() {
		for _, element := range elements {
			element.release()
		}
	}()
	for i := range names {
		var element *watchUIAObject
		if !tabs.call(4, uintptr(i), uintptr(unsafe.Pointer(&element))) || element == nil {
			return "terminal-tab-changed"
		}
		elements = append(elements, element)
		var ok bool
		if names[i], ok = element.name(); !ok {
			return "terminal-tab-changed"
		}
	}
	index, problem := watchTerminalTabIndex(title, names)
	if problem != "" {
		return problem
	}
	element := elements[index]
	patternIID, _ := windows.GUIDFromString("{A8EFA66A-0FDA-421A-9194-38021F3578EA}")
	var pattern *watchUIAObject
	if !element.call(14, 10010, uintptr(unsafe.Pointer(&patternIID)), uintptr(unsafe.Pointer(&pattern))) || pattern == nil {
		return "terminal-tab-not-selectable"
	}
	defer pattern.release()
	name, ok := element.name()
	if !ok || name != title || watchTerminalConsoleTitle() != title || !watchTerminalOwnerValid(s) {
		return "terminal-tab-changed"
	}
	if !pattern.call(3) { // IUIAutomationSelectionItemPattern.Select
		return "terminal-tab-selection-failed"
	}
	var selected int32
	if !pattern.call(6, uintptr(unsafe.Pointer(&selected))) || selected == 0 || !watchTerminalOwnerValid(s) {
		return "terminal-tab-selection-unconfirmed"
	}
	return ""
}
