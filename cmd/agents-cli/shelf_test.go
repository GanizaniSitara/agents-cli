package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestShelfDurableSaveBeforeCloseAndFailure(t *testing.T) {
	for _, closeFails := range []bool{false, true} {
		dir := t.TempDir()
		entry := shelfEntry{ID: "test", Name: "Review", Tool: "codex", Conversation: "verified-id"}
		err := shelfSaveResolved(dir, entry, func(got shelfEntry) error {
			entries, err := shelfRead(dir)
			if err != nil || len(entries) != 1 || entries[0].Conversation != got.Conversation || entries[0].State != "closing" {
				t.Fatal("close ran before durable resume details existed")
			}
			if closeFails {
				return errors.New("native exit refused")
			}
			return nil
		})
		entries, readErr := shelfRead(dir)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if closeFails {
			if err == nil || entries[0].State != "window-open" || entries[0].Problem == "" {
				t.Fatal("lost failed-close recovery")
			}
		} else if err != nil || entries[0].State != "shelved" {
			t.Fatalf("%v %+v", err, entries)
		}
	}
	path := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(path, []byte("file"), 0600); err != nil {
		t.Fatal(err)
	}
	closed := false
	if err := shelfSaveResolved(path, shelfEntry{ID: "fail"}, func(shelfEntry) error { closed = true; return nil }); err == nil || closed {
		t.Fatal("save failure closed the session")
	}
}

func TestShelfConcurrentWritersAndCorruptStore(t *testing.T) {
	dir := t.TempDir()
	var group sync.WaitGroup
	for i := 0; i < 8; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			if err := shelfUpdate(dir, func(entries *[]shelfEntry) error {
				*entries = append(*entries, shelfEntry{ID: shelfID(), Name: "Saved"})
				return nil
			}); err != nil {
				t.Error(err)
			}
		}()
	}
	group.Wait()
	entries, err := shelfRead(dir)
	if err != nil || len(entries) != 8 {
		t.Fatalf("lost shelf entries: %d %v", len(entries), err)
	}
	if err = os.WriteFile(shelfPath(dir), []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	called := false
	if err = shelfUpdate(dir, func(*[]shelfEntry) error { called = true; return nil }); err == nil || called {
		t.Fatal("corrupt store was overwritten")
	}
}

func TestShelfDuplicateResumeDoesNotLaunch(t *testing.T) {
	dir := t.TempDir()
	pid := uint32(os.Getpid())
	entry := shelfEntry{ID: "already-open", Tool: "codex", Conversation: "verified", State: "resumed", ResumedPID: pid, ResumedStart: startTime(pid).Format(time.RFC3339Nano), Exe: "must-not-launch.exe"}
	if err := shelfStore(dir, entry); err != nil {
		t.Fatal(err)
	}
	if err := shelfResume(dir, entry.ID); err == nil || !strings.Contains(err.Error(), "already open") {
		t.Fatalf("duplicate resume: %v", err)
	}
	if shelfAlive(pid, "old-start") {
		t.Fatal("reused PID considered same session")
	}
}

func TestShelfStatusIdentityAndSearch(t *testing.T) {
	t.Setenv("USERNAME", "test-user")
	if got := watchShortName("Mail | test-user"); got != "Mail" {
		t.Fatal("user suffix leaked into saved short name")
	}
	id := "12345678-1234-5678-1234-123456789abc"
	if got := shelfStatusID([]string{"Historical prose mentions " + id, "│ Session: " + id + " │"}); got != id {
		t.Fatalf("got %q", got)
	}
	if got := shelfStatusID([]string{"Earlier session was " + id}); got != "" {
		t.Fatal("historical prose became identity")
	}
	if got := shelfStatusID([]string{"Session: " + id, "Session: " + id}); got != "" {
		t.Fatal("ambiguous identity accepted")
	}
	rows := watchShelfVisible([]shelfEntry{{Name: "Mail", Tool: "codex", Directory: "C:/project", State: "shelved"}, {Name: "Mail", State: "cancelled"}, {Name: "Search", State: "shelved"}}, "CODEX")
	if len(rows) != 1 || rows[0].Name != "Mail" {
		t.Fatal("Shelf search included wrong sessions")
	}
}

