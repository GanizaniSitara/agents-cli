package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func auditTestHome(t *testing.T) {
	t.Helper()
	t.Setenv("LOCALAPPDATA", t.TempDir())
	t.Cleanup(func() { activeSendAudit.Lock(); activeSendAudit.audit = nil; activeSendAudit.Unlock() })
}

func TestSendAuditHardDeadlineRecordsTimeout(t *testing.T) {
	auditTestHome(t)
	t.Setenv("AGENTS_CLI_TEST_SEND_AUDIT_TIMEOUT", "1")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0])
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.DETACHED_PROCESS, HideWindow: true}
	if err := cmd.Run(); err == nil || ctx.Err() != nil {
		t.Fatalf("hard deadline failed: %v", err)
	}
	r := readAuditTest(t, sendsPath())
	if len(r) != 3 || r[2].Outcome != "timeout" || r[2].Submitted != nil {
		t.Fatalf("hard timeout not recorded: %+v", r)
	}
}

func readAuditTest(t *testing.T, path string) []sendAuditRecord {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	scan := bufio.NewScanner(f)
	var records []sendAuditRecord
	for scan.Scan() {
		var r sendAuditRecord
		if err := json.Unmarshal(scan.Bytes(), &r); err != nil {
			t.Fatal(err)
		}
		records = append(records, r)
	}
	if err := scan.Err(); err != nil {
		t.Fatal(err)
	}
	return records
}

func TestSendAuditMetadataAndPrivacy(t *testing.T) {
	auditTestHome(t)
	a := startSendAudit("send")
	a.input(123, true, true, "")
	s := &Session{PID: 123, AgentPID: 124, Tool: "codex", Started: "2026-11-10T12:00:00Z", ThreadID: "01a16f6f-e31f-7161-a3a5-3966f6bfc355", Name: privateTestText, Title: privateTestText, Exe: privateTestText, Home: privateTestText}
	a.resolve(s, procMap{a.record.ParentPID: &proc{PID: a.record.ParentPID, Name: "cmd.exe", Cmd: privateTestText}})
	if err := a.dispatch("inject"); err != nil {
		t.Fatal(err)
	}
	if err := auditKeyDispatch(s.PID, s.Tool, "enter"); err != nil {
		t.Fatal(err)
	}
	a.finish(errors.New(privateTestText), false)
	records := readAuditTest(t, sendsPath())
	if len(records) != 5 || records[2].Phase != "dispatch" || records[3].Phase != "key_dispatch" || records[4].Outcome != "delivery_uncertain" {
		t.Fatalf("events: %+v", records)
	}
	if records[4].PID != 123 || records[4].ThreadID != s.ThreadID || !records[4].Force || records[4].Bytes != 123 || records[4].CallerPID == 0 || records[4].ParentPID == 0 {
		t.Fatal("missing provenance")
	}
	b, _ := os.ReadFile(sendsPath())
	if strings.Contains(string(b), privateTestText) {
		t.Fatal("audit leaked message/error/title/path")
	}
	for _, r := range records {
		if r.ID != records[0].ID || r.Time.IsZero() {
			t.Fatal("lost correlation/time")
		}
	}
}

func TestSendAuditTimeoutAndUnknownSubmission(t *testing.T) {
	auditTestHome(t)
	a := startSendAudit("send")
	a.input(9, true, false, "")
	if err := a.dispatch("inject"); err != nil {
		t.Fatal(err)
	}
	a.finish(errors.New("timeout"), true)
	a.finish(nil, false)
	r := readAuditTest(t, sendsPath())
	if len(r) != 3 || r[2].Outcome != "timeout" || r[2].Submitted != nil {
		t.Fatal("timeout claims delivery or was overwritten")
	}
	if err := a.dispatch("inject"); err == nil {
		t.Fatal("ended operation reused")
	}
}

func TestSendAuditBlocksUnauditedDispatch(t *testing.T) {
	auditTestHome(t)
	path := sendsPath()
	if err := os.MkdirAll(path, 0700); err != nil {
		t.Fatal(err)
	}
	a := startSendAudit("send")
	if err := a.dispatch("inject"); err == nil || a.dispatched {
		t.Fatal("allowed unlogged delivery")
	}
}

func TestSendAuditConcurrentAppendAndRotation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sends.jsonl")
	var group sync.WaitGroup
	errs := make(chan error, 16)
	for i := 0; i < 16; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			errs <- appendSendAudit(path, sendAuditRecord{ID: "test", Time: time.Now()})
		}()
	}
	group.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(readAuditTest(t, path)) != 16 {
		t.Fatal("concurrent events lost")
	}
	if err := os.WriteFile(path, make([]byte, sendAuditLimit), 0600); err != nil {
		t.Fatal(err)
	}
	if err := appendSendAudit(path, sendAuditRecord{ID: "after rotation"}); err != nil {
		t.Fatal(err)
	}
	if len(readAuditTest(t, path)) != 1 {
		t.Fatal("rotation failed")
	}
	if info, err := os.Stat(strings.TrimSuffix(path, ".jsonl") + ".previous.jsonl"); err != nil || info.Size() != sendAuditLimit {
		t.Fatal("previous log missing")
	}
}

func TestSendAuditMalformedRequestRecordedWithoutText(t *testing.T) {
	auditTestHome(t)
	if err := cmdSend([]string{"123", privateTestText, "--stdin"}); err == nil {
		t.Fatal("invalid request accepted")
	}
	r := readAuditTest(t, sendsPath())
	if len(r) != 2 || r[1].Outcome != "refused" || r[1].PID != 0 {
		t.Fatal("refusal missing")
	}
	b, _ := os.ReadFile(sendsPath())
	if strings.Contains(string(b), privateTestText) {
		t.Fatal("invalid request leaked")
	}
}
