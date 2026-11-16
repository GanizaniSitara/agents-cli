package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"runtime"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

type watchSessionAction struct {
	Action string      `json:"action"`
	Sample watchSample `json:"sample"`
}

type watchCopyData struct {
	Tag  uintptr
	Size uint32
	Data uintptr
}

const watchSessionActionTag = 0x4147504b

// A daemon-hosted Codex tool call is not a child of its interactive console.
// Use the exact thread ID in that case, never a title or the foreground window.
func watchResolveSelf(rows []watchEntry, root uint32, thread string, command func(uint32) string) (watchSample, error) {
	var found []watchSample
	for _, row := range rows {
		s := row.Sample
		match := s.PID == root && (thread == "" || s.Tool == "codex")
		if thread != "" && s.Tool == "codex" {
			match = match || s.ThreadID == thread || s.SessionID == thread
			if !match {
				args, err := windows.DecomposeCommandLine(command(s.AgentPID))
				if err == nil {
					for i := 1; i+1 < len(args); i++ {
						if args[i] == "resume" && args[i+1] == thread {
							match = true
						}
					}
				}
			}
		}
		if match {
			found = append(found, s)
		}
	}
	if len(found) != 1 {
		return watchSample{}, errors.New("Cannot identify this session uniquely; use an explicit PID from agents-cli watch list")
	}
	return found[0], nil
}

func watchActionTarget(dir, ref string) (watchReceipt, watchSample, error) {
	var receipt watchReceipt
	b, err := os.ReadFile(dir + string(os.PathSeparator) + "status.json")
	if err != nil {
		return receipt, watchSample{}, err
	}
	if err = json.Unmarshal(b, &receipt); err != nil {
		return receipt, watchSample{}, err
	}
	if !shelfAlive(receipt.PID, receipt.Started) || time.Since(receipt.Updated) > 10*time.Second {
		return receipt, watchSample{}, errors.New("Watcher is not running or its status is stale")
	}
	var sample watchSample
	if ref == "self" {
		processes, err := snapshot()
		if err != nil {
			return receipt, sample, err
		}
		processes.fillCmd()
		sample, err = watchResolveSelf(receipt.Sessions, processes.selfRoot(), os.Getenv("CODEX_THREAD_ID"), commandLine)
		if err != nil {
			return receipt, sample, err
		}
	} else {
		var sessions []Session
		for _, row := range receipt.Sessions {
			sessions = append(sessions, row.Sample.Session)
		}
		s, err := resolve(ref, sessions)
		if err != nil {
			return receipt, sample, err
		}
		for _, row := range receipt.Sessions {
			if row.Sample.PID == s.PID {
				sample = row.Sample
			}
		}
	}
	if err = shelfSource(sample); err != nil {
		return receipt, sample, err
	}
	return receipt, sample, nil
}

func (a *watchApp) applySessionAction(action watchSessionAction) bool {
	if action.Action != "park" && action.Action != "unpark" {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	entry := a.tracker.entries[action.Sample.PID]
	if entry == nil || !watchSameClickSession(action.Sample, entry.Sample) || action.Sample.NamingAgentStarted != entry.Sample.NamingAgentStarted || action.Sample.WindowStarted != entry.Sample.WindowStarted {
		return false
	}
	park := action.Action == "park"
	if entry.Parked != park && !a.tracker.togglePark(action.Sample) {
		return false
	}
	a.save()
	return true
}

func (a *watchApp) receiveSessionAction(lp uintptr) uintptr {
	if lp == 0 {
		return 0
	}
	data := (*watchCopyData)(unsafe.Pointer(lp))
	if data.Tag != watchSessionActionTag || data.Size == 0 || data.Size > 16384 || data.Data == 0 {
		return 0
	}
	var action watchSessionAction
	if json.Unmarshal(unsafe.Slice((*byte)(unsafe.Pointer(data.Data)), int(data.Size)), &action) != nil {
		return 0
	}
	if a.applySessionAction(action) {
		return 1
	}
	return 0
}

func cmdSessionAction(action string, args []string) error {
	p, err := parseArgs(args, "check")
	if err != nil {
		return err
	}
	if len(p.pos) != 1 {
		return fmt.Errorf("Use %s self|PID|unique-name [--check]", action)
	}
	dir, err := watchDirectory(p)
	if err != nil {
		return err
	}
	receipt, sample, err := watchActionTarget(dir, p.pos[0])
	if err != nil {
		return err
	}
	if p.has("check") {
		emit(map[string]any{"checked": true, "action": action, "pid": sample.PID, "name": firstNonEmpty(sample.Name, sample.Title), "tool": sample.Tool})
		return nil
	}
	if action == "shelve" {
		// Review and confirmation are owner-paced. Queueing a busy caller
		// lets the current turn finish before the existing idle-safe close.
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		cmd := watchBackgroundCommand(exe, []string{"shelf", "review", "--state-dir", dir}, nil)
		// Hand off through a real pipe before returning; os/exec's reader
		// goroutine could be terminated with this short-lived parent.
		reader, writer, err := os.Pipe()
		if err != nil {
			return err
		}
		defer writer.Close()
		cmd.Stdin, cmd.Stdout, cmd.Stderr = reader, nil, nil
		if err = cmd.Start(); err != nil {
			reader.Close()
			return err
		}
		reader.Close()
		_, err = writer.Write([]byte(shelfReviewJSON(sample)))
		writer.Close()
		cmd.Process.Release()
		if err != nil {
			return err
		}
		emit(map[string]any{"review_opened": true, "pid": sample.PID})
		return nil
	}
	window := watchShelfOwner()
	if window == 0 || watchWindowPID(window) != receipt.PID {
		return errors.New("Matching watcher panel is unavailable")
	}
	b, _ := json.Marshal(watchSessionAction{Action: action, Sample: sample})
	data := watchCopyData{Tag: watchSessionActionTag, Size: uint32(len(b)), Data: uintptr(unsafe.Pointer(&b[0]))}
	var result uintptr
	ok, _, _ := watchUser.NewProc("SendMessageTimeoutW").Call(window, 0x4a, 0, uintptr(unsafe.Pointer(&data)), 0x2|0x20, 2000, uintptr(unsafe.Pointer(&result)))
	runtime.KeepAlive(b)
	if ok == 0 || result != 1 {
		return errors.New("Watcher did not confirm the action; inspect its row before retrying")
	}
	emit(map[string]any{"action": action, "pid": sample.PID, "parked": action == "park"})
	return nil
}
