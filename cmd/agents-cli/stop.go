package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/windows"
)

// stopRecord is one line of stops.jsonl. The file answers "what got stopped,
// when, by whom" until TECH-182 gives the box a real log sink; keep the writer
// behind logStop so that swap is one edit.
type stopRecord struct {
	TS            string   `json:"ts"`
	PID           uint32   `json:"pid"`
	Tool          string   `json:"tool"`
	Name          string   `json:"name,omitempty"`
	Title         string   `json:"title,omitempty"`
	Source        string   `json:"source"`
	Killed        []uint32 `json:"killed"`
	Failed        []string `json:"failed,omitempty"`
	CallerPID     uint32   `json:"caller_pid"`
	CallerCmdline string   `json:"caller_cmdline,omitempty"`
	Result        string   `json:"result"`
}

func stopsPath() string {
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		base = dataDir()
	}
	return filepath.Join(base, "agents-cli", "stops.jsonl")
}

func logStop(rec stopRecord) error {
	if err := os.MkdirAll(filepath.Dir(stopsPath()), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(stopsPath(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	line, _ := json.Marshal(rec)
	_, err = f.Write(append(line, '\n'))
	return err
}

func cmdStop(args []string) error {
	p, err := parseArgs(args)
	if err != nil {
		return err
	}
	if len(p.pos) < 1 {
		return errors.New("usage: agents-cli stop <ref> --confirm <pid>")
	}
	s, procs, err := lookup(p.pos[0])
	if err != nil {
		return err
	}
	confirm := p.str("confirm", "")
	if confirm == "" {
		emit(map[string]interface{}{
			"would_stop": s, "descendants": procs.descendants(s.PID),
			"hint": fmt.Sprintf("re-run with --confirm %d to kill this process tree", s.PID),
		})
		return nil
	}
	if confirm != strconv.FormatUint(uint64(s.PID), 10) {
		return fmt.Errorf("--confirm %s does not match the resolved session pid %d; nothing stopped", confirm, s.PID)
	}
	self := uint32(windows.GetCurrentProcessId())
	if s.PID == procs.selfRoot() || s.PID == self {
		return errors.New("refusing to stop the caller's own session")
	}
	for _, d := range procs.descendants(s.PID) {
		if d == self {
			return errors.New("refusing to stop the caller's own session")
		}
	}

	rec := stopRecord{
		TS: time.Now().UTC().Format(time.RFC3339), PID: s.PID, Tool: s.Tool, Name: s.Name,
		Title: s.Title, Source: s.Source, CallerPID: self, CallerCmdline: callerCmdline(procs, self),
	}
	victims := append(procs.descendants(s.PID), s.PID)
	for _, pid := range victims {
		if err := terminate(pid); err != nil {
			rec.Failed = append(rec.Failed, fmt.Sprintf("%d: %v", pid, err))
		} else {
			rec.Killed = append(rec.Killed, pid)
		}
	}
	if rec.Killed == nil {
		rec.Killed = []uint32{}
	}
	switch {
	case len(rec.Failed) == 0:
		rec.Result = "ok"
	case len(rec.Killed) == 0:
		rec.Result = "failed"
	default:
		rec.Result = "partial"
	}
	logErr := logStop(rec)
	out := map[string]interface{}{"stopped": rec, "log": stopsPath()}
	if logErr != nil {
		out["log_error"] = logErr.Error()
	}
	emit(out)
	if rec.Result == "failed" {
		return errors.New("no process could be terminated")
	}
	return nil
}

// callerCmdline walks up from agents-cli to the first non-shell ancestor so
// the log says which agent (or human shell) asked for the kill.
func callerCmdline(procs procMap, self uint32) string {
	shells := map[string]bool{"agents-cli.exe": true, "bash.exe": true, "sh.exe": true, "cmd.exe": true, "powershell.exe": true, "pwsh.exe": true}
	for cur, ok := procs[self]; ok; cur, ok = procs[cur.PPID] {
		if !shells[cur.Name] {
			cmd := commandLine(cur.PID)
			if cmd == "" {
				cmd = cur.Name
			}
			if len(cmd) > 200 {
				cmd = cmd[:200]
			}
			return fmt.Sprintf("%d %s", cur.PID, cmd)
		}
		if cur.PPID == 0 || cur.PPID == cur.PID {
			break
		}
	}
	return ""
}

func terminate(pid uint32) error {
	h, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, pid)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(h)
	return windows.TerminateProcess(h, 1)
}

func cmdStops(args []string) error {
	p, err := parseArgs(args)
	if err != nil {
		return err
	}
	since, err := p.durationFlag("since", 0)
	if err != nil {
		return err
	}
	var cutoff time.Time
	if since > 0 {
		cutoff = time.Now().Add(-since)
	}
	f, err := os.Open(stopsPath())
	if err != nil {
		if os.IsNotExist(err) {
			emit(map[string]interface{}{"count": 0, "stops": []stopRecord{}, "log": stopsPath()})
			return nil
		}
		return err
	}
	defer f.Close()
	var recs []stopRecord
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var r stopRecord
		if json.Unmarshal([]byte(line), &r) != nil {
			continue
		}
		if !cutoff.IsZero() {
			if t, err := time.Parse(time.RFC3339, r.TS); err == nil && t.Before(cutoff) {
				continue
			}
		}
		recs = append(recs, r)
	}
	if recs == nil {
		recs = []stopRecord{}
	}
	emit(map[string]interface{}{"count": len(recs), "stops": recs, "log": stopsPath()})
	return nil
}
