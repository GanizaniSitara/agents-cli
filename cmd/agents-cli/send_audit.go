package main

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/windows"
)

const sendAuditLimit = 4 << 20

type sendAuditRecord struct {
	Time            time.Time `json:"time"`
	ID              string    `json:"id"`
	Phase           string    `json:"phase"`
	Operation       string    `json:"operation"`
	Build           string    `json:"build"`
	CallerPID       uint32    `json:"caller_pid"`
	ParentPID       uint32    `json:"parent_pid"`
	CallerAgentPID  uint32    `json:"caller_agent_pid,omitempty"`
	CallerTool      string    `json:"caller_tool,omitempty"`
	CallerStarted   string    `json:"caller_agent_started,omitempty"`
	CallerSessionID string    `json:"caller_session_id,omitempty"`
	PID             uint32    `json:"target_pid,omitempty"`
	AgentPID        uint32    `json:"target_agent_pid,omitempty"`
	Tool            string    `json:"target_tool,omitempty"`
	Started         string    `json:"target_started,omitempty"`
	ThreadID        string    `json:"target_thread_id,omitempty"`
	Via             string    `json:"via,omitempty"`
	Enter           bool      `json:"enter"`
	Force           bool      `json:"force"`
	Bytes           int       `json:"message_bytes,omitempty"`
	Key             string    `json:"key,omitempty"`
	Outcome         string    `json:"outcome,omitempty"`
	Submitted       *bool     `json:"submitted,omitempty"`
	ErrorCode       string    `json:"error_code,omitempty"`
}

type sendAudit struct {
	mu                   sync.Mutex
	record               sendAuditRecord
	stage                string
	dispatched, finished bool
	writeError           bool
}

var activeSendAudit struct {
	sync.Mutex
	audit *sendAudit
}

func sendsPath() string {
	base, _ := os.UserCacheDir()
	return filepath.Join(base, "agents-cli", "sends.jsonl")
}

func startSendAudit(operation string) *sendAudit {
	var id [16]byte
	identity := hex.EncodeToString(id[:])
	if _, err := rand.Read(id[:]); err == nil {
		identity = hex.EncodeToString(id[:])
	} else {
		identity = fmt.Sprintf("%d-%d", os.Getpid(), time.Now().UnixNano())
	}
	a := &sendAudit{stage: "parse", record: sendAuditRecord{ID: identity, Operation: operation, Build: buildCommit, CallerPID: uint32(os.Getpid()), ParentPID: uint32(os.Getppid())}}
	activeSendAudit.Lock()
	activeSendAudit.audit = a
	activeSendAudit.Unlock()
	a.mu.Lock()
	a.write("request")
	a.mu.Unlock()
	return a
}

func (a *sendAudit) write(phase string) {
	a.record.Time, a.record.Phase = time.Now().UTC(), phase
	if err := appendSendAudit(sendsPath(), a.record); err != nil {
		a.writeError = true
	}
}

func (a *sendAudit) input(bytes int, enter, force bool, key string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.finished {
		return
	}
	a.record.Bytes, a.record.Enter, a.record.Force, a.record.Key = bytes, enter, force, key
	a.stage = "resolve"
}

func (a *sendAudit) resolve(s *Session, procs procMap) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.finished {
		return
	}
	a.stage = "validate"
	a.record.PID, a.record.AgentPID, a.record.Tool, a.record.Started = s.PID, s.AgentPID, s.Tool, s.Started
	a.record.ThreadID = firstNonEmpty(s.ThreadID, s.SessionID)
	seen := map[uint32]bool{}
	for p, hops := procs[a.record.ParentPID], 0; p != nil && hops < 32 && !seen[p.PID]; p, hops = procs[p.PPID], hops+1 {
		seen[p.PID] = true
		started := startTime(p.PID)
		if started.IsZero() || started.After(a.record.Time) {
			break
		}
		if tool := classify(p); tool != "" && p.Name != "cmd.exe" && p.Name != "powershell.exe" && p.Name != "pwsh.exe" {
			a.record.CallerAgentPID, a.record.CallerTool = p.PID, tool
			a.record.CallerStarted = started.UTC().Format(time.RFC3339Nano)
			if tool == "claude" {
				a.record.CallerSessionID = callerSessionID(procs)
			}
			break
		}
	}
	a.write("resolved")
}

func (a *sendAudit) dispatch(via string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.finished {
		return errors.New("send operation already ended")
	}
	a.record.Via, a.stage = via, "dispatch"
	a.write("dispatch")
	if a.writeError {
		return errors.New("cannot record send audit; nothing sent")
	}
	a.dispatched = true
	return nil
}

