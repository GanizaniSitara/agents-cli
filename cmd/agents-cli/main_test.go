package main

import (
	"strings"
	"testing"
)

func TestParseArgsMixedOrder(t *testing.T) {
	p, err := parseArgs([]string{"1234", "hello", "world", "--no-enter", "--via", "inject", "--idle-for=5s"}, "no-enter")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(p.pos, " ") != "1234 hello world" {
		t.Fatalf("pos = %v", p.pos)
	}
	if !p.boolean("no-enter") || p.str("via", "") != "inject" || p.str("idle-for", "") != "5s" {
		t.Fatalf("flags = %v", p.flags)
	}
}

func TestParseDurationDays(t *testing.T) {
	d, err := parseDuration("7d")
	if err != nil || d.Hours() != 168 {
		t.Fatalf("7d -> %v %v", d, err)
	}
}

func TestClassify(t *testing.T) {
	cases := []struct {
		p    proc
		want string
	}{
		{proc{Name: "claude.exe", Cmd: `c:\x\claude.exe --session-id abc`}, "claude"},
		{proc{Name: "claude.exe", Cmd: `c:\x\claude.exe daemon run --origin transient`}, ""},
		{proc{Name: "claude.exe", Cmd: `c:\x\claude.exe --bg-pty-host \\.\pipe\x 129 5`}, ""},
		{proc{Name: "node.exe", Cmd: `"node" "c:\nvm4w\nodejs\node_modules\@qwen-code\qwen-code\cli-entry.js"`}, "qwen"},
		{proc{Name: "node.exe", Cmd: `"node" "c:\x\node_modules\@github\copilot\npm-loader.js"`}, "copilot"},
		{proc{Name: "node.exe", Cmd: `"node" "c:\x\node_modules\opencode-ai\bin\opencode"`}, "opencode"},
		{proc{Name: "node.exe", Cmd: `"node" "c:\x\node_modules\@openai\codex\bin\codex.js"`}, "codex"},
		{proc{Name: "codex.exe", Cmd: ``}, "codex"},
		{proc{Name: "agy.exe"}, "agy"},
		{proc{Name: "node.exe", Cmd: `"node" "c:\x\context-mode\cli.bundle.mjs"`}, ""},
		{proc{Name: "bash.exe"}, ""},
	}
	for _, c := range cases {
		if got := classify(&c.p); got != c.want {
			t.Errorf("classify(%s %q) = %q, want %q", c.p.Name, c.p.Cmd, got, c.want)
		}
	}
}

func TestExePath(t *testing.T) {
	cases := map[string]string{
		`"C:\nvm4w\nodejs\\node.exe"   "C:\x\codex.js"`:   `C:\nvm4w\nodejs\\node.exe`,
		`C:\x\bin\codex.exe --no-daemon -c tui.theme='y'`: `C:\x\bin\codex.exe`,
		`agy.exe`: `agy.exe`,
	}
	for in, want := range cases {
		if got := exePath(in); got != want {
			t.Errorf("exePath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRootOfStopsAtPtyHost(t *testing.T) {
	m := procMap{
		1:  {PID: 1, PPID: 0, Name: "explorer.exe"},
		2:  {PID: 2, PPID: 1, Name: "cmd.exe"},
		3:  {PID: 3, PPID: 2, Name: "claude.exe", Cmd: "claude.exe"},
		4:  {PID: 4, PPID: 3, Name: "bash.exe"},
		5:  {PID: 5, PPID: 4, Name: "codex.exe"},
		10: {PID: 10, PPID: 1, Name: "claude.exe", Cmd: "claude.exe daemon run"},
		11: {PID: 11, PPID: 10, Name: "claude.exe", Cmd: "claude.exe --bg-pty-host x"},
		12: {PID: 12, PPID: 11, Name: "claude.exe", Cmd: "claude.exe --session-id y"},
	}
	if root, kind := m.rootOf(m[5]); root.PID != 2 || kind != "window" {
		t.Fatalf("codex under claude: root %d kind %s", root.PID, kind)
	}
	if root, kind := m.rootOf(m[12]); root.PID != 12 || kind != "bg" {
		t.Fatalf("bg session: root %d kind %s", root.PID, kind)
	}
	if m.depth(m[5], 2) <= m.depth(m[3], 2) {
		t.Fatal("nested codex should be deeper than the window's own claude")
	}
}

func TestGuessState(t *testing.T) {
	if s := guessState("claude", []string{"", "⠋ Thinking…", "esc to interrupt"}); s != "busy" {
		t.Fatalf("busy -> %s", s)
	}
	if s := guessState("codex", []string{"Allow command? (y/n)"}); s != "prompt" {
		t.Fatalf("prompt -> %s", s)
	}
	if s := guessState("qwen", []string{"some output", ">"}); s != "idle" {
		t.Fatalf("idle -> %s", s)
	}
}

func TestResolve(t *testing.T) {
	sessions := []Session{
		{PID: 10, AgentPID: 11, Name: "TECH-181-agents-cli", Title: "Claude TECH-181"},
		{PID: 20, AgentPID: 21, Name: "OP-001-mail", Title: "Codex OP-001"},
		{PID: 30, AgentPID: 31, Title: "Codex OP-002"},
	}
	if s, _ := resolve("11", sessions); s == nil || s.PID != 10 {
		t.Fatal("agent pid should resolve to its root")
	}
	if s, _ := resolve("op-001", sessions); s == nil || s.PID != 20 {
		t.Fatal("substring on name")
	}
	if _, err := resolve("codex", sessions); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("expected ambiguity error, got %v", err)
	}
}
