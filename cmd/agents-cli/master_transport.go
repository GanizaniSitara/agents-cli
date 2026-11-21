package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type masterRequest struct {
	Op   string
	PID  uint32
	ID   uint64
	Text string
}
type masterResponse struct {
	Event *masterEvent `json:"event,omitempty"`
	Lines []string     `json:"lines,omitempty"`
	Error string       `json:"error,omitempty"`
	Sent  bool         `json:"sent,omitempty"`
}

func masterListen(path string, f *masterFeed, reply func(masterRequest) error) (net.Listener, error) {
	if !filepath.IsAbs(path) {
		return nil, errors.New("master socket requires an absolute path in a private local directory")
	}
	l, err := net.Listen("unix", path)
	if err != nil {
		// A crash or power loss leaves the socket file behind; replace it only when nothing answers.
		if c, dialErr := net.DialTimeout("unix", path, time.Second); dialErr == nil {
			c.Close()
			return nil, err
		}
		if os.Remove(path) != nil {
			return nil, err
		}
		if l, err = net.Listen("unix", path); err != nil {
			return nil, err
		}
	}
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go masterHandle(c, f, reply)
		}
	}()
	return l, nil
}
func masterHandle(c net.Conn, f *masterFeed, reply func(masterRequest) error) {
	defer c.Close()
	var q masterRequest
	c.SetReadDeadline(time.Now().Add(5 * time.Second))
	if err := json.NewDecoder(io.LimitReader(c, 32768)).Decode(&q); err != nil {
		return
	}
	c.SetReadDeadline(time.Time{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { var b [1]byte; c.Read(b[:]); cancel() }()
	var r masterResponse
	var err error
	switch q.Op {
	case "wait":
		var ev masterEvent
		ev, err = f.wait(ctx, q.ID)
		if err == nil {
			r.Event = &ev
		}
	case "context":
		r.Lines, err = f.more(q.PID, q.ID)
	case "reply":
		if q.Text == "" {
			err = errors.New("empty reply")
		} else {
			err = reply(q)
			r.Sent = err == nil
		}
	default:
		err = errors.New("unknown master operation")
	}
	if err != nil {
		r.Error = err.Error()
	}
	json.NewEncoder(c).Encode(r)
}
func queueMasterWake(bin, thread string, event masterEvent) (bool, error) {
	// A small pointer keeps cached conversation text out of process arguments.
	note := fmt.Sprintf("Watcher event %d for session PID %d. Read with agents-cli master context %d %d.", event.ID, event.Session.PID, event.Session.PID, event.ID)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if !filepath.IsAbs(bin) || !strings.EqualFold(filepath.Base(bin), "codex.exe") {
		return false, errors.New("expected absolute Codex executable path")
	}
	probe := exec.CommandContext(ctx, bin, "queue", "--help")
	probe.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	help, err := probe.Output()
	if err != nil {
		return false, fmt.Errorf("inspect Codex queue options: %w", err)
	}
	queueArgs := []string{"queue", "--thread", thread, "--message", note}
	resume := false
	for _, line := range strings.Split(string(help), "\n") {
		if strings.TrimSpace(line) == "--resume" {
			resume = true
			queueArgs = append(queueArgs, "--resume")
			break
		}
	}
	cmd := exec.CommandContext(ctx, bin, queueArgs...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	cmd.Stderr = os.Stderr
	if err = cmd.Run(); err != nil {
		return false, err
	}
	return resume, nil
}
func masterClient(args []string) error {
	if len(args) > 0 && (args[0] == "subscribe" || args[0] == "unsubscribe" || args[0] == "subscription-status") {
		return masterSubscriptionCommand(args)
	}
	if len(args) == 4 && args[0] == "wake" {
		// Wake exactly once per invocation; the caller checkpoints the emitted cursor.
		after, err := strconv.ParseUint(args[3], 10, 64)
		if err != nil {
			return err
		}
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		out, err := exec.Command(exe, "master", "wait", strconv.FormatUint(after, 10)).Output()
		if err != nil {
			return err
		}
		var r masterResponse
		if json.Unmarshal(out, &r) != nil || r.Event == nil {
			return errors.New("missing watcher event")
		}
		resume, err := queueMasterWake(args[1], args[2], *r.Event)
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"queued": true, "resume_requested": resume, "cursor": r.Event.ID})
	}
	usage := errors.New("usage: master wait AFTER | master context PID ID | master reply PID ID (text from stdin)")
	if len(args) < 2 {
		return usage
	}
	q := masterRequest{Op: args[0]}
	if q.Op == "wait" && len(args) == 2 {
	} else if (q.Op == "context" || q.Op == "reply") && len(args) == 3 {
		n, err := strconv.ParseUint(args[1], 10, 32)
		if err != nil {
			return err
		}
		q.PID = uint32(n)
	} else {
		return usage
	}
	n, err := strconv.ParseUint(args[len(args)-1], 10, 64)
	if err != nil {
		return err
	}
	q.ID = n
	if q.Op == "reply" {
		b, err := io.ReadAll(io.LimitReader(os.Stdin, 4097))
		if err != nil {
			return err
		}
		if len(b) > 4096 {
			return errors.New("reply exceeds 4096 bytes")
		}
		q.Text = string(b)
	}
	socket := os.Getenv("AGENTS_MASTER_SOCKET")
	if socket == "" {
		dir, err := watchDirectory(parsed{})
		if err != nil {
			return err
		}
		socket = filepath.Join(dir, "master.sock")
	}
	c, err := net.DialTimeout("unix", socket, 2*time.Second)
	if err != nil {
		return err
	}
	defer c.Close()
	if err = json.NewEncoder(c).Encode(q); err != nil {
		return err
	}
	var r masterResponse
	if err = json.NewDecoder(c).Decode(&r); err != nil {
		return err
	}
	if r.Error != "" {
		return errors.New(r.Error)
	}
	return json.NewEncoder(os.Stdout).Encode(r)
}

// Require the same actionable stop in both the subscription and the tracker.
func (a *watchApp) masterDispatch(q masterRequest, send func(watchSample, string) error) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	e := a.tracker.entries[q.PID]
	if e == nil || e.Parked || e.Sample.PollingMuted || !e.Attention || !e.Stopped || e.Dismissed || e.Sample.Source == "adopted" || e.Sample.Tool == "other" {
		return errors.New("session unavailable for reply")
	}
	s, err := a.master.claim(q.PID, q.ID)
	if err != nil {
		return err
	}
	if !watchSameClickSession(s, e.Sample) || s.RootStarted != e.Sample.RootStarted || s.Phase != e.Sample.Phase {
		return errors.New("session changed")
	}
	return send(s, q.Text)
}
func (a *watchApp) masterReply(q masterRequest) error {
	if len(q.Text) > 4096 || strings.TrimSpace(q.Text) == "" {
		return errors.New("reply must be 1 to 4096 bytes")
	}
	ref := strconv.FormatUint(uint64(q.PID), 10)
	if _, err := a.master.more(q.PID, q.ID); err != nil {
		return err
	}
	live, _, err := lookup(ref)
	if err != nil {
		return err
	}
	if !live.Alive || live.PollingMuted || live.Source == "adopted" || live.Tool == "other" || live.AgentPID == 0 {
		return errors.New("target unavailable")
	}
	screen, err := peek(q.PID, 0)
	if err != nil {
		return err
	}
	digest := sha256.Sum256([]byte(strings.Join(screen.Lines, "\n")))
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	var cmd *exec.Cmd
	var output bytes.Buffer
	err = a.masterDispatch(q, func(s watchSample, text string) error {
		if live.AgentPID != s.AgentPID || live.Tool != s.Tool || fmt.Sprintf("%x", digest[:8]) != s.Signature || watchTargetProblem(s, watchReadTarget(s)) != "" || startTime(s.AgentPID).Format(time.RFC3339Nano) != s.NamingAgentStarted {
			return errors.New("target changed since stop")
		}
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		cmd = exec.CommandContext(ctx, exe, "send", ref, "--stdin")
		cmd.Stdin = strings.NewReader(text)
		cmd.Stdout = &output
		return cmd.Start()
	})
	if err != nil {
		return err
	}
	err = cmd.Wait()
	if err != nil {
		return fmt.Errorf("send failed; inspect target before retry: %w", err)
	}
	var receipt struct{ Submitted bool }
	if json.Unmarshal(output.Bytes(), &receipt) != nil || !receipt.Submitted {
		return errors.New("submission unconfirmed; inspect target")
	}
	return nil
}
