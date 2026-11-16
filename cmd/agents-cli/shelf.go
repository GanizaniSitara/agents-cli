package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

type shelfEntry struct {
	ID           string      `json:"id"`
	Name         string      `json:"name"`
	Tool         string      `json:"tool"`
	Conversation string      `json:"conversation,omitempty"`
	Directory    string      `json:"directory,omitempty"`
	Exe          string      `json:"exe,omitempty"`
	Args         []string    `json:"args,omitempty"`
	Env          []string    `json:"env,omitempty"`
	Saved        time.Time   `json:"saved"`
	State        string      `json:"state"`
	Problem      string      `json:"problem,omitempty"`
	Source       watchSample `json:"source"`
	ResumedPID   uint32      `json:"resumed_pid,omitempty"`
	ResumedStart string      `json:"resumed_start,omitempty"`
	WorkerPID    uint32      `json:"worker_pid,omitempty"`
	WorkerStart  string      `json:"worker_start,omitempty"`
}

func shelfPath(dir string) string { return filepath.Join(dir, "shelf.json") }

func (a *watchApp) refreshShelfPending() {
	entries, err := shelfRead(a.dir)
	if err != nil {
		return
	}
	pending := map[string]bool{}
	for _, e := range entries {
		if e.State == "pending" {
			pending[watchNameKey(e.Source)] = true
		}
	}
	a.shelfPending.Store(&pending)
	if owner := watchShelfOwner(); owner != 0 {
		watchUser.NewProc("InvalidateRect").Call(owner, 0, 1)
	}
}

func shelfRead(dir string) ([]shelfEntry, error) {
	f, err := os.Open(shelfPath(dir))
	if os.IsNotExist(err) {
		return []shelfEntry{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, 8*1024*1024+1))
	if err != nil {
		return nil, err
	}
	if len(b) > 8*1024*1024 {
		return nil, errors.New("Shelf exceeds its size limit; no entries changed")
	}
	var entries []shelfEntry
	if err = json.Unmarshal(b, &entries); err != nil {
		return nil, fmt.Errorf("Shelf is unreadable; no entries changed: %w", err)
	}
	return entries, nil
}

// All writers, including pending requests and resume, serialize across processes.
func shelfUpdate(dir string, change func(*[]shelfEntry) error) error {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	f, err := os.OpenFile(shelfPath(dir)+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	var ov windows.Overlapped
	if err = windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK, 0, 1, 0, &ov); err != nil {
		return err
	}
	defer windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, &ov)
	entries, err := shelfRead(dir)
	if err != nil {
		return err
	}
	if err = change(&entries); err != nil {
		return err
	}
	b, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return err
	}
	if len(b) > 8*1024*1024 {
		return errors.New("Shelf exceeds its size limit; session left open")
	}
	tmp, err := os.CreateTemp(dir, ".shelf-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err = tmp.Write(b); err == nil {
		err = tmp.Sync()
	}
	closeErr := tmp.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return windows.MoveFileEx(windows.StringToUTF16Ptr(tmp.Name()), windows.StringToUTF16Ptr(shelfPath(dir)), windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}

func shelfSameSource(a, b watchSample) bool {
	return watchSameClickSession(a, b) && a.NamingAgentStarted == b.NamingAgentStarted
}
func shelfAlive(pid uint32, started string) bool {
	return pid != 0 && started != "" && isAlive(pid) && startTime(pid).Format(time.RFC3339Nano) == started
}
func shelfSourceAlive(e shelfEntry) bool { return shelfAlive(e.Source.PID, e.Source.RootStarted) }
func shelfID() string                    { var b [16]byte; _, _ = rand.Read(b[:]); return hex.EncodeToString(b[:]) }

func shelfSource(s watchSample) error {
	if p := watchTargetProblem(s, watchReadTarget(s)); p != "" {
		return errors.New(p)
	}
	if !shelfAlive(s.AgentPID, s.NamingAgentStarted) {
		return errors.New("Agent changed; reopen the menu")
	}
	if watchWindowTitle(s.HWND) != s.Title {
		return errors.New("Title changed; reopen the menu")
	}
	return nil
}

