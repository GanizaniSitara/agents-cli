package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

type watchBatch struct {
	Stage         string             `json:"stage,omitempty"`
	Failures      []watchReaderError `json:"failures,omitempty"`
	ReadingPID    uint32             `json:"reading_pid,omitempty"`
	ScanMillis    int64              `json:"scan_ms,omitempty"`
	Partial       bool               `json:"partial,omitempty"`
	Alive         map[uint32]string  `json:"alive,omitempty"`
	Unavailable   []uint32           `json:"unavailable,omitempty"`
	Samples       []watchSample      `json:"samples"`
	Error         string             `json:"error,omitempty"`
	ConsoleEvents uint64             `json:"console_events"`
}

type watchReceipt struct {
	BuildCommit       string                         `json:"build_commit"`
	ErrorCode         string                         `json:"error_code"`
	ReaderStage       string                         `json:"reader_stage"`
	LastReaderError   *watchReaderError              `json:"last_reader_error,omitempty"`
	LastConsoleError  *watchReaderError              `json:"last_console_error,omitempty"`
	ReaderErrorCounts map[string]uint64              `json:"reader_error_counts,omitempty"`
	ReaderRestarts    int                            `json:"reader_restarts"`
	ReadingPID        uint32                         `json:"reading_pid,omitempty"`
	LastScanMillis    int64                          `json:"last_scan_ms,omitempty"`
	Shortcuts         map[string]watchShortcutHealth `json:"shortcuts,omitempty"`
	PID               uint32                         `json:"pid"`
	Started           string                         `json:"started"`
	Updated           time.Time                      `json:"updated"`
	Rows              []watchEntry                   `json:"stopped"`
	Sessions          []watchEntry                   `json:"sessions"`
	Error             string                         `json:"error,omitempty"`
	ConsoleEvents     uint64                         `json:"console_events"`
	Coverage          map[string]map[string]int      `json:"coverage"`
	CurrentDesktop    string                         `json:"current_desktop"`
	PanelRows         []watchEntry                   `json:"panel_rows"`
	PanelSummary      []string                       `json:"panel_summary"`
	PanelSections     []watchPanelSummary            `json:"panel_sections"`
}

type watchApp struct {
	policyQueue       chan watchSample
	policyAttempts    watchPolicyAttempts
	problemCode       string
	readerStage       string
	lastReaderError   *watchReaderError
	lastConsoleError  *watchReaderError
	readerErrorCounts map[string]uint64
	readerRestarts    int
	readingPID        uint32
	lastScanMillis    int64
	shortcuts         map[string]watchShortcutHealth
	mu                sync.Mutex
	tracker           *watchTracker
	dir               string
	problem           string
	stop              chan struct{}
	consoleEvents     uint64
	coverage          map[string]map[string]int
	flashes           watchFlashController
	agentRecords      watchAgentRecords
	foregroundLift    watchForegroundLift
}

func watchDirectory(p parsed) (string, error) {
	if p.has("state-dir") {
		return p.str("state-dir", ""), nil
	}
	d, err := os.UserCacheDir()
	return filepath.Join(d, "agents-cli", "watch"), err
}

