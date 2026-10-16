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
	TS        string   `json:"ts"`
	PID       uint32   `json:"pid"`
	Tool      string   `json:"tool"`
	Source    string   `json:"source"`
	Killed    []uint32 `json:"killed"`
	Failed    []string `json:"failed,omitempty"`
	CallerPID uint32   `json:"caller_pid"`
	Result    string   `json:"result"`
}

func stopsPath() string {
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		base = dataDir()
	}
	return filepath.Join(base, "agents-cli", "stops.jsonl")
}

func logStop(rec stopRecord) error {
	_, err := rewriteStops(&rec)
	return err
}

func rewriteStops(add *stopRecord) (int, error) {
	if err := os.MkdirAll(filepath.Dir(stopsPath()), 0700); err != nil {
		return 0, err
	}
	lock, err := os.OpenFile(stopsPath()+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return 0, err
	}
	defer lock.Close()
	var overlapped windows.Overlapped
	if err := windows.LockFileEx(windows.Handle(lock.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK, 0, 1, 0, &overlapped); err != nil {
		return 0, err
	}
	defer windows.UnlockFileEx(windows.Handle(lock.Fd()), 0, 1, 0, &overlapped)
	records, err := retainedStops(time.Now())
	if err != nil {
		return 0, err
	}
	if add != nil {
		records = append(records, *add)
	}
	return len(records), writeStops(records)
}

// Decoding into stopRecord drops legacy names, titles and command lines.
func retainedStops(now time.Time) ([]stopRecord, error) {
	f, err := os.Open(stopsPath())
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var records []stopRecord
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 4096), 1024*1024)
	for sc.Scan() {
		var rec stopRecord
		if err := json.Unmarshal(sc.Bytes(), &rec); err != nil {
			return nil, errors.New("stop log contains invalid JSON; no records changed")
		}
		ts, err := time.Parse(time.RFC3339, rec.TS)
		if err == nil && !ts.Before(now.Add(-30*24*time.Hour)) {
			records = append(records, rec)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, errors.New("cannot read stop log; no records changed")
	}
	return records, nil
}

func writeStops(records []stopRecord) error {
	if err := os.MkdirAll(filepath.Dir(stopsPath()), 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(stopsPath()), ".stops-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	for _, rec := range records {
		line, err := json.Marshal(rec)
		if err != nil {
			f.Close()
			return err
		}
		if _, err := f.Write(append(line, '\n')); err != nil {
			f.Close()
			return err
		}
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), stopsPath())
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
		TS: time.Now().UTC().Format(time.RFC3339), PID: s.PID, Tool: s.Tool,
		Source: s.Source, CallerPID: self,
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

func terminate(pid uint32) error {
	h, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, pid)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(h)
	return windows.TerminateProcess(h, 1)
}

func cmdStops(args []string) error {
	p, err := parseArgs(args, "scrub")
	if err != nil {
		return err
	}
	if p.boolean("scrub") {
		count, err := rewriteStops(nil)
		if err != nil {
			return err
		}
		emit(map[string]interface{}{"scrubbed": true, "retained": count, "retention_days": 30})
		return nil
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