func shelfIdle(s watchSample, p *peekResult) bool {
	return p != nil && watchRenameComposerEmpty(s.Tool, p.Lines) && s.WorkProcesses == 0 && !s.Working && (s.Agents.Running == nil || *s.Agents.Running == 0)
}

func shelfSend(s watchSample, text string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "send", strconv.FormatUint(uint64(s.PID), 10), "--stdin", "--via", "inject")
	cmd.Stdin = strings.NewReader(text)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	cmd.WaitDelay = 100 * time.Millisecond
	b, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("Command submission failed; inspect the session before retrying")
	}
	var receipt struct {
		Submitted *bool `json:"submitted"`
	}
	if json.Unmarshal(b, &receipt) != nil || receipt.Submitted == nil || !*receipt.Submitted {
		return errors.New("Submission uncertain; nothing resent. Inspect the session")
	}
	return nil
}

var shelfUUID = regexp.MustCompile(`(?i)\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b`)

func shelfStatusID(lines []string) string {
	// Only a freshly requested native status panel supplies Codex identity.
	var ids []string
	for _, line := range lines {
		line = strings.TrimSpace(strings.Trim(line, "│┃| "))
		if strings.HasPrefix(strings.ToLower(line), "session:") {
			if id := shelfUUID.FindString(line); id != "" {
				ids = append(ids, id)
			}
		}
	}
	if len(ids) == 1 {
		return ids[0]
	}
	return ""
}

func shelfWorkingDirectory(pid uint32) string {
	h, p, ok := processParams(pid)
	if !ok {
		return ""
	}
	defer windows.CloseHandle(h)
	u := p.CurrentDirectory.DosPath
	if u.Length == 0 || u.Buffer == nil {
		return ""
	}
	b := make([]uint16, u.Length/2)
	var n uintptr
	if windows.ReadProcessMemory(h, uintptr(unsafe.Pointer(u.Buffer)), (*byte)(unsafe.Pointer(&b[0])), uintptr(u.Length), &n) != nil {
		return ""
	}
	return windows.UTF16ToString(b)
}

