package main

import (
	"errors"
	"fmt"
	"runtime"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

type watchDesktop struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type watchDesktopState struct {
	Current  string
	Desktops []watchDesktop
}

// The reader is the only process that polls desktop registry state. UI and
// receipt paths use its immutable last result, even during reader recovery.
func (a *watchApp) currentDesktop() string {
	if state := a.desktop.Load(); state != nil {
		return state.Current
	}
	return ""
}

func (a *watchApp) desktopList() []watchDesktop {
	if state := a.desktop.Load(); state != nil {
		return state.Desktops
	}
	return nil
}

func (a *watchApp) acceptDesktopState(batch watchBatch) {
	if batch.Desktops != nil && batch.CurrentDesktop != "" {
		a.desktop.Store(&watchDesktopState{Current: batch.CurrentDesktop, Desktops: append([]watchDesktop(nil), batch.Desktops...)})
	}
}

func watchCurrentDesktop() string {
	var session uint32
	k32.NewProc("ProcessIdToSessionId").Call(uintptr(windows.GetCurrentProcessId()), uintptr(unsafe.Pointer(&session)))
	paths := []string{
		fmt.Sprintf(`Software\Microsoft\Windows\CurrentVersion\Explorer\SessionInfo\%d\VirtualDesktops`, session),
		`Software\Microsoft\Windows\CurrentVersion\Explorer\VirtualDesktops`,
	}
	for _, path := range paths {
		key, err := registry.OpenKey(registry.CURRENT_USER, path, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		b, _, err := key.GetBinaryValue("CurrentVirtualDesktop")
		key.Close()
		if err == nil && len(b) == 16 {
			var id windows.GUID
			copy(unsafe.Slice((*byte)(unsafe.Pointer(&id)), 16), b)
			return id.String()
		}
	}
	if desktops := watchDesktops(); len(desktops) == 1 {
		return desktops[0].ID
	}
	// Explorer may omit CurrentVirtualDesktop after sign-in even though its
	// desktop list and the public window-desktop APIs are available.
	manager, release := openWatchDesktopManager()
	defer release()
	if manager != nil {
		foreground, _, _ := watchUser.NewProc("GetForegroundWindow").Call()
		return watchVerifiedCurrentDesktop(foreground, watchDesktops(), func(hwnd uintptr) (string, bool) {
			var current int32
			hr, _, _ := syscall.SyscallN(manager.VTable[3], uintptr(unsafe.Pointer(manager)), hwnd, uintptr(unsafe.Pointer(&current)))
			if int32(hr) < 0 || current == 0 {
				return "", false
			}
			id, _ := windowWatchDesktop(manager, hwnd, nil)
			return id, true
		})
	}
	return ""
}

func watchVerifiedCurrentDesktop(hwnd uintptr, desktops []watchDesktop, query func(uintptr) (string, bool)) string {
	if hwnd == 0 {
		return ""
	}
	id, current := query(hwnd)
	if !current {
		return ""
	}
	for _, d := range desktops {
		if id == d.ID {
			return id
		}
	}
	return ""
}

// Explorer's ordered GUID list supplies the same numbering as Task View.
// No shell/window movement is involved in this read-only lookup.
func watchDesktops() []watchDesktop {
	single := []watchDesktop{{ID: "single-desktop", Name: "Desktop 1"}}
	key, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Explorer\VirtualDesktops`, registry.QUERY_VALUE)
	if err != nil {
		return single
	}
	defer key.Close()
	b, _, err := key.GetBinaryValue("VirtualDesktopIDs")
	if err != nil {
		return single
	}
	var out []watchDesktop
	for i := 0; i+16 <= len(b); i += 16 {
		var id windows.GUID
		copy(unsafe.Slice((*byte)(unsafe.Pointer(&id)), 16), b[i:i+16])
		out = append(out, watchDesktop{ID: id.String(), Name: fmt.Sprintf("Desktop %d", i/16+1)})
	}
	if len(out) == 0 {
		return single
	}
	return out
}

func (s watchSettings) includedDesktops() map[string]bool {
	included := map[string]bool{}
	for _, id := range s.IncludedDesktops {
		included[id] = true
	}
	return included
}

func (s *watchSettings) toggleDesktop(id string) {
	for i, included := range s.IncludedDesktops {
		if included == id {
			s.IncludedDesktops = append(s.IncludedDesktops[:i], s.IncludedDesktops[i+1:]...)
			return
		}
	}
	s.IncludedDesktops = append(s.IncludedDesktops, id)
}

func watchDesktopSelectable(sample, current string) bool {
	return sample != "" && current != "" && sample == current
}

// An explicit selection (row click, numbered shortcut, Ctrl+Win+Z) of a row on
// another desktop pulls its window onto the owner's current desktop first.
// Background discovery and monitoring never pull.
func watchDesktopPullNeeded(sample, current string) bool {
	return sample != "" && current != "" && sample != current
}

// watchPullAndForeground runs the explicit-selection sequence: an other-desktop
// window is moved to the current desktop first, then foregrounded. It reports
// whether the window was pulled and whether activation proceeded at all.
func watchPullAndForeground(hwnd uintptr, sampleDesktop, current string, move func(hwnd uintptr, desktopID string) error, foreground func(hwnd uintptr) bool) (pulled, proceeded bool) {
	if watchDesktopPullNeeded(sampleDesktop, current) {
		if err := move(hwnd, current); err != nil {
			return false, false
		}
		pulled = true
	}
	foreground(hwnd)
	return pulled, true
}

// ImmersiveShell COM for moving another process's window across virtual
// desktops. The public IVirtualDesktopManager::MoveWindowToDesktop returns
// E_ACCESSDENIED for foreign windows, so this goes through
// IApplicationViewCollection::GetViewForHwnd +
// IVirtualDesktopManagerInternal::MoveViewToDesktop. GUIDs and vtable order
// are the working Win10 19045 set ported from C:\scripts\move-to-desktop2.ps1.
type watchShellCOM struct{ VTable *[16]uintptr }

func watchGUID(s string) windows.GUID {
	if len(s) == 0 || s[0] != '{' {
		s = "{" + s + "}"
	}
	g, err := windows.GUIDFromString(s)
	if err != nil {
		return windows.GUID{}
	}
	return g
}

var (
	watchClsidImmersiveShell              = watchGUID("C2F03A33-21F5-47FA-B4BB-156362A2F239")
	watchIIDIServiceProvider10            = watchGUID("6D5140C1-7436-11CE-8034-00AA006009FA")
	watchIIDApplicationViewCollection     = watchGUID("1841C6D7-4F9D-42C0-AF41-8747538F10E5")
	watchSvcVirtualDesktopManagerInternal = watchGUID("C5E0CDCA-7B6E-41B2-9FC4-D93975CC467B")
	watchIIDVirtualDesktopManagerInternal = watchGUID("F31574D6-B682-4CDC-BD56-1827860ABEC6")
)

func watchCOMRelease(obj *watchShellCOM) {
	if obj != nil && obj.VTable != nil {
		syscall.SyscallN(obj.VTable[2], uintptr(unsafe.Pointer(obj)))
	}
}

func watchQueryShellService(shell *watchShellCOM, service, iid windows.GUID) (*watchShellCOM, error) {
	var obj *watchShellCOM
	hr, _, _ := syscall.SyscallN(shell.VTable[3], uintptr(unsafe.Pointer(shell)),
		uintptr(unsafe.Pointer(&service)), uintptr(unsafe.Pointer(&iid)), uintptr(unsafe.Pointer(&obj)))
	if int32(hr) < 0 || obj == nil {
		return nil, fmt.Errorf("QueryService %s failed: %#x", iid.String(), uint32(hr))
	}
	return obj, nil
}

// moveWatchWindowToDesktop moves hwnd onto the desktop identified by desktopID
// (a GUID string from the Explorer VirtualDesktops list). It must run on a
// thread that initialized COM; like the reader worker, it locks its OS thread
// so a hung Explorer COM server cannot stall the caller's thread.
func moveWatchWindowToDesktop(hwnd uintptr, desktopID string) error {
	target := watchGUID(desktopID)
	if target == (windows.GUID{}) {
		return errors.New("invalid target desktop id")
	}
	if hwnd == 0 {
		return errors.New("no window to move")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	ole := windows.NewLazySystemDLL("ole32.dll")
	// COINIT_APARTMENTTHREADED: the Immersive Shell LocalServer only activates
	// from an STA thread (MTA returns REGDB_E_CLASSNOTREG for its CLSID).
	const coinitApartmentThreaded = 2
	hr, _, _ := ole.NewProc("CoInitializeEx").Call(0, coinitApartmentThreaded)
	if int32(hr) < 0 {
		return fmt.Errorf("CoInitializeEx failed: %#x", uint32(hr))
	}
	defer ole.NewProc("CoUninitialize").Call()
	var shell *watchShellCOM
	// CLSCTX_ALL: the Immersive Shell CLSID is a LocalServer (AppID), not an
	// InprocServer, so CLSCTX_INPROC_SERVER alone returns REGDB_E_CLASSNOTREG.
	const clsctxAll = 1 | 2 | 4 | 16
	hr, _, _ = ole.NewProc("CoCreateInstance").Call(uintptr(unsafe.Pointer(&watchClsidImmersiveShell)), 0, clsctxAll,
		uintptr(unsafe.Pointer(&watchIIDIServiceProvider10)), uintptr(unsafe.Pointer(&shell)))
	if int32(hr) < 0 || shell == nil {
		return fmt.Errorf("ImmersiveShell CoCreateInstance failed: %#x", uint32(hr))
	}
	defer watchCOMRelease(shell)
	views, err := watchQueryShellService(shell, watchIIDApplicationViewCollection, watchIIDApplicationViewCollection)
	if err != nil {
		return err
	}
	defer watchCOMRelease(views)
	manager, err := watchQueryShellService(shell, watchSvcVirtualDesktopManagerInternal, watchIIDVirtualDesktopManagerInternal)
	if err != nil {
		return err
	}
	defer watchCOMRelease(manager)
	var view *watchShellCOM
	hr, _, _ = syscall.SyscallN(views.VTable[6], uintptr(unsafe.Pointer(views)), hwnd, uintptr(unsafe.Pointer(&view)))
	if int32(hr) < 0 || view == nil {
		return fmt.Errorf("GetViewForHwnd failed: %#x", uint32(hr))
	}
	defer watchCOMRelease(view)
	var desktop *watchShellCOM
	hr, _, _ = syscall.SyscallN(manager.VTable[12], uintptr(unsafe.Pointer(manager)),
		uintptr(unsafe.Pointer(&target)), uintptr(unsafe.Pointer(&desktop)))
	if int32(hr) < 0 || desktop == nil {
		return fmt.Errorf("FindDesktop failed: %#x", uint32(hr))
	}
	defer watchCOMRelease(desktop)
	hr, _, _ = syscall.SyscallN(manager.VTable[4], uintptr(unsafe.Pointer(manager)),
		uintptr(unsafe.Pointer(view)), uintptr(unsafe.Pointer(desktop)))
	if int32(hr) < 0 {
		return fmt.Errorf("MoveViewToDesktop failed: %#x", uint32(hr))
	}
	return nil
}

// watchMoveWindowBounded bounds the COM move with the same deadline the reader
// worker's watchdog applies to desktop COM calls, so an unresponsive Explorer
// cannot freeze the panel on a keypress. The stuck goroutine keeps its own
// locked thread; the panel continues without it.
func watchMoveWindowBounded(hwnd uintptr, desktopID string) error {
	type moveResult struct{ err error }
	done := make(chan moveResult, 1)
	go func() {
		done <- moveResult{moveWatchWindowToDesktop(hwnd, desktopID)}
	}()
	select {
	case r := <-done:
		return r.err
	case <-time.After(watchReadDeadline):
		return errors.New("desktop move watchdog expired")
	}
}

// The public IVirtualDesktopManager is stable across Windows 10/11. Keep COM
// apartment calls on the worker's locked OS thread; the worker watchdog also
// bounds an unresponsive Explorer COM server.
type watchDesktopManager struct{ VTable *[6]uintptr }

func openWatchDesktopManager() (*watchDesktopManager, func()) {
	runtime.LockOSThread()
	ole := windows.NewLazySystemDLL("ole32.dll")
	hr, _, _ := ole.NewProc("CoInitializeEx").Call(0, 0)
	if int32(hr) < 0 {
		runtime.UnlockOSThread()
		return nil, func() {}
	}
	cleanup := func() { ole.NewProc("CoUninitialize").Call(); runtime.UnlockOSThread() }
	clsid, _ := windows.GUIDFromString("{AA509086-5CA9-4C25-8F95-589D3C07B48A}")
	iid, _ := windows.GUIDFromString("{A5CD92FF-29BE-454C-8D04-D82879FB3F1B}")
	var manager *watchDesktopManager
	hr, _, _ = ole.NewProc("CoCreateInstance").Call(uintptr(unsafe.Pointer(&clsid)), 0, 1, uintptr(unsafe.Pointer(&iid)), uintptr(unsafe.Pointer(&manager)))
	if int32(hr) < 0 {
		cleanup()
		return nil, func() {}
	}
	return manager, func() {
		syscall.SyscallN(manager.VTable[2], uintptr(unsafe.Pointer(manager)))
		cleanup()
	}
}

func windowWatchDesktop(manager *watchDesktopManager, hwnd uintptr, desktops []watchDesktop) (string, string) {
	if len(desktops) == 1 && desktops[0].ID == "single-desktop" {
		return desktops[0].ID, desktops[0].Name
	}
	if manager == nil {
		return "", "Desktop unknown"
	}
	var id windows.GUID
	hr, _, _ := syscall.SyscallN(manager.VTable[4], uintptr(unsafe.Pointer(manager)), hwnd, uintptr(unsafe.Pointer(&id)))
	if int32(hr) < 0 {
		return "", "Desktop unknown"
	}
	idString := id.String()
	for _, d := range desktops {
		if d.ID == idString {
			return idString, d.Name
		}
	}
	return idString, "Desktop unknown"
}