func runWatch(args []string) error {
	if args[0] == "_watch-worker" {
		var after, cooldown uint64
		if len(args) >= 2 {
			after, _ = strconv.ParseUint(args[1], 10, 32)
		}
		if len(args) >= 3 {
			cooldown, _ = strconv.ParseUint(args[2], 10, 32)
		}
		return watchWorker(uint32(after), uint32(cooldown))
	}
	if len(args) == 1 || args[1] == "--help" || args[1] == "help" {
		printHelp()
		return nil
	}
	p, err := parseArgs(args[1:])
	if err != nil {
		return err
	}
	if len(p.pos) != 1 {
		return fmt.Errorf("use watch start|run|list|stop [--stable 2s] [--state-dir DIR]")
	}
	dir, err := watchDirectory(p)
	if err != nil {
		return err
	}
	statePath := filepath.Join(dir, "status.json")
	if p.pos[0] == "monitors" {
		emit(watchMonitors())
		return nil
	}
	if p.pos[0] == "desktops" {
		emit(watchDesktops())
		return nil
	}
	switch p.pos[0] {
	case "list", "stop":
		b, err := os.ReadFile(statePath)
		if err != nil {
			return err
		}
		var r watchReceipt
		if err := json.Unmarshal(b, &r); err != nil {
			return err
		}
		if startTime(r.PID).Format(time.RFC3339Nano) != r.Started || !isAlive(r.PID) {
			return fmt.Errorf("watcher is not running; status is stale")
		}
		if p.pos[0] == "stop" {
			return os.WriteFile(filepath.Join(dir, "stop"), []byte(r.Started), 0600)
		}
		if time.Since(r.Updated) > 10*time.Second {
			return fmt.Errorf("watcher has not updated for %s", time.Since(r.Updated).Round(time.Second))
		}
		rows := []map[string]interface{}{}
		for _, row := range r.Rows {
			rows = append(rows, map[string]interface{}{"pid": row.Sample.PID, "tool": row.Sample.Tool, "name": firstNonEmpty(row.Sample.Name, row.Sample.Title), "desktop": row.Sample.Desktop, "waiting_on": firstNonEmpty(row.Sample.Reason, watchReason(row.Sample.State)), "stopped_for": time.Since(row.Since).Round(time.Second).String(), "agents": row.Sample.Agents, "phase": row.Sample.Phase})
		}
		emit(map[string]interface{}{"pid": r.PID, "updated": r.Updated, "stopped": rows, "sessions": r.Sessions, "error": r.Error, "error_code": r.ErrorCode, "reader_stage": r.ReaderStage, "last_reader_error": r.LastReaderError, "last_console_error": r.LastConsoleError, "reader_error_counts": r.ReaderErrorCounts, "build_commit": r.BuildCommit, "coverage": r.Coverage, "shortcuts": r.Shortcuts, "reader_restarts": r.ReaderRestarts, "reading_pid": r.ReadingPID, "last_scan_ms": r.LastScanMillis})
		return nil
	case "start":
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		childArgs := append([]string{"watch", "run"}, args[2:]...)
		cmd := exec.Command(exe, childArgs...)
		cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.DETACHED_PROCESS | windows.BELOW_NORMAL_PRIORITY_CLASS, HideWindow: true}
		if err := cmd.Start(); err != nil {
			return err
		}
		pid := cmd.Process.Pid
		defer cmd.Process.Release()
		for until := time.Now().Add(12 * time.Second); time.Now().Before(until); {
			if b, err := os.ReadFile(statePath); err == nil {
				var r watchReceipt
				if json.Unmarshal(b, &r) == nil && r.PID == uint32(pid) {
					emit(map[string]interface{}{"started": true, "pid": pid, "state_dir": dir})
					return nil
				}
			}
			if !isAlive(uint32(pid)) {
				return fmt.Errorf("watcher exited; another watcher may already be running")
			}
			time.Sleep(100 * time.Millisecond)
		}
		return fmt.Errorf("watcher start receipt timed out (pid %d); inspect watch list before retrying", pid)
	case "run":
	default:
		return fmt.Errorf("unknown watch command %q", p.pos[0])
	}
	stable, err := p.durationFlag("stable", 2*time.Second)
	if err != nil || stable < time.Second {
		return fmt.Errorf("--stable must be at least 1s")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	absolute, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	hash := sha256.Sum256([]byte(strings.ToLower(absolute)))
	mutex, err := windows.CreateMutex(nil, false, windows.StringToUTF16Ptr(fmt.Sprintf("Local\\AgentsCLIWatch-%x", hash[:8])))
	if err != nil {
		return fmt.Errorf("watcher singleton: %w", err)
	}
	defer windows.CloseHandle(mutex)
	windows.SetPriorityClass(windows.CurrentProcess(), windows.BELOW_NORMAL_PRIORITY_CLASS)
	app := &watchApp{tracker: newWatchTracker(stable), dir: dir, stop: make(chan struct{}), policyQueue: make(chan watchSample, 16), policyAttempts: watchPolicyAttempts{}}
	if p.has("monitor") {
		number, err := p.intFlag("monitor", 0)
		if err != nil {
			return err
		}
		found := false
		for _, m := range watchMonitors() {
			if m.Number == number {
				settings := loadWatchSettings(dir)
				settings.Monitor = m.Device
				settings.Hidden = false
				if err := saveWatchSettings(dir, settings); err != nil {
					return err
				}
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("monitor %d is not connected", number)
		}
	}
	collected := make(chan struct{})
	defer func() {
		close(app.stop)
		// Let the collector cancel and reap its own reader before exiting.
		select {
		case <-collected:
		case <-time.After(2 * time.Second):
		}
	}()
	app.save()
	go app.policyInterruptLoop()
	go func() { defer close(collected); app.collect() }()
	return watchPanel(app)
}

// One persistent detached copy attaches sequentially, reusing the established
// classifier. There is no per-window/per-tick process creation. Its watchdog
// bounds even a blocked Windows syscall, independently of the parent's heartbeat.
func watchWorker(afterPID, cooldownPID uint32) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	windows.SetPriorityClass(windows.CurrentProcess(), windows.BELOW_NORMAL_PRIORITY_CLASS)
	initialDeadline := time.AfterFunc(watchReadDeadline, func() { os.Exit(2) })
	manager, release := openWatchDesktopManager()
	defer release()
	drain, unhook, hooked := watchConsoleEvents()
	defer unhook()
	initialDeadline.Stop()
	var desktops []watchDesktop
	var sessions []Session
	type cachedScreen struct {
		sample      watchSample
		desktopRead time.Time
		readAt      time.Time
	}
	cache := map[uint32]cachedScreen{}
	nextDiscovery := time.Time{}
	encoder := json.NewEncoder(os.Stdout)
	var eventCount uint64
	cooldownUntil := time.Now().Add(30 * time.Second)
	for {
		scanStarted := time.Now()
		deadline := time.AfterFunc(watchReadDeadline, func() { os.Exit(2) })
		batch := watchBatch{Alive: map[uint32]string{}, Stage: "discovery"}
		published := 0
		if err := watchPublishProgress(batch, &published, encoder.Encode, deadline.Reset); err != nil {
			return err
		}
		adopted := readAdopted()
		dirty := drain()
		foreground, _, _ := watchUser.NewProc("GetForegroundWindow").Call()
		eventCount += uint64(len(dirty))
		batch.ConsoleEvents = eventCount
		if time.Now().After(nextDiscovery) {
			desktops = watchDesktops()
			found, _, err := discover(discoverOpts{})
			if err != nil {
				batch.Error = err.Error()
				batch.Failures = append(batch.Failures, watchReaderError{Code: "WR107", Stage: "discovery", Detail: batch.Error})
				nextDiscovery = time.Now().Add(2 * time.Second)
			} else {
				sessions = found
				nextDiscovery = time.Now().Add(15 * time.Second)
			}
		}
		if err := watchPublishProgress(batch, &published, encoder.Encode, deadline.Reset); err != nil {
			return err
		}
		// One inexpensive process enumeration for all AGY rows, never parallel.
		var runtimeProcesses procMap
		for _, s := range sessions {
			if s.Tool == "agy" && !s.PollingMuted {
				runtimeProcesses, _ = snapshot()
				break
			}
		}
		for _, s := range watchReadOrder(sessions, afterPID) {
			batch.Stage = "read"
			batch.ReadingPID = s.PID
			if err := watchPublishProgress(batch, &published, encoder.Encode, deadline.Reset); err != nil {
				return err
			}
			if !s.Alive || s.Kind != "window" || s.PollingMuted {
				continue
			}
			started, _ := time.Parse(time.RFC3339Nano, s.Started)
			if entry, ok := adoptedFor(adopted, s.PID, started); ok && entry.PollingMuted {
				continue
			}
			rootStarted := startTime(s.PID)
			if rootStarted.IsZero() || rootStarted.Format(time.RFC3339) != normalizeWatchStart(s.Started) {
				continue
			}
			batch.Alive[s.PID] = rootStarted.Format(time.RFC3339Nano)
			if watchReadCoolingDown(s.PID, cooldownPID, time.Now(), cooldownUntil) {
				batch.Unavailable = append(batch.Unavailable, s.PID)
				continue
			}
			previous := cache[s.PID]
			if s.Tool != "agy" && hooked && previous.sample.RootStarted == rootStarted.Format(time.RFC3339Nano) && previous.sample.Started == s.Started && previous.sample.HWND != 0 && previous.sample.State != "busy" && previous.sample.State != "error" && previous.sample.State != "unknown" && !dirty[previous.sample.HWND] && time.Since(previous.readAt) < watchScreenCacheLifetime(previous.sample.HWND, foreground) {
				s.State, s.Title = previous.sample.State, previous.sample.Title
				sample := previous.sample
				sample.Session = s
				batch.Samples = append(batch.Samples, sample)
				continue
			}
			step := func(stage string) error {
				batch.Stage = stage
				return watchPublishProgress(batch, &published, encoder.Encode, deadline.Reset)
			}
			if err := step("read.runtime"); err != nil {
				return err
			}
			work := agyRuntimeWork(s, runtimeProcesses, commandLine, startTime)
			r := peekWithBlankRetry(func() *peekResult { return doPeekProgress(s.PID, 0, step) }, time.Sleep)
			if r.Error != "" {
				batch.Failures = append(batch.Failures, watchReaderError{Code: "WR108", Stage: "read", PID: s.PID, Detail: r.Error})
				if previous.sample.HWND != 0 && previous.sample.Started == s.Started && previous.sample.RootStarted == rootStarted.Format(time.RFC3339Nano) {
					failed := previous.sample
					failed = watchUnavailableSample(failed, "console read unavailable; retrying")
					failed.Signature = r.Error
					failed.WorkProcesses = work
					if work > 0 {
						failed.State, failed.Phase, failed.Reason = "busy", "working", "child command active; console unavailable"
					}
					previous.sample = failed
					cache[s.PID] = previous
					batch.Samples = append(batch.Samples, failed)
				} else {
					batch.Unavailable = append(batch.Unavailable, s.PID)
				}
				continue
			}
			if err := step("read.window"); err != nil {
				return err
			}
			hwnd, _, _ := k32.NewProc("GetConsoleWindow").Call()
			hwnd = watchConsolePresentation(hwnd)
			if hwnd == 0 {
				batch.Unavailable = append(batch.Unavailable, s.PID)
				continue
			} // Detached or unowned pseudo consoles have no presentation window.
			s.Title = r.Title
			digest := sha256.Sum256([]byte(strings.Join(r.Lines, "\n")))
			signature := hex.EncodeToString(digest[:8])
			if s.Tool != "agy" && previous.sample.RootStarted == rootStarted.Format(time.RFC3339Nano) && previous.sample.Started == s.Started && previous.sample.HWND == hwnd && previous.sample.Signature == signature {
				s.State = previous.sample.State
			} else {
				s.State = guessState(s.Tool, r.Lines)
			}
			desktopID, desktop := previous.sample.DesktopID, previous.sample.Desktop
			if previous.sample.RootStarted != rootStarted.Format(time.RFC3339Nano) || previous.sample.HWND != hwnd || time.Since(previous.desktopRead) >= 5*time.Second {
				if err := step("read.desktop"); err != nil {
					return err
				}
				desktopID, desktop = windowWatchDesktop(manager, hwnd, desktops)
				previous.desktopRead = time.Now()
			}
			if err := step("read.classify"); err != nil {
				return err
			}
			sample := watchSample{Session: s, HWND: hwnd, Signature: signature, DesktopID: desktopID, Desktop: desktop, Context: watchContext(r.Lines)}
			sample.RootStarted = rootStarted.Format(time.RFC3339Nano)
			sample.WindowPID = watchWindowPID(hwnd)
			sample.WindowStarted = startTime(sample.WindowPID).Format(time.RFC3339Nano)
			if s.Tool != "agy" && previous.sample.RootStarted == rootStarted.Format(time.RFC3339Nano) && previous.sample.Signature == signature && previous.sample.Started == s.Started && previous.sample.HWND == hwnd {
				sample.Phase, sample.Reason, sample.Agents = previous.sample.Phase, previous.sample.Reason, previous.sample.Agents
			} else {
				sample.Phase, sample.Reason = watchPhase(s.Tool, r.Lines, s.State)
				sample.Agents = watchAgentDetails(s.Tool, r.Lines)
			}
			if sample.Phase == "error" || sample.Phase == "blocked" {
				sample.State = "error"
			}
			sample.WorkProcesses = work
			if s.Tool == "codex" {
				sample.PendingQuestions, sample.Working = codexQueuedQuestions(r.Lines)
			}
			if agyRuntimeState(sample.State, work) != sample.State {
				sample.State, sample.Phase, sample.Reason = "busy", "working", "child command active"
			}
			if sample.Agents.Running != nil && *sample.Agents.Running > 0 {
				sample.Working = true
			}
			if sample.Agents.Running != nil && *sample.Agents.Running > 0 && sample.PendingQuestions == 0 && sample.State != "error" {
				sample.State = "busy"
				if sample.Phase != "error" {
					sample.Phase = "working"
					sample.Reason = "sub-agents running"
				}
			}
			cache[s.PID] = cachedScreen{sample: sample, desktopRead: previous.desktopRead, readAt: time.Now()}
			batch.Samples = append(batch.Samples, sample)
		}
		batch.Samples = batch.Samples[published:]
		batch.Partial = batch.Error != ""
		batch.ReadingPID = 0
		batch.Stage = "pause"
		batch.ScanMillis = time.Since(scanStarted).Milliseconds()
		if err := encoder.Encode(batch); err != nil {
			return err
		}
		deadline.Stop()
		// Two matching stopped samples, two seconds apart, notify within about
		// four seconds while keeping even busy-console polling inexpensive.
		time.Sleep(2 * time.Second)
	}
}

