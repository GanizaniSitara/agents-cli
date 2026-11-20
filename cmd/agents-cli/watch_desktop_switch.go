package main

import (
	"errors"
	"time"
	"unsafe"
)

// INPUT's union is sized and aligned by MOUSEINPUT on both Win32 and Win64.
type switchInput struct {
	kind uint32
	data struct {
		x, y                    int32
		mouseData, flags, stamp uint32
		extra                   uintptr
	}
}

func desktopStep(list []watchDesktop, from, to string) (int, bool) {
	a, b := -1, -1
	for i, d := range list {
		if d.ID == from {
			a = i
		}
		if d.ID == to {
			b = i
		}
	}
	if a < 0 || b < 0 {
		return 0, false
	}
	return b - a, true
}

// Called only by a detached helper for an explicit panel click. Verify each
// adjacent desktop before sending another chord if the destination is farther away.
func switchClickedDesktop(target string) error {
	for attempts := 0; attempts < 32; attempts++ {
		list := watchDesktops()
		from := watchCurrentDesktop()
		step, ok := desktopStep(list, from, target)
		if !ok {
			return errors.New("desktop is no longer available")
		}
		if step == 0 {
			return nil
		}
		arrow := uint16(0x27)
		if step < 0 {
			arrow = 0x25
		}
		keys := []uint16{0x5b, 0x11, arrow, arrow, 0x11, 0x5b}
		for _, key := range []uintptr{0x10, 0x11, 0x12, 0x5b, 0x5c} {
			down, _, _ := watchUser.NewProc("GetAsyncKeyState").Call(key)
			if down&0x8000 != 0 {
				return errors.New("release shortcut modifiers and click again")
			}
		}
		var input [6]switchInput
		for i, key := range keys {
			input[i].kind = 1
			*(*uint16)(unsafe.Pointer(&input[i].data)) = key
			flags := (*uint32)(unsafe.Add(unsafe.Pointer(&input[i].data), 4))
			if key != 0x11 {
				*flags = 1
			}
			if i >= 3 {
				*flags |= 2
			}
		}
		n, _, _ := watchUser.NewProc("SendInput").Call(6, uintptr(unsafe.Pointer(&input[0])), unsafe.Sizeof(input[0]))
		if n != 6 {
			return errors.New("Windows rejected desktop shortcut")
		}
		expected := from
		for i, d := range list {
			if d.ID == from {
				if step > 0 {
					expected = list[i+1].ID
				} else {
					expected = list[i-1].ID
				}
				break
			}
		}
		until := time.Now().Add(2 * time.Second)
		for {
			now := watchCurrentDesktop()
			if now == expected {
				break
			}
			if now != from && now != "" || time.Now().After(until) {
				return errors.New("desktop switch unconfirmed")
			}
			time.Sleep(60 * time.Millisecond)
		}
	}
	return errors.New("desktop switch limit exceeded")
}