func shelfResolve(s watchSample) (shelfEntry, error) {
	e := shelfEntry{ID: shelfID(), Name: firstNonEmpty(watchShortName(watchRowLabel(s)), s.Tool), Tool: s.Tool, Source: s, Saved: time.Now().UTC(), State: "saved"}
	if err := shelfSource(s); err != nil {
		return e, err
	}
	p, err := peek(s.PID, 0)
	if err != nil {
		return e, err
	}
	if !shelfIdle(s, p) {
		return e, errors.New("Session must be idle with an empty input field and no background work")
	}
	e.Directory = shelfWorkingDirectory(s.AgentPID)
	e.Exe = exePath(commandLine(s.AgentPID))
	if e.Directory == "" || e.Exe == "" {
		return e, errors.New("Could not verify executable and working folder; window left open")
	}
	switch s.Tool {
	case "claude":
		base := envVar(s.AgentPID, "CLAUDE_CONFIG_DIR")
		if base == "" {
			home, _ := os.UserHomeDir()
			base = filepath.Join(home, ".claude")
		}
		b, err := os.ReadFile(filepath.Join(base, "sessions", fmt.Sprint(s.AgentPID)+".json"))
		if err != nil {
			return e, errors.New("Claude has no current PID/session record; window left open")
		}
		var record struct {
			SessionID string `json:"sessionId"`
			ProcStart string `json:"procStart"`
			Cwd       string `json:"cwd"`
		}
		if json.Unmarshal(b, &record) != nil || shelfUUID.FindString(record.SessionID) != record.SessionID || len(record.SessionID) != 36 || record.ProcStart != strconv.FormatInt(startTime(s.AgentPID).UnixNano()/100+116444736000000000, 10) {
			return e, errors.New("Claude session record is stale; window left open")
		}
		e.Conversation = record.SessionID
		e.Directory = record.Cwd
		e.Args = []string{"--resume", e.Conversation}
		e.Env = append(e.Env, "CLAUDE_CONFIG_DIR="+base)
	case "codex":
		if s.Home == "" {
			return e, errors.New("Codex account/profile is unknown")
		}
		before := strings.Join(p.Lines, "\n")
		if err = shelfSend(s, "/status"); err != nil {
			return e, err
		}
		until := time.Now().Add(3 * time.Second)
		for time.Now().Before(until) {
			time.Sleep(150 * time.Millisecond)
			p, err = peek(s.PID, 0)
			if err != nil {
				return e, err
			}
			if strings.Join(p.Lines, "\n") != before {
				e.Conversation = shelfStatusID(p.Lines)
				if e.Conversation != "" {
					break
				}
			}
		}
		if e.Conversation == "" {
			return e, errors.New("Could not verify the current Codex session from /status; window left open")
		}
		// /status is a native information panel, never an agent prompt.
		if !watchRenameComposerEmpty(s.Tool, p.Lines) {
			if err = keyFor(s.PID, s.Tool, "escape"); err != nil {
				return e, err
			}
			time.Sleep(150 * time.Millisecond)
		}
		e.Args = []string{"resume", e.Conversation}
		e.Env = append(e.Env, "CODEX_HOME="+s.Home)
	default:
		return e, fmt.Errorf("Verified shelving is not yet available for %s; window left open", s.Tool)
	}
	// Preserve explicit launch permissions/model options, never arbitrary argv or secrets.
	args, _ := windows.DecomposeCommandLine(commandLine(s.AgentPID))
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--dangerously-skip-permissions", "--dangerously-bypass-approvals-and-sandbox", "--no-daemon", "--no-alt-screen":
			e.Args = append(e.Args, args[i])
		case "--permission-mode", "--model", "--sandbox", "--ask-for-approval", "--profile":
			if i+1 < len(args) {
				e.Args = append(e.Args, args[i], args[i+1])
				i++
			}
		}
	}
	if info, err := os.Stat(e.Directory); err != nil || !info.IsDir() {
		return e, errors.New("Working folder is unavailable; window left open")
	}
	key := sha256.Sum256([]byte(e.Tool + "\x00" + strings.ToLower(strings.Join(e.Env, "\x00")) + "\x00" + strings.ToLower(e.Conversation)))
	e.ID = hex.EncodeToString(key[:16])
	return e, shelfSource(s)
}

func shelfStore(dir string, e shelfEntry) error {
	return shelfUpdate(dir, func(entries *[]shelfEntry) error {
		for i, old := range *entries {
			if old.ID == e.ID && e.Conversation != "" {
				if old.State == "closing" || old.State == "preparing" || old.State == "pending" {
					return errors.New("Shelving is already in progress")
				}
				if (shelfSourceAlive(old) && !shelfSameSource(old.Source, e.Source)) || (shelfAlive(old.ResumedPID, old.ResumedStart) && old.ResumedPID != e.Source.PID) {
					return errors.New("This conversation is already open elsewhere")
				}
				(*entries)[i] = e
				return nil
			}
			if shelfSameSource(old.Source, e.Source) && old.State != "preparing" && old.State != "cancelled" && old.State != "completed" && old.State != "failed" {
				return errors.New("Shelving is already pending")
			}
		}
		*entries = append(*entries, e)
		return nil
	})
}

func shelfChange(dir, id string, change func(*shelfEntry) error) error {
	return shelfUpdate(dir, func(entries *[]shelfEntry) error {
		for i := range *entries {
			if (*entries)[i].ID == id {
				return change(&(*entries)[i])
			}
		}
		return errors.New("Shelf entry no longer exists")
	})
}