func (a *sendAudit) finish(err error, timeout bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.finished {
		return
	}
	a.finished = true
	switch {
	case timeout:
		a.record.Outcome, a.record.ErrorCode = "timeout", "command_timeout"
	case err != nil:
		a.record.Outcome, a.record.ErrorCode = "refused", a.stage+"_failed"
		if a.dispatched {
			a.record.Outcome = "delivery_uncertain"
		}
	case a.record.Operation == "key":
		a.record.Outcome = "key_delivered"
	case !a.record.Enter:
		a.record.Outcome = "typed_without_enter"
	case a.record.Submitted != nil && *a.record.Submitted:
		a.record.Outcome = "submitted"
	default:
		a.record.Outcome = "submission_unverified"
	}
	a.write("result")
}

func (a *sendAudit) sent(s *Session, via string, enter, submitted bool) {
	a.mu.Lock()
	if a.finished {
		a.mu.Unlock()
		return
	}
	if enter {
		a.record.Submitted = &submitted
	}
	a.mu.Unlock()
	a.finish(nil, false)
	out := sendReceipt(s, via, enter, submitted)
	a.addReceipt(out)
	emit(out)
}

func (a *sendAudit) addReceipt(out map[string]interface{}) {
	a.mu.Lock()
	defer a.mu.Unlock()
	out["audit_id"], out["audit_log"] = a.record.ID, sendsPath()
	if a.writeError {
		out["audit_error"] = "audit_write_failed"
	}
}

func currentSendAudit() *sendAudit {
	activeSendAudit.Lock()
	defer activeSendAudit.Unlock()
	return activeSendAudit.audit
}

func auditKeyDispatch(pid uint32, tool, key string) error {
	a := currentSendAudit()
	if a == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.record.PID != pid || a.record.Tool != tool {
		return nil
	}
	if a.finished {
		return errors.New("operation already ended; key not sent")
	}
	previous := a.record.Key
	a.record.Key = key
	a.write("key_dispatch")
	a.record.Key = previous
	if a.writeError {
		return errors.New("cannot record key audit; key not sent")
	}
	return nil
}

// A short cross-process lock protects append and rotation. Failure is bounded;
// callers refuse before delivery if an attempt cannot be recorded.
func withSendAuditLock(path string, fn func() error) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	var overlap windows.Overlapped
	deadline := time.Now().Add(250 * time.Millisecond)
	for {
		err = windows.LockFileEx(windows.Handle(lock.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &overlap)
		if err == nil {
			break
		}
		if err != windows.ERROR_LOCK_VIOLATION || time.Now().After(deadline) {
			return err
		}
		time.Sleep(10 * time.Millisecond)
	}
	defer windows.UnlockFileEx(windows.Handle(lock.Fd()), 0, 1, 0, &overlap)
	return fn()
}

func appendSendAudit(path string, record sendAuditRecord) error {
	b, err := json.Marshal(record)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return withSendAuditLock(path, func() error {
		if info, err := os.Stat(path); err == nil && info.Size()+int64(len(b)) > sendAuditLimit {
			previous := strings.TrimSuffix(path, ".jsonl") + ".previous.jsonl"
			if err := os.Remove(previous); err != nil && !os.IsNotExist(err) {
				return err
			}
			if err := os.Rename(path, previous); err != nil {
				return err
			}
		}
		f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = f.Write(b)
		return err
	})
}

func cmdSends(args []string) error {
	p, err := parseArgs(args)
	if err != nil {
		return err
	}
	since, err := p.durationFlag("since", 0)
	if err != nil || since < 0 {
		return errors.New("--since must be a nonnegative duration")
	}
	limit, err := p.intFlag("limit", 50)
	if err != nil || limit < 1 || limit > 1000 {
		return errors.New("--limit must be between 1 and 1000")
	}
	records := []sendAuditRecord{}
	path := sendsPath()
	err = withSendAuditLock(path, func() error {
		for _, name := range []string{strings.TrimSuffix(path, ".jsonl") + ".previous.jsonl", path} {
			f, err := os.Open(name)
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				return err
			}
			scan := bufio.NewScanner(f)
			for scan.Scan() {
				var record sendAuditRecord
				if json.Unmarshal(scan.Bytes(), &record) != nil {
					continue
				}
				if since > 0 && record.Time.Before(time.Now().Add(-since)) {
					continue
				}
				records = append(records, record)
				if len(records) > limit {
					records = records[1:]
				}
			}
			err = scan.Err()
			f.Close()
			if err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("cannot read send audit: %w", err)
	}
	emit(map[string]interface{}{"count": len(records), "events": records, "log": path})
	return nil
}
