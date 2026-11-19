package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMasterTransportCLI(t *testing.T) {
	bin := os.Getenv("MASTER_SMOKE_EXE")
	if bin == "" {
		t.Skip("set MASTER_SMOKE_EXE to the isolated CLI binary")
	}
	socket := filepath.Join(t.TempDir(), "m.sock")
	app := &watchApp{master: newMasterFeed(), tracker: newWatchTracker(time.Second)}
	var sent string
	fakeSend := func(s watchSample, text string) error { sent = text; return nil }
	l, err := masterListen(socket, app.master, func(q masterRequest) error { return app.masterDispatch(q, fakeSend) })
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	run := func(input string, args ...string) (masterResponse, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, bin, append([]string{"master"}, args...)...)
		cmd.Env = append(os.Environ(), "AGENTS_MASTER_SOCKET="+socket)
		cmd.Stdin = strings.NewReader(input)
		b, err := cmd.Output()
		var response masterResponse
		if err == nil {
			err = json.Unmarshal(b, &response)
		}
		return response, err
	}
	add := func(pid uint32, mirror bool) {
		e := &watchEntry{Attention: true, Stopped: true, Sample: watchSample{Session: Session{PID: pid, Tool: "codex"}, RootStarted: "fixture"}}
		app.tracker.entries[pid] = e
		app.master.observe(*e, []string{"Question from fixture", "› ignored"}, mirror)
	}
	add(11, false)
	r, err := run("", "wait", "0")
	if err != nil || r.Event == nil || r.Event.ID != 1 {
		t.Fatal(r, err)
	}
	r, err = run("", "context", "11", "1")
	if err != nil || len(r.Lines) != 1 {
		t.Fatal(r, err)
	}
	app.tracker.entries[11].Parked = true
	if _, err = run("fake", "reply", "11", "1"); err == nil {
		t.Fatal("parked reply accepted")
	}
	add(12, true)
	r, err = run("", "wait", "1")
	if err != nil || r.Event == nil || !r.Event.ReadOnly {
		t.Fatal(r, err)
	}
	if _, err = run("fake", "reply", "12", "2"); err == nil {
		t.Fatal("mirror reply accepted")
	}
	add(13, false)
	r, err = run("fake", "reply", "13", "3")
	if err != nil || !r.Sent || sent != "fake" {
		t.Fatal(r, err)
	}
	if _, err = run("repeat", "reply", "13", "3"); err == nil {
		t.Fatal("duplicate reply accepted")
	}
}

func TestOfflineMasterFeed(t *testing.T) {
	f := newMasterFeed()
	e := watchEntry{Stopped: true, Attention: true, Sample: watchSample{Session: Session{PID: 1}, RootStarted: "first"}}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	f.observe(e, []string{"one", "two", "three", "four", "five", "› Ask Codex to do anything"}, false)
	a, err := f.wait(ctx, 0)
	if err != nil || len(a.Lines) != 4 || a.Lines[0] != "two" {
		t.Fatal(a, err)
	}
	f.observe(e, []string{"cached context"}, false)
	lines, err := f.more(1, a.ID)
	if err != nil || len(lines) != 1 || lines[0] != "cached context" {
		t.Fatal(lines, err)
	}
	quiet, stop := context.WithTimeout(ctx, 15*time.Millisecond)
	defer stop()
	if _, err = f.wait(quiet, a.ID); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("duplicate event", err)
	}
	e.Parked = true
	f.observe(e, nil, false)
	if _, err = f.claim(1, a.ID); err == nil {
		t.Fatal("parked reply")
	}
	e.Parked = false
	e.Sample.PID = 2
	f.observe(e, []string{"mirror question"}, true)
	b, err := f.wait(ctx, a.ID)
	if err != nil || !b.ReadOnly || b.Session.PID != 2 {
		t.Fatal(b, err)
	}
	if _, err = f.claim(2, b.ID); err == nil {
		t.Fatal("mirror reply")
	}
	e.Sample.PID = 3
	f.observe(e, nil, false)
	c, err := f.wait(ctx, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.claim(3, c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = f.claim(3, c.ID); err == nil {
		t.Fatal("duplicate reply")
	}
	for _, reason := range []string{"resumed", "muted", "resolved", "replaced", "removed"} {
		e.Sample.PID++
		f.observe(e, nil, false)
		old := f.next
		changed := e
		switch reason {
		case "resumed":
			changed.Stopped = false
		case "muted":
			changed.Sample.PollingMuted = true
		case "resolved":
			changed.Attention = false
		case "replaced":
			changed.Sample.RootStarted = "replacement"
		}
		if reason == "removed" {
			f.forget(e.Sample.PID)
		} else {
			f.observe(changed, nil, false)
		}
		if _, err = f.claim(e.Sample.PID, old); err == nil {
			t.Fatal(reason)
		}
	}
}