func shelfClose(e shelfEntry) error {
	s := e.Source
	if err := shelfSource(s); err != nil {
		return err
	}
	p, err := peek(s.PID, 0)
	if err != nil {
		return err
	}
	if !shelfIdle(s, p) {
		return errors.New("Session became busy or has a draft; saved entry kept, window left open")
	}
	if err = shelfSend(s, "/exit"); err != nil {
		return err
	}
	until := time.Now().Add(5 * time.Second)
	for shelfAlive(s.AgentPID, s.NamingAgentStarted) && time.Now().Before(until) {
		time.Sleep(100 * time.Millisecond)
	}
	if shelfAlive(s.AgentPID, s.NamingAgentStarted) {
		return errors.New("Agent did not exit; saved entry kept, no process was killed")
	}
	if !shelfSourceAlive(e) {
		return nil
	}
	// A /k host shell may survive the agent. Exit it only after verifying it
	// has no children left; never close the shared Windows Terminal HWND.
	m, err := snapshot()
	if err != nil {
		return err
	}
	otherChildren := false
	for _, pid := range m.descendants(s.PID) {
		if p := m[pid]; p != nil && p.Name != "conhost.exe" && p.Name != "openconsole.exe" {
			otherChildren = true
		}
	}
	if otherChildren || m[s.PID] == nil || m[s.PID].Name != "cmd.exe" {
		return errors.New("Agent exited; host shell still has child processes, so it was left open")
	}
	if !shelfSourceAlive(e) {
		return nil
	}
	// Do not append exit to something the owner has typed into the host shell.
	shell, err := peek(s.PID, 0)
	if err != nil {
		return err
	}
	if !shelfShellEmpty(shell.Lines, shelfWorkingDirectory(s.PID)) {
		return errors.New("Saved successfully; host prompt is not empty or could not be verified, so it was left open")
	}
	if err = inject(s.PID, "exit", true); err != nil {
		return err
	}
	until = time.Now().Add(2 * time.Second)
	for shelfSourceAlive(e) && time.Now().Before(until) {
		time.Sleep(100 * time.Millisecond)
	}
	if shelfSourceAlive(e) {
		return errors.New("Saved successfully; host shell remains open")
	}
	return nil
}

func shelfShellEmpty(lines []string, cwd string) bool {
	if cwd == "" {
		return false
	}
	tail := trimBlankTail(lines)
	if len(tail) == 0 {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(tail[len(tail)-1]), filepath.Clean(cwd)+">")
}

func shelfSaveClose(dir string, s watchSample) error {
	e, err := shelfResolve(s)
	if err != nil {
		return err
	}
	return shelfSaveResolved(dir, e, shelfClose)
}

func shelfSaveResolved(dir string, e shelfEntry, closeSession func(shelfEntry) error) error {
	e.State = "closing"
	if err := shelfStore(dir, e); err != nil {
		return err
	} // durable save must succeed before /exit
	err := closeSession(e)
	writeErr := shelfChange(dir, e.ID, func(saved *shelfEntry) error {
		saved.State = "shelved"
		if err != nil {
			saved.State = "window-open"
			saved.Problem = err.Error()
		}
		return nil
	})
	if err != nil {
		return err
	}
	return writeErr
}

func shelfQueue(dir string, s watchSample) error {
	if s.Tool != "claude" && s.Tool != "codex" {
		return fmt.Errorf("Verified shelving is not yet available for %s", s.Tool)
	}
	if err := shelfSource(s); err != nil {
		return err
	}
	return shelfStore(dir, shelfEntry{ID: shelfID(), Name: firstNonEmpty(watchShortName(watchRowLabel(s)), s.Tool), Tool: s.Tool, Source: s, Saved: time.Now().UTC(), State: "pending"})
}

func shelfTick(dir string) error {
	entries, err := shelfRead(dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.State == "preparing" && !shelfAlive(entry.WorkerPID, entry.WorkerStart) {
			return shelfChange(dir, entry.ID, func(e *shelfEntry) error {
				e.State = "pending"
				for _, saved := range entries {
					if saved.Conversation != "" && shelfSameSource(saved.Source, e.Source) {
						e.State = "completed"
						break
					}
				}
				return nil
			})
		}
		if entry.State != "pending" {
			continue
		}
		s := entry.Source
		if !shelfSourceAlive(entry) || !shelfAlive(s.AgentPID, s.NamingAgentStarted) {
			return shelfChange(dir, entry.ID, func(e *shelfEntry) error {
				e.State = "failed"
				e.Problem = "Original session ended or changed before shelving"
				return nil
			})
		}
		b, readErr := os.ReadFile(filepath.Join(dir, "status.json"))
		var receipt watchReceipt
		if readErr != nil || json.Unmarshal(b, &receipt) != nil || time.Since(receipt.Updated) > 5*time.Second {
			continue
		}
		found := false
		for _, live := range receipt.Sessions {
			if shelfSameSource(live.Sample, s) {
				s = live.Sample
				found = true
				break
			}
		}
		if !found {
			continue
		}
		p, err := peek(s.PID, 0)
		if err != nil || !shelfIdle(s, p) {
			continue
		}
		// Claim once under the store lock before doing external work.
		if err = shelfChange(dir, entry.ID, func(e *shelfEntry) error {
			if e.State != "pending" {
				return errors.New("Request changed")
			}
			e.State = "preparing"
			e.WorkerPID = uint32(os.Getpid())
			e.WorkerStart = startTime(e.WorkerPID).Format(time.RFC3339Nano)
			return nil
		}); err != nil {
			return err
		}
		err = shelfSaveClose(dir, s)
		return shelfChange(dir, entry.ID, func(e *shelfEntry) error {
			e.State = "completed"
			if err != nil {
				e.State = "failed"
				e.Problem = err.Error()
			}
			return nil
		})
	}
	return nil
}