func TestShelfReShelveReplacesSameConversation(t *testing.T) {
	dir := t.TempDir()
	old := shelfEntry{ID: "canonical", Conversation: "same-thread", Tool: "codex", State: "resumed", Source: watchSample{Session: Session{PID: 123}, RootStarted: "old"}}
	if err := shelfStore(dir, old); err != nil {
		t.Fatal(err)
	}
	next := old
	next.State = "closing"
	next.Source.PID = 456
	next.Source.RootStarted = "new"
	if err := shelfStore(dir, next); err != nil {
		t.Fatal(err)
	}
	entries, _ := shelfRead(dir)
	if len(entries) != 1 || entries[0].Source.PID != 456 {
		t.Fatal("re-shelving duplicated conversation")
	}
	if err := shelfStore(dir, next); err == nil {
		t.Fatal("concurrent close claim accepted")
	}
}

func TestShelfInterruptedPendingRecoveryAndIdentityGuard(t *testing.T) {
	dir := t.TempDir()
	e := shelfEntry{ID: "pending", State: "preparing", Source: watchSample{Session: Session{PID: 123, AgentPID: 124}, RootStarted: "original", NamingAgentStarted: "agent"}}
	if err := shelfStore(dir, e); err != nil {
		t.Fatal(err)
	}
	if err := shelfTick(dir); err != nil {
		t.Fatal(err)
	}
	entries, _ := shelfRead(dir)
	if entries[0].State != "pending" {
		t.Fatal("interrupted preparation was not recoverable")
	}
	replacement := e.Source
	replacement.RootStarted = "replacement"
	if shelfSameSource(e.Source, replacement) {
		t.Fatal("PID reuse inherited shelving request")
	}
}

func TestShelfHostExitNeverAppendsToDraft(t *testing.T) {
	if !shelfShellEmpty([]string{`C:\project>`}, `C:\project\`) {
		t.Fatal("empty host prompt not recognized")
	}
	for _, lines := range [][]string{{`C:\project> important-command`}, {`C:\project>`, `a draft`}, {`other prompt`}, nil} {
		if shelfShellEmpty(lines, `C:\project`) {
			t.Fatal("host draft or unknown prompt accepted")
		}
	}
}

func TestWatchRowMenuParkUnparkNative(t *testing.T) {
	for _, parked := range []bool{false, true} {
		menu := watchRowMenu(parked)
		if menu == 0 {
			t.Fatal("no menu")
		}
		var text [64]uint16
		watchUser.NewProc("GetMenuStringW").Call(menu, 0, uintptr(unsafe.Pointer(&text[0])), 64, 0x400)
		if windows.UTF16ToString(text[:]) != "Rename…" {
			t.Fatal("Rename must be first")
		}
		id, _, _ := watchUser.NewProc("GetMenuItemID").Call(menu, 1)
		if id != watchRowShelve {
			t.Fatal("wrong Shelve action")
		}
		count, _, _ := watchUser.NewProc("GetMenuItemCount").Call(menu)
		if count != 3 {
			t.Fatal("menu must contain Rename, Shelve and cancel shelving only")
		}
		watchUser.NewProc("DestroyMenu").Call(menu)
	}
}

func TestShelfNativeSearchAndCloseWithoutFocusChange(t *testing.T) {
	dir := t.TempDir()
	if err := shelfStore(dir, shelfEntry{ID: "saved", Name: "Mail", Tool: "codex", State: "shelved"}); err != nil {
		t.Fatal(err)
	}
	foreground, _, _ := watchUser.NewProc("GetForegroundWindow").Call()
	done := make(chan struct{})
	go func() {
		defer close(done)
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			h, _, _ := watchUser.NewProc("FindWindowW").Call(uintptr(unsafe.Pointer(windows.StringToUTF16Ptr("AgentsCLIShelf"))), 0)
			if h != 0 && watchWindowPID(h) == uint32(os.Getpid()) && watchDialogItem(h, 3) != 0 {
				list := watchDialogItem(h, 11)
				count, _, _ := watchUser.NewProc("SendMessageW").Call(list, 0x18b, 0, 0)
				if count != 1 {
					time.Sleep(10 * time.Millisecond)
					continue
				}
				edit := watchDialogItem(h, 10)
				watchUser.NewProc("SetWindowTextW").Call(edit, uintptr(unsafe.Pointer(windows.StringToUTF16Ptr("missing"))))
				watchUser.NewProc("SendMessageW").Call(h, 0x111, 10|(0x300<<16), edit)
				count, _, _ = watchUser.NewProc("SendMessageW").Call(list, 0x18b, 0, 0)
				if count != 0 {
					t.Error("native search did not filter")
				}
				watchUser.NewProc("PostMessageW").Call(h, 0x111, 2, 0)
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()
	if err := watchShelfDialog(dir, false); err != nil {
		t.Fatal(err)
	}
	<-done
	after, _, _ := watchUser.NewProc("GetForegroundWindow").Call()
	if after != foreground {
		t.Fatal("hidden validation stole focus")
	}
}