func normalizeWatchStart(v string) string {
	t, err := time.Parse(time.RFC3339Nano, v)
	if err != nil {
		return v
	}
	return t.Format(time.RFC3339)
}

func (a *watchApp) save() {
	r := watchReceipt{PID: uint32(os.Getpid()), Started: startTime(uint32(os.Getpid())).Format(time.RFC3339Nano), Updated: time.Now(), Rows: a.tracker.rows(), Sessions: a.tracker.panelRows(), Error: a.problem, ConsoleEvents: a.consoleEvents, Coverage: a.coverage}
	r.BuildCommit = buildCommit
	r.ErrorCode, r.ReaderStage, r.LastReaderError, r.ReaderErrorCounts = a.problemCode, a.readerStage, a.lastReaderError, a.readerErrorCounts
	r.LastConsoleError = a.lastConsoleError
	r.Shortcuts = a.shortcuts
	r.ReaderRestarts, r.ReadingPID, r.LastScanMillis = a.readerRestarts, a.readingPID, a.lastScanMillis
	r.CurrentDesktop = watchCurrentDesktop()
	r.PanelRows, r.PanelSections = watchPanelLayout(r.Sessions, r.CurrentDesktop, watchDesktops(), loadWatchSettings(a.dir).includedDesktops())
	for _, summary := range r.PanelSections {
		r.PanelSummary = append(r.PanelSummary, summary.Text)
	}
	b, _ := json.MarshalIndent(r, "", "  ")
	tmp := filepath.Join(a.dir, "status.tmp")
	if err := os.WriteFile(tmp, b, 0600); err == nil {
		_ = windows.MoveFileEx(windows.StringToUTF16Ptr(tmp), windows.StringToUTF16Ptr(filepath.Join(a.dir, "status.json")), windows.MOVEFILE_REPLACE_EXISTING)
	}
}