func shelfResume(dir, id string) error {
	// Keep the lock through spawn and PID recording to prevent double launch.
	return shelfChange(dir, id, func(e *shelfEntry) error {
		if e.Conversation == "" || e.State == "pending" || e.State == "preparing" || (e.State == "closing" && shelfSourceAlive(*e)) {
			return errors.New("This entry is not ready to resume")
		}
		if shelfSourceAlive(*e) || shelfAlive(e.ResumedPID, e.ResumedStart) {
			return errors.New("Session is already open; no duplicate was started")
		}
		if e.Tool != "claude" && e.Tool != "codex" {
			return errors.New("Unsupported resume provider")
		}
		// Recover an earlier spawn if saving its PID failed or the helper died.
		// Only our marker on the exact saved executable can suppress a new launch.
		processes, err := snapshot()
		if err != nil {
			return err
		}
		checked := 0
		for _, p := range processes {
			if strings.EqualFold(p.Name, filepath.Base(e.Exe)) {
				checked++
				if checked > 64 {
					return errors.New("Too many matching processes to verify duplicate protection")
				}
				if envVar(p.PID, "AGENTS_CLI_SHELF_ID") == e.ID {
					args, parseErr := windows.DecomposeCommandLine(commandLine(p.PID))
					if parseErr != nil || len(args) == 0 || !slices.Equal(args[1:], e.Args) {
						continue
					} // inherited markers on daemons/subagents do not identify this TUI
					e.ResumedPID = p.PID
					e.ResumedStart = startTime(p.PID).Format(time.RFC3339Nano)
					e.State = "resumed"
					return nil
				}
			}
		}
		cmd := exec.Command(e.Exe, e.Args...)
		cmd.Dir = e.Directory
		cmd.Env = os.Environ()
		for _, v := range e.Env {
			key, _, ok := strings.Cut(v, "=")
			if !ok || (key != "CODEX_HOME" && key != "CLAUDE_CONFIG_DIR") {
				return errors.New("Invalid saved profile")
			}
			for i := len(cmd.Env) - 1; i >= 0; i-- {
				if strings.HasPrefix(strings.ToUpper(cmd.Env[i]), key+"=") {
					cmd.Env = append(cmd.Env[:i], cmd.Env[i+1:]...)
				}
			}
			cmd.Env = append(cmd.Env, v)
		}
		for i := len(cmd.Env) - 1; i >= 0; i-- {
			if strings.HasPrefix(strings.ToUpper(cmd.Env[i]), "AGENTS_CLI_SHELF_ID=") {
				cmd.Env = append(cmd.Env[:i], cmd.Env[i+1:]...)
			}
		}
		cmd.Env = append(cmd.Env, "AGENTS_CLI_SHELF_ID="+e.ID)
		pid, err := shelfStartConsole(cmd.Path, cmd.Args, cmd.Dir, cmd.Env)
		if err != nil {
			return err
		}
		e.ResumedPID = pid
		e.ResumedStart = startTime(e.ResumedPID).Format(time.RFC3339Nano)
		e.State = "resumed"
		e.Problem = ""
		return nil
	})
}

