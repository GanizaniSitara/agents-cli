package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/windows"
)

func cmdLs(args []string) error {
	p, err := parseArgs(args, "all", "fast")
	if err != nil {
		return err
	}
	sessions, _, err := discover(discoverOpts{
		includeDead: p.boolean("all"),
		withScreen:  !p.boolean("fast"),
		toolFilter:  strings.ToLower(p.str("tool", "")),
	})
	if err != nil {
		return err
	}
	if sessions == nil {
		sessions = []Session{}
	}
	emit(map[string]interface{}{"count": len(sessions), "sessions": sessions})
	return nil
}

func lookup(ref string) (*Session, procMap, error) {
	sessions, procs, err := discover(discoverOpts{includeDead: true})
	if err != nil {
		return nil, nil, err
	}
	s, err := resolve(ref, sessions)
	if err != nil {
		return nil, nil, err
	}
	return s, procs, nil
}

func cmdTail(args []string) error {
	p, err := parseArgs(args)
	if err != nil {
		return err
	}
	if len(p.pos) < 1 {
		return errors.New("usage: agents-cli tail <ref> [--lines N] [--above N]")
	}
	above, err := p.intFlag("above", 0)
	if err != nil {
		return err
	}
	lines, err := p.intFlag("lines", 0)
	if err != nil {
		return err
	}
	if above < 0 || lines < 0 || (p.has("lines") && lines == 0) {
		return errors.New("--above must be nonnegative and --lines must be positive")
	}
	s, _, err := lookup(p.pos[0])
	if err != nil {
		return err
	}
	if err := requirePolling(s); err != nil {
		return err
	}
	r, err := peek(s.PID, above)
	if err != nil {
		return err
	}
	emit(map[string]interface{}{
		"pid": s.PID, "tool": s.Tool, "name": s.Name, "title": r.Title,
		"state": guessState(s.Tool, r.Lines), "lines": screenTail(r.Lines, lines),
	})
	return nil
}

func cmdSend(args []string) error {
	p, err := parseArgs(args, "no-enter", "force")
	if err != nil {
		return err
	}
	if len(p.pos) < 2 {
		return errors.New(`usage: agents-cli send <ref> "text" [--no-enter] [--via auto|inject|native] [--force]`)
	}
	text := strings.Join(p.pos[1:], " ")
	s, procs, err := lookup(p.pos[0])
	if err != nil {
		return err
	}
	if !s.Alive {
		return fmt.Errorf("session %d is not running", s.PID)
	}
	if s.Tool == "claude" && procs.ancestorNamed(uint32(windows.GetCurrentProcessId()), "claude.exe") && !p.boolean("force") {
		return errors.New("target is a Claude session and so is the caller: use the native SendMessage tool, or pass --force")
	}
	via := p.str("via", "auto")
	switch via {
	case "auto":
		if s.CanNative && !p.boolean("no-enter") {
			via = "native"
		} else {
			via = "inject"
		}
	case "inject", "native":
	default:
		return fmt.Errorf("--via must be auto, inject or native (got %q)", via)
	}
	if via == "native" {
		if p.boolean("no-enter") {
			return errors.New("--no-enter requires injection; native queue submits immediately")
		}
		if s.Tool != "codex" {
			return fmt.Errorf("native send is only implemented for codex (target is %s)", s.Tool)
		}
		if !s.CanNative {
			return fmt.Errorf("native send unavailable: %s", s.NativeUnavailable)
		}
		thread := firstNonEmpty(s.ThreadID, s.SessionID)
		if thread == "" {
			return errors.New("codex session has no thread id in its launch-info; use --via inject")
		}
		// Use the session's own codex.exe and CODEX_HOME: two Codex installs
		// share this box and a queue aimed at the wrong profile lands nowhere.
		codex := s.Exe
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, codex, "queue", "--thread", thread, "--message", text)
		cmd.Env = append(os.Environ(), "CODEX_HOME="+s.Home)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("codex queue: %v: %s", err, strings.TrimSpace(string(out)))
		}
		emit(map[string]interface{}{"pid": s.PID, "tool": s.Tool, "variant": s.Variant, "via": "native", "thread": thread, "exe": codex, "home": s.Home, "sent": text, "codex": strings.TrimSpace(string(out))})
		return nil
	}
	if s.Tool == "copilot" {
		enter := !p.boolean("no-enter")
		submitted, err := liveCopilotConsole.send(s, text, enter)
		if err != nil {
			return err
		}
		emit(map[string]interface{}{"pid": s.PID, "tool": s.Tool, "via": "inject", "sent": text, "enter": enter, "submitted": submitted})
		return nil
	}
	if err := inject(s.PID, text, !p.boolean("no-enter")); err != nil {
		return err
	}
	emit(map[string]interface{}{"pid": s.PID, "tool": s.Tool, "via": "inject", "sent": text, "enter": !p.boolean("no-enter")})
	return nil
}

