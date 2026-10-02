package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// ---- parent side: run the hidden helper -----------------------------------

type peekResult struct {
	Title string   `json:"title"`
	Lines []string `json:"lines"`
	Width int      `json:"width"`
	Error string   `json:"error,omitempty"`
}

// runHelper re-executes this binary as a detached child so it can attach to
// the target console without disturbing the caller's own console.
func runHelper(args ...string) ([]byte, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, append([]string{"_attach"}, args...)...)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: windows.DETACHED_PROCESS,
		HideWindow:    true,
	}
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && len(out) > 0 {
			return out, nil // helper reported a structured error on stdout
		}
		return nil, fmt.Errorf("console helper: %v", err)
	}
	return out, nil
}

func peek(pid uint32, above int) (*peekResult, error) {
	out, err := runHelper("peek", strconv.FormatUint(uint64(pid), 10), strconv.Itoa(above))
	if err != nil {
		return nil, err
	}
	var r peekResult
	if err := json.Unmarshal(out, &r); err != nil {
		return nil, fmt.Errorf("console helper returned %q", strings.TrimSpace(string(out)))
	}
	if r.Error != "" {
		return &r, errors.New(r.Error)
	}
	return &r, nil
}

func inject(pid uint32, text string, enter bool) error {
	e := "0"
	if enter {
		e = "1"
	}
	out, err := runHelper("inject", strconv.FormatUint(uint64(pid), 10), e, text)
	if err != nil {
		return err
	}
	return helperErr(out)
}

func sendKey(pid uint32, key string) error {
	out, err := runHelper("key", strconv.FormatUint(uint64(pid), 10), key)
	if err != nil {
		return err
	}
	return helperErr(out)
}

func helperErr(out []byte) error {
	var r struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(out, &r); err != nil {
		return fmt.Errorf("console helper returned %q", strings.TrimSpace(string(out)))
	}
	if r.Error != "" {
		return errors.New(r.Error)
	}
	return nil
}

// ---- child side: attached to the target console ----------------------------

var (
	k32                          = windows.NewLazySystemDLL("kernel32.dll")
	pAttachConsole               = k32.NewProc("AttachConsole")
	pFreeConsole                 = k32.NewProc("FreeConsole")
	pGetConsoleTitleW            = k32.NewProc("GetConsoleTitleW")
	pGetConsoleScreenBufferInfo  = k32.NewProc("GetConsoleScreenBufferInfo")
	pReadConsoleOutputCharacterW = k32.NewProc("ReadConsoleOutputCharacterW")
	pWriteConsoleInputW          = k32.NewProc("WriteConsoleInputW")
)

type coord struct{ X, Y int16 }
type smallRect struct{ Left, Top, Right, Bottom int16 }
type screenBufferInfo struct {
	Size       coord
	Cursor     coord
	Attributes uint16
	Window     smallRect
	MaxWindow  coord
}

// inputRecord is INPUT_RECORD with the KEY_EVENT_RECORD union member, 20 bytes.
type inputRecord struct {
	EventType   uint16
	_           uint16
	KeyDown     int32
	RepeatCount uint16
	VK          uint16
	Scan        uint16
	Char        uint16
	Ctrl        uint32
}

const (
	keyEvent        = 0x0001
	leftCtrlPressed = 0x0008
)

func helperMain(args []string) error {
	if len(args) < 2 {
		return errors.New("_attach needs an op and a pid")
	}
	pid64, err := strconv.ParseUint(args[1], 10, 32)
	if err != nil {
		return err
	}
	pid := uint32(pid64)
	var result interface{}
	switch args[0] {
	case "peek":
		above := 0
		if len(args) > 2 {
			above, _ = strconv.Atoi(args[2])
		}
		result = doPeek(pid, above)
	case "inject":
		if len(args) < 4 {
			return errors.New("_attach inject needs pid, enter, text")
		}
		result = errJSON(doInject(pid, args[3], args[2] == "1"))
	case "key":
		if len(args) < 3 {
			return errors.New("_attach key needs pid, key")
		}
		result = errJSON(doKey(pid, args[2]))
	default:
		return fmt.Errorf("unknown helper op %q", args[0])
	}
	encoded, _ := json.Marshal(result)
	fmt.Println(string(encoded))
	return nil
}

func errJSON(err error) map[string]string {
	if err != nil {
		return map[string]string{"error": err.Error()}
	}
	return map[string]string{}
}

func attach(pid uint32) error {
	pFreeConsole.Call()
	r, _, e := pAttachConsole.Call(uintptr(pid))
	if r == 0 {
		return fmt.Errorf("AttachConsole(%d): %v", pid, e)
	}
	return nil
}

func openConsole(name string) (windows.Handle, error) {
	p, _ := windows.UTF16PtrFromString(name)
	h, err := windows.CreateFile(p, windows.GENERIC_READ|windows.GENERIC_WRITE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		return 0, fmt.Errorf("open %s: %v", name, err)
	}
	return h, nil
}

