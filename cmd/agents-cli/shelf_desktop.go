package main

import (
	"fmt"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

type shelfCOM struct{ VTable *[8]uintptr }

func shelfRelease(p *shelfCOM) {
	if p != nil {
		syscall.SyscallN(p.VTable[2], uintptr(unsafe.Pointer(p)))
	}
}

// The public desktop manager refuses moving a conhost owned by another process.
// These exact, queried shell interfaces expose the common prefix used for moving
// a view. No desktop-switch or foreground methods are called. Unsupported shell
// versions return an error rather than guessing a vtable layout.
// Interface references: microsoft/TypeAgent WindowsVirtualDesktopService.cs and
// Ciantic/VirtualDesktopAccessor src/interfaces.rs; Win10 mapping also matches
// the workstation's existing move-to-desktop2.ps1.
func shelfMoveConsole(hwnd uintptr, target windows.GUID) error {
	ole := windows.NewLazySystemDLL("ole32.dll")
	clsid, _ := windows.GUIDFromString("{C2F03A33-21F5-47FA-B4BB-156362A2F239}")
	providerIID, _ := windows.GUIDFromString("{6D5140C1-7436-11CE-8034-00AA006009FA}")
	var provider *shelfCOM
	hr, _, _ := ole.NewProc("CoCreateInstance").Call(uintptr(unsafe.Pointer(&clsid)), 0, 0x17, uintptr(unsafe.Pointer(&providerIID)), uintptr(unsafe.Pointer(&provider)))
	if int32(hr) < 0 || provider == nil {
		return fmt.Errorf("Desktop shell unavailable (%x)", hr)
	}
	defer shelfRelease(provider)
	query := func(service, iface string) *shelfCOM {
		s, _ := windows.GUIDFromString(service)
		iid, _ := windows.GUIDFromString(iface)
		var result *shelfCOM
		hr, _, _ := syscall.SyscallN(provider.VTable[3], uintptr(unsafe.Pointer(provider)), uintptr(unsafe.Pointer(&s)), uintptr(unsafe.Pointer(&iid)), uintptr(unsafe.Pointer(&result)))
		if int32(hr) < 0 {
			return nil
		}
		return result
	}
	views := query("{1841C6D7-4F9D-42C0-AF41-8747538F10E5}", "{1841C6D7-4F9D-42C0-AF41-8747538F10E5}")
	if views == nil {
		return fmt.Errorf("Window view service unavailable")
	}
	defer shelfRelease(views)
	var view *shelfCOM
	hr, _, _ = syscall.SyscallN(views.VTable[6], uintptr(unsafe.Pointer(views)), hwnd, uintptr(unsafe.Pointer(&view)))
	if int32(hr) < 0 || view == nil {
		return fmt.Errorf("Resumed window view is unavailable (%x)", hr)
	}
	defer shelfRelease(view)
	service := "{C5E0CDCA-7B6E-41B2-9FC4-D93975CC467B}"
	manager := query(service, "{53F5CA0B-158F-4124-900C-057158060B27}")
	desktopIID := "{3F07F4BE-B107-441A-AF0F-39D82529072C}"
	if manager == nil {
		manager = query(service, "{F31574D6-B682-4CDC-BD56-1827860ABEC6}")
		desktopIID = "{FF72FFDD-BE7E-43FC-9C03-AD81681E88E4}"
	}
	if manager == nil {
		return fmt.Errorf("This Windows desktop shell version is not supported")
	}
	defer shelfRelease(manager)
	var array *shelfCOM
	hr, _, _ = syscall.SyscallN(manager.VTable[7], uintptr(unsafe.Pointer(manager)), uintptr(unsafe.Pointer(&array)))
	if int32(hr) < 0 || array == nil {
		return fmt.Errorf("Desktop list unavailable (%x)", hr)
	}
	defer shelfRelease(array)
	var count uint32
	hr, _, _ = syscall.SyscallN(array.VTable[3], uintptr(unsafe.Pointer(array)), uintptr(unsafe.Pointer(&count)))
	if int32(hr) < 0 || count > 64 {
		return fmt.Errorf("Invalid desktop list")
	}
	iid, _ := windows.GUIDFromString(desktopIID)
	for i := uint32(0); i < count; i++ {
		var desktop *shelfCOM
		hr, _, _ = syscall.SyscallN(array.VTable[4], uintptr(unsafe.Pointer(array)), uintptr(i), uintptr(unsafe.Pointer(&iid)), uintptr(unsafe.Pointer(&desktop)))
		if int32(hr) < 0 || desktop == nil {
			continue
		}
		var id windows.GUID
		hr, _, _ = syscall.SyscallN(desktop.VTable[4], uintptr(unsafe.Pointer(desktop)), uintptr(unsafe.Pointer(&id)))
		if int32(hr) >= 0 && id == target {
			hr, _, _ = syscall.SyscallN(manager.VTable[4], uintptr(unsafe.Pointer(manager)), uintptr(unsafe.Pointer(view)), uintptr(unsafe.Pointer(desktop)))
			shelfRelease(desktop)
			if int32(hr) < 0 {
				return fmt.Errorf("Could not place resumed window (%x)", hr)
			}
			return nil
		}
		shelfRelease(desktop)
	}
	return fmt.Errorf("Target desktop no longer exists")
}