// Do not use os/exec.Start here: its redirected standard handles replace the
// new console's input with NUL, causing real TUIs to exit or remain invisible.
func shelfStartConsole(exe string, args []string, dir string, env []string) (uint32, error) {
	application, err := windows.UTF16PtrFromString(exe)
	if err != nil {
		return 0, err
	}
	command, err := windows.UTF16PtrFromString(windows.ComposeCommandLine(args))
	if err != nil {
		return 0, err
	}
	cwd, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return 0, err
	}
	slices.SortFunc(env, func(a, b string) int { return strings.Compare(strings.ToUpper(a), strings.ToUpper(b)) })
	// Build the double-terminated block entry by entry, rejecting embedded NULs.
	var environment []uint16
	for _, value := range env {
		encoded, err := windows.UTF16FromString(value)
		if err != nil {
			return 0, err
		}
		environment = append(environment, encoded...)
	}
	environment = append(environment, 0, 0)
	startup := windows.StartupInfo{Cb: uint32(unsafe.Sizeof(windows.StartupInfo{})), Flags: windows.STARTF_USESHOWWINDOW, ShowWindow: windows.SW_HIDE}
	var process windows.ProcessInformation
	if err := windows.CreateProcess(application, command, nil, nil, false, windows.CREATE_NEW_CONSOLE|windows.CREATE_UNICODE_ENVIRONMENT, &environment[0], cwd, &startup, &process); err != nil {
		return 0, err
	}
	windows.CloseHandle(process.Thread)
	windows.CloseHandle(process.Process)
	return process.ProcessId, nil
}

func cmdShelf(args []string) error {
	p, err := parseArgs(args)
	if err != nil {
		return err
	}
	dir, err := watchDirectory(p)
	if err != nil {
		return err
	}
	if len(p.pos) == 0 {
		return errors.New("Use shelf list|ui|tick|resume ID|cancel ID")
	}
	switch p.pos[0] {
	case "list":
		entries, err := shelfRead(dir)
		if err != nil {
			return err
		}
		emit(entries)
		return nil
	case "tick":
		return shelfTick(dir)
	case "resume":
		if len(p.pos) != 2 {
			return errors.New("Use shelf resume ID")
		}
		return shelfResumeAndPresent(dir, p.pos[1])
	case "cancel":
		if len(p.pos) != 2 {
			return errors.New("Use shelf cancel ID")
		}
		return shelfChange(dir, p.pos[1], func(e *shelfEntry) error {
			if e.State != "pending" {
				return errors.New("Only pending shelving can be cancelled")
			}
			e.State = "cancelled"
			return nil
		})
	case "ui":
		return watchShelfWindow(dir)
	case "review":
		var s watchSample
		if err = json.NewDecoder(io.LimitReader(os.Stdin, 16384)).Decode(&s); err != nil {
			return err
		}
		return watchShelfReview(dir, s)
	case "inspect":
		var s watchSample
		if err = json.NewDecoder(io.LimitReader(os.Stdin, 16384)).Decode(&s); err != nil {
			return err
		}
		entry, err := shelfResolve(s)
		if err != nil {
			return err
		}
		emit(entry)
		return nil
	case "save", "queue":
		var s watchSample
		if err = json.NewDecoder(io.LimitReader(os.Stdin, 16384)).Decode(&s); err != nil {
			return err
		}
		if p.pos[0] == "queue" {
			return shelfQueue(dir, s)
		}
		return shelfSaveClose(dir, s)
	case "cancel-source":
		var s watchSample
		if err = json.NewDecoder(io.LimitReader(os.Stdin, 16384)).Decode(&s); err != nil {
			return err
		}
		err = shelfUpdate(dir, func(entries *[]shelfEntry) error {
			for i := range *entries {
				e := &(*entries)[i]
				if (e.State == "preparing" || e.State == "closing") && shelfSameSource(e.Source, s) {
					return errors.New("Shelving has already started; the saved entry will remain in Shelf")
				}
				if e.State == "pending" && shelfSameSource(e.Source, s) {
					e.State = "cancelled"
				}
			}
			return nil
		})
		if err != nil {
			watchShelfMessage(err.Error(), false)
		}
		return err
	}
	return errors.New("Unknown shelf command")
}
