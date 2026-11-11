package main

import (
	"fmt"
	"runtime"
	"syscall"
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