func doPeek(pid uint32, above int) *peekResult {
	r := &peekResult{Lines: []string{}}
	if err := attach(pid); err != nil {
		r.Error = err.Error()
		return r
	}
	title := make([]uint16, 1024)
	if n, _, _ := pGetConsoleTitleW.Call(uintptr(unsafe.Pointer(&title[0])), uintptr(len(title))); n > 0 {
		r.Title = windows.UTF16ToString(title[:n])
	}
	h, err := openConsole("CONOUT$")
	if err != nil {
		r.Error = err.Error()
		return r
	}
	defer windows.CloseHandle(h)
	var info screenBufferInfo
	if ok, _, e := pGetConsoleScreenBufferInfo.Call(uintptr(h), uintptr(unsafe.Pointer(&info))); ok == 0 {
		r.Error = fmt.Sprintf("GetConsoleScreenBufferInfo: %v", e)
		return r
	}
	width := int(info.Size.X)
	if width < 1 {
		width = 1
	}
	r.Width = width
	top := int(info.Window.Top) - above
	if top < 0 {
		top = 0
	}
	buf := make([]uint16, width)
	for y := top; y <= int(info.Window.Bottom); y++ {
		var read uint32
		at := uint32(uint16(y))<<16 | uint32(uint16(0))
		if ok, _, e := pReadConsoleOutputCharacterW.Call(uintptr(h), uintptr(unsafe.Pointer(&buf[0])), uintptr(width), uintptr(at), uintptr(unsafe.Pointer(&read))); ok == 0 {
			r.Error = fmt.Sprintf("ReadConsoleOutputCharacter row %d: %v", y, e)
			return r
		}
		line := windows.UTF16ToString(buf[:read])
		r.Lines = append(r.Lines, strings.TrimRight(strings.ReplaceAll(line, "\x00", ""), " "))
	}
	return r
}

func writeInput(h windows.Handle, recs []inputRecord) error {
	if len(recs) == 0 {
		return nil
	}
	var written uint32
	ok, _, e := pWriteConsoleInputW.Call(uintptr(h), uintptr(unsafe.Pointer(&recs[0])), uintptr(len(recs)), uintptr(unsafe.Pointer(&written)))
	if ok == 0 {
		return fmt.Errorf("WriteConsoleInput: %v", e)
	}
	return nil
}

func keyPair(vk, ch uint16, ctrl uint32) []inputRecord {
	return []inputRecord{
		{EventType: keyEvent, KeyDown: 1, RepeatCount: 1, VK: vk, Char: ch, Ctrl: ctrl},
		{EventType: keyEvent, KeyDown: 0, RepeatCount: 1, VK: vk, Char: ch, Ctrl: ctrl},
	}
}

// doInject types text into the target's input buffer. The Enter goes in a
// second write after a pause: TUIs treat a fast burst as a paste and turn a
// trailing CR into a newline instead of a submit.
func doInject(pid uint32, text string, enter bool) error {
	if err := attach(pid); err != nil {
		return err
	}
	h, err := openConsole("CONIN$")
	if err != nil {
		return err
	}
	defer windows.CloseHandle(h)
	if text != "" {
		var recs []inputRecord
		for _, r := range text {
			for _, u := range utf16Units(r) {
				recs = append(recs, keyPair(0, u, 0)...)
			}
		}
		if err := writeInput(h, recs); err != nil {
			return err
		}
	}
	if enter {
		time.Sleep(150 * time.Millisecond)
		return writeInput(h, keyPair(0x0D, '\r', 0))
	}
	return nil
}

func utf16Units(r rune) []uint16 {
	if r < 0x10000 {
		return []uint16{uint16(r)}
	}
	r -= 0x10000
	return []uint16{uint16(0xD800 + (r >> 10)), uint16(0xDC00 + (r & 0x3FF))}
}

var namedKeys = map[string]struct {
	vk, ch uint16
	ctrl   uint32
}{
	"enter":     {0x0D, '\r', 0},
	"escape":    {0x1B, 0x1B, 0},
	"esc":       {0x1B, 0x1B, 0},
	"tab":       {0x09, '\t', 0},
	"backspace": {0x08, '\b', 0},
	"up":        {0x26, 0, 0},
	"down":      {0x28, 0, 0},
	"left":      {0x25, 0, 0},
	"right":     {0x27, 0, 0},
	"ctrl-c":    {0x43, 0x03, leftCtrlPressed},
	"ctrl-u":    {0x55, 0x15, leftCtrlPressed},
	"ctrl-d":    {0x44, 0x04, leftCtrlPressed},
	"ctrl-l":    {0x4C, 0x0C, leftCtrlPressed},
}

func doKey(pid uint32, name string) error {
	k, ok := namedKeys[strings.ToLower(name)]
	if !ok {
		return fmt.Errorf("unknown key %q", name)
	}
	if err := attach(pid); err != nil {
		return err
	}
	h, err := openConsole("CONIN$")
	if err != nil {
		return err
	}
	defer windows.CloseHandle(h)
	return writeInput(h, keyPair(k.vk, k.ch, k.ctrl))
}