func screenTail(lines []string, count int) []string {
	if count == 0 {
		return lines
	}
	lines = trimBlankTail(lines)
	if len(lines) > count {
		return lines[len(lines)-count:]
	}
	return lines
}

func cmdKey(args []string) error {
	p, err := parseArgs(args)
	if err != nil {
		return err
	}
	if len(p.pos) < 2 {
		return errors.New("usage: agents-cli key <ref> enter|escape|ctrl-c|ctrl-u|tab|up|down|backspace")
	}
	s, _, err := lookup(p.pos[0])
	if err != nil {
		return err
	}
	if err := keyFor(s.PID, s.Tool, p.pos[1]); err != nil {
		return err
	}
	emit(map[string]interface{}{"pid": s.PID, "tool": s.Tool, "key": strings.ToLower(p.pos[1])})
	return nil
}

func cmdWait(args []string) error {
	p, err := parseArgs(args)
	if err != nil {
		return err
	}
	if len(p.pos) < 1 {
		return errors.New("usage: agents-cli wait <ref> [--idle-for 10s] [--timeout 10m] [--match REGEX]")
	}
	idleFor, err := p.durationFlag("idle-for", 10*time.Second)
	if err != nil {
		return err
	}
	timeout, err := p.durationFlag("timeout", 10*time.Minute)
	if err != nil {
		return err
	}
	var match *regexp.Regexp
	if m := p.str("match", ""); m != "" {
		if match, err = regexp.Compile(m); err != nil {
			return fmt.Errorf("--match: %v", err)
		}
	}
	s, _, err := lookup(p.pos[0])
	if err != nil {
		return err
	}
	if err := requirePolling(s); err != nil {
		return err
	}
	start := time.Now()
	deadline := start.Add(timeout)
	var last string
	lastChange := start
	var lines []string
	for {
		// Honour a mute issued by another coordinator while wait is running.
		if a, ok := adoptedFor(readAdopted(), s.PID, startTime(s.PID)); ok && a.PollingMuted {
			s.PollingMuted = true
			return requirePolling(s)
		}
		r, err := peek(s.PID, 0)
		if err != nil {
			if !isAlive(s.PID) {
				emit(map[string]interface{}{"pid": s.PID, "reason": "exited", "elapsed": time.Since(start).Round(time.Second).String()})
				return nil
			}
			return err
		}
		lines = r.Lines
		screen := strings.Join(r.Lines, "\n")
		if match != nil && match.MatchString(screen) {
			return emitWait(s, "match", start, lines)
		}
		if screen != last {
			last = screen
			lastChange = time.Now()
		} else if time.Since(lastChange) >= idleFor && waitCanIdle(s.Tool, lines) {
			return emitWait(s, "idle", start, lines)
		}
		if time.Now().After(deadline) {
			return emitWait(s, "timeout", start, lines)
		}
		time.Sleep(2 * time.Second)
	}
}

