package main

import "testing"

// TECH-211 fixtures: window 2 was launched for task tech-211-x (claude child 4
// carrying the task slug in its prompt); pids 10-12 are a background session
// (daemon -> pty-host -> claude) with agents-cli (99) as its descendant, i.e.
// the caller.
func tech211Procs() procMap {
	return procMap{
		1:  {PID: 1, PPID: 0, Name: "explorer.exe"},
		2:  {PID: 2, PPID: 1, Name: "cmd.exe", Cmd: `cmd /k c:\temp\launch-tech-211-x.bat`},
		3:  {PID: 3, PPID: 2, Name: "claude-console-filter.exe"},
		4:  {PID: 4, PPID: 3, Name: "claude.exe", Cmd: `claude.exe --dangerously-skip-permissions --name "TECH-211" "read the task file at c:\users\admin\tasks\in-progress\tech-211-x.md"`},
		10: {PID: 10, PPID: 1, Name: "claude.exe", Cmd: "claude.exe daemon run"},
		11: {PID: 11, PPID: 10, Name: "claude.exe", Cmd: `claude.exe --bg-pty-host \\.\pipe\x 11 5`},
		12: {PID: 12, PPID: 11, Name: "claude.exe", Cmd: "claude.exe --session-id ba03e821-own"},
		99: {PID: 99, PPID: 12, Name: "agents-cli.exe", Cmd: "agents-cli.exe ls"},
	}
}

func TestLaunchTreeIntactKeepsLaunchedChild(t *testing.T) {
	m := tech211Procs()
	li := launchInfo{TaskSlug: "tech-211-x"}
	if !launchTreeIntact(m, 2, li) {
		t.Fatal("launched child still in the tree: launch-info name must be kept")
	}
}

func TestLaunchTreeIntactDropsAttachTakeover(t *testing.T) {
	m := tech211Procs()
	m[4].Cmd = "claude.exe attach ba03e821-own" // window re-attached to another session
	li := launchInfo{TaskSlug: "tech-211-x", SessionId: "7d567b5b-old"}
	if launchTreeIntact(m, 2, li) {
		t.Fatal("tree now hosts an attach of another session: stale launch-info name must be dropped")
	}
}

func TestLaunchTreeIntactStartupRace(t *testing.T) {
	m := procMap{
		1: {PID: 1, PPID: 0, Name: "explorer.exe"},
		2: {PID: 2, PPID: 1, Name: "cmd.exe", Cmd: "cmd /k launch-tech-211-x.bat"},
	}
	if !launchTreeIntact(m, 2, launchInfo{TaskSlug: "tech-211-x"}) {
		t.Fatal("no agent child yet (startup race): record must be kept")
	}
}

func TestLaunchTreeIntactUnreadableCmdline(t *testing.T) {
	m := tech211Procs()
	m[4].Cmd = "" // protected/unreadable: cannot prove the tree changed
	if !launchTreeIntact(m, 2, launchInfo{TaskSlug: "tech-211-x"}) {
		t.Fatal("unreadable agent cmdline must not drop the launch-info name")
	}
}

func TestLaunchTreeIntactResumeMatch(t *testing.T) {
	m := tech211Procs()
	m[4].Cmd = `claude.exe --resume 7d567b5b-old` // relaunched with --resume
	if !launchTreeIntact(m, 2, launchInfo{TaskSlug: "tech-211-x", SessionId: "7d567b5b-old"}) {
		t.Fatal("--resume child carrying the launch-info session id must count as intact")
	}
}

func TestTreeAttachesSession(t *testing.T) {
	m := tech211Procs()
	m[4].Cmd = "claude.exe attach ba03e821-own"
	if !m.treeAttachesSession(2, "ba03e821-own") {
		t.Fatal("attach of the caller's session inside window 2 must be detected")
	}
	if m.treeAttachesSession(2, "someone-else") {
		t.Fatal("attach of a different session must not match")
	}
	if m.treeAttachesSession(2, "") {
		t.Fatal("empty session id must never match")
	}
	if tech211Procs().treeAttachesSession(2, "ba03e821-own") {
		t.Fatal("a plain launched session is not an attach")
	}
}

func TestCallerClaudeAncestorSkipsDaemon(t *testing.T) {
	m := tech211Procs()
	if pid := m.callerClaudeAncestor(99); pid != 12 {
		t.Fatalf("caller claude ancestor = %d, want 12 (not daemon 10 / pty-host 11)", pid)
	}
	if pid := m.callerClaudeAncestor(4); pid != 4 {
		t.Fatalf("window claude ancestor = %d, want 4", pid)
	}
}

func TestMarkSelfViaAttach(t *testing.T) {
	m := tech211Procs()
	m[4].Cmd = "claude.exe attach ba03e821-own"
	s := &Session{PID: 2, Tool: "claude"}
	markSelf(s, m, 12, "ba03e821-own")
	if !s.Self {
		t.Fatal("window attached to the caller's own session must be marked Self")
	}
	s2 := &Session{PID: 2, Tool: "claude"}
	markSelf(s2, m, 12, "other-sid")
	if s2.Self {
		t.Fatal("window not attached to the caller's session must not be Self")
	}
	s3 := &Session{PID: 12, Tool: "claude"}
	markSelf(s3, m, 12, "")
	if !s3.Self {
		t.Fatal("the caller's own root must stay Self via selfRoot")
	}
}

func TestSelfSlashRefusal(t *testing.T) {
	self := &Session{PID: 2, Tool: "claude", Self: true}
	if err := selfSlashRefusal(self, "inject", "/rename TECH-210"); err == nil {
		t.Fatal("slash command into own session must be refused")
	}
	if err := selfSlashRefusal(self, "inject", "  /color red"); err == nil {
		t.Fatal("leading-whitespace slash command must be refused")
	}
	if err := selfSlashRefusal(self, "inject", "hello there"); err != nil {
		t.Fatalf("plain text is not a slash command: %v", err)
	}
	if err := selfSlashRefusal(self, "native", "/rename X"); err != nil {
		t.Fatalf("native queue is not injection: %v", err)
	}
	other := &Session{PID: 3, Tool: "claude"}
	if err := selfSlashRefusal(other, "inject", "/rename X"); err != nil {
		t.Fatalf("other sessions may still receive slash commands: %v", err)
	}
}