func (a *watchApp) collect() {
	for {
		select {
		case <-a.stop:
			return
		default:
		}
		err := a.readWorker()
		if err == nil {
			return
		}
		a.mu.Lock()
		a.problem = fmt.Sprint(err)
		failure, ok := err.(*watchReaderError)
		if !ok {
			failure = watchReaderFailure("WR105", "", err)
		}
		a.problemCode = failure.Code
		a.recordReaderError(*failure)
		a.readerRestarts++
		if failure.Code == "WR104" && a.readingPID != 0 && strings.HasPrefix(a.readerStage, "read") {
			if entry := a.tracker.entries[a.readingPID]; entry != nil {
				entry.unavailable("console timed out; cooling down", time.Now())
			}
		} else {
			a.tracker.unavailable("reader reconnecting; status unavailable", time.Now())
		}
		a.save()
		a.mu.Unlock()
		select {
		case <-a.stop:
			return
		case <-time.After(3 * time.Second):
		}
	}
}

func (a *watchApp) readWorker() (result error) {
	a.mu.Lock()
	a.readerStage = "startup"
	a.mu.Unlock()
	exe, err := os.Executable()
	if err != nil {
		return watchReaderFailure("WR101", "startup", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var cooldownPID uint32
	if a.lastReaderError != nil && a.lastReaderError.Code == "WR104" && strings.HasPrefix(a.lastReaderError.Stage, "read") {
		cooldownPID = a.lastReaderError.PID
	}
	cmd := exec.CommandContext(ctx, exe, "_watch-worker", strconv.FormatUint(uint64(a.readingPID), 10), strconv.FormatUint(uint64(cooldownPID), 10))
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.DETACHED_PROCESS | windows.BELOW_NORMAL_PRIORITY_CLASS, HideWindow: true}
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return watchReaderFailure("WR101", "startup", err)
	}
	if err = cmd.Start(); err != nil {
		return watchReaderFailure("WR101", "startup", err)
	}
	defer func() {
		cancel()
		pipe.Close()
		exitErr := cmd.Wait()
		if failure, ok := result.(*watchReaderError); ok && (failure.Code == "WR105" || failure.Code == "WR106") {
			classified := watchReaderExit(nil, exitErr)
			if classified.Code == "WR104" {
				result = classified
			} else {
				failure.ExitCode = classified.ExitCode
				if failure.ExitCode != nil {
					failure.Detail += fmt.Sprintf(" (exit %d)", *failure.ExitCode)
				}
			}
		}
	}()
	updates := make(chan watchBatch)
	fail := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(pipe)
		scanner.Buffer(make([]byte, 4096), 2<<20)
		for scanner.Scan() {
			var b watchBatch
			if err := json.Unmarshal(scanner.Bytes(), &b); err != nil {
				fail <- watchReaderFailure("WR103", "protocol", fmt.Errorf("invalid reader JSON: %w", err))
				return
			}
			select {
			case updates <- b:
			case <-ctx.Done():
				return
			}
		}
		fail <- watchReaderExit(scanner.Err(), nil)
	}()
	timer := time.NewTimer(watchHeartbeatTimeout)
	defer timer.Stop()
	for {
		select {
		case <-a.stop:
			return nil
		case <-timer.C:
			return watchReaderFailure("WR102", "", fmt.Errorf("read worker heartbeat timed out; restarting"))
		case err := <-fail:
			return err
		case batch := <-updates:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(watchHeartbeatTimeout)
			for i := range batch.Samples {
				if counts, ok := a.agentRecords.counts(batch.Samples[i].Session); ok {
					batch.Samples[i].Agents = counts
				}
			}
			a.mu.Lock()
			a.problem = batch.Error
			a.readerStage = batch.Stage
			a.problemCode = ""
			if batch.Error != "" {
				a.problemCode = "WR107"
			}
			for _, failure := range batch.Failures {
				a.recordReaderError(failure)
			}
			a.readingPID = batch.ReadingPID
			if batch.Partial && len(batch.Samples) == 0 && len(batch.Failures) == 0 && batch.Error == "" {
				// Progress updates only refresh the watchdog and current stage.
				// Avoid disk writes and desktop COM queries for every syscall.
				a.mu.Unlock()
				continue
			}
			if !batch.Partial {
				a.lastScanMillis = batch.ScanMillis
			}
			a.consoleEvents = batch.ConsoleEvents
			settings := loadWatchSettings(a.dir)
			alive := map[uint32]bool{}
			if !batch.Partial {
				for pid, root := range batch.Alive {
					if entry := a.tracker.entries[pid]; entry != nil && entry.Sample.RootStarted == root {
						alive[pid] = true
					}
				}
			}
			for _, s := range batch.Samples {
				alive[s.PID] = true
				if a.tracker.observe(s, time.Now()) {
					a.log(s)
				}
				if a.policyAttempts != nil && a.policyAttempts.ready(s, settings.InterruptPolicyBlock) {
					select {
					case a.policyQueue <- s:
						a.policyAttempts[s.PID] = s.RootStarted
					default: // Retry enqueueing next scan; never block the reader.
					}
				}
			}
			if !batch.Partial {
				a.tracker.prune(alive)
			}
			for _, pid := range batch.Unavailable {
				if entry := a.tracker.entries[pid]; entry != nil && entry.Sample.RootStarted == batch.Alive[pid] {
					entry.unavailable("console read unavailable; retrying", time.Now())
				}
			}
			a.coverage = map[string]map[string]int{}
			for _, entry := range a.tracker.entries {
				s := entry.Sample
				if a.coverage[s.Tool] == nil {
					a.coverage[s.Tool] = map[string]int{}
				}
				a.coverage[s.Tool][s.State]++
			}
			a.flashes.update(a.tracker.entries, watchCurrentDesktop(), watchFlash)
			a.save()
			a.mu.Unlock()
		}
	}
}

func (a *watchApp) log(s watchSample) {
	f, err := os.OpenFile(filepath.Join(a.dir, "events.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return
	}
	defer f.Close()
	_ = json.NewEncoder(f).Encode(map[string]interface{}{"time": time.Now().Format(time.RFC3339), "pid": s.PID, "name": firstNonEmpty(s.Name, s.Title, strconv.Itoa(int(s.PID))), "waiting_on": firstNonEmpty(s.Reason, watchReason(s.State))})
}

func watchReason(state string) string {
	if state == "error" {
		return "error"
	}
	if state == "prompt" {
		return "needs input"
	}
	return "finished"
}

type flashInfo struct {
	Size    uint32
	HWND    uintptr
	Flags   uint32
	Count   uint32
	Timeout uint32
}

func watchFlash(hwnd uintptr, on bool) {
	flags := uint32(0)
	if on {
		flags = 2 | 12
	} // taskbar only; until foreground
	f := flashInfo{HWND: hwnd, Flags: flags}
	f.Size = uint32(unsafe.Sizeof(f))
	watchUser.NewProc("FlashWindowEx").Call(uintptr(unsafe.Pointer(&f)))
}