func emitWait(s *Session, reason string, start time.Time, lines []string) error {
	tail := trimBlankTail(lines)
	if len(tail) > 12 {
		tail = tail[len(tail)-12:]
	}
	emit(map[string]interface{}{
		"pid": s.PID, "tool": s.Tool, "name": s.Name, "reason": reason,
		"elapsed": time.Since(start).Round(time.Second).String(),
		"state":   guessState(s.Tool, lines), "tail": tail,
	})
	return nil
}

func cmdMute(args []string, muted bool) error {
	p, err := parseArgs(args)
	if err != nil {
		return err
	}
	if len(p.pos) != 1 || len(p.flags) != 0 {
		return errors.New("usage: agents-cli mute|unmute <ref>")
	}
	s, _, err := lookup(p.pos[0]) // metadata only; never peek to mute
	if err != nil {
		return err
	}
	started := startTime(s.PID)
	if !s.Alive || started.IsZero() {
		return fmt.Errorf("cannot identify live process start time for session %d", s.PID)
	}
	m := readAdopted()
	a, _ := adoptedFor(m, s.PID, started)
	a.Tool = s.Tool
	a.Start = started.Format(time.RFC3339)
	a.PollingMuted = muted
	if a.Added == "" {
		a.Added = time.Now().UTC().Format(time.RFC3339)
	}
	m[strconv.FormatUint(uint64(s.PID), 10)] = a
	if err := writeAdopted(m); err != nil {
		return err
	}
	emit(map[string]interface{}{"pid": s.PID, "polling_muted": muted})
	return nil
}

func cmdName(args []string) error {
	p, err := parseArgs(args)
	if err != nil {
		return err
	}
	if len(p.pos) < 2 {
		return errors.New(`usage: agents-cli name <ref> "label"`)
	}
	s, _, err := lookup(p.pos[0])
	if err != nil {
		return err
	}
	m := readAdopted()
	key := strconv.FormatUint(uint64(s.PID), 10)
	a := m[key]
	a.Name = strings.Join(p.pos[1:], " ")
	if a.Tool == "" {
		a.Tool = s.Tool
	}
	if t := startTime(s.PID); !t.IsZero() {
		a.Start = t.Format(time.RFC3339)
	}
	if a.Added == "" {
		a.Added = time.Now().UTC().Format(time.RFC3339)
	}
	m[key] = a
	if err := writeAdopted(m); err != nil {
		return err
	}
	emit(map[string]interface{}{"pid": s.PID, "name": a.Name, "tool": a.Tool})
	return nil
}

func cmdAdopt(args []string) error {
	p, err := parseArgs(args)
	if err != nil {
		return err
	}
	if len(p.pos) < 1 {
		return errors.New("usage: agents-cli adopt <pid> --tool T [--name L]")
	}
	pid64, err := strconv.ParseUint(p.pos[0], 10, 32)
	if err != nil {
		return fmt.Errorf("adopt needs a numeric pid")
	}
	pid := uint32(pid64)
	if !isAlive(pid) {
		return fmt.Errorf("pid %d is not running", pid)
	}
	tool := strings.ToLower(p.str("tool", ""))
	if tool == "" {
		return errors.New("--tool is required (claude|codex|agy|qwen|opencode|copilot|gemini|vibe|other)")
	}
	m := readAdopted()
	key := p.pos[0]
	a := m[key]
	a.Tool = tool
	if n := p.str("name", ""); n != "" {
		a.Name = n
	}
	if t := startTime(pid); !t.IsZero() {
		a.Start = t.Format(time.RFC3339)
	}
	a.Added = time.Now().UTC().Format(time.RFC3339)
	m[key] = a
	if err := writeAdopted(m); err != nil {
		return err
	}
	emit(map[string]interface{}{"pid": pid, "tool": a.Tool, "name": a.Name, "adopted": true})
	return nil
}
