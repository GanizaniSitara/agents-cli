package main

import "testing"

func copilotScreen(draft, footer string) []string {
	return []string{"● Previous answer: Thinking. Allow access?", "────────────", "❯" + draft, "────────────", footer, "", ""}
}

func TestCopilotDiscovery(t *testing.T) {
	for _, p := range []proc{
		{Name: "copilot.exe"},
		{Name: "node.exe", Cmd: `node "c:\npm\@github\copilot\npm-loader.js"`},
		{Name: "node.exe", Cmd: `node "c:/npm/@github/copilot/npm-loader.js"`},
	} {
		if classify(&p) != "copilot" {
			t.Fatalf("missed Copilot: %+v", p)
		}
	}
	for _, p := range []proc{
		{Name: "node.exe", Cmd: `node c:/npm/@github/copilot-other/index.js`},
		{Name: "cmd.exe", Cmd: `echo c:\npm\@github\copilot\npm-loader.js`},
	} {
		if classify(&p) == "copilot" {
			t.Fatalf("false Copilot: %+v", p)
		}
	}
	m := procMap{
		1: {PID: 1, Name: "explorer.exe"},
		2: {PID: 2, PPID: 1, Name: "cmd.exe"},
		3: {PID: 3, PPID: 2, Name: "node.exe"},
		4: {PID: 4, PPID: 3, Name: "copilot.exe"},
		5: {PID: 5, PPID: 4, Name: "cmd.exe"},
	}
	if root, kind := m.rootOf(m[5]); root.PID != 2 || kind != "window" {
		t.Fatalf("Copilot descendant root = %v, %s", root, kind)
	}
}

func TestCopilotScreens(t *testing.T) {
	for _, tc := range []struct {
		name, draft, footer, want string
	}{
		{"idle", "", "/ commands · ? help · tab next tab", "idle"},
		{"working", "", "◎ Working esc interrupt    Auto → GPT-5.4 mini", "busy"},
		{"queued draft", " pending text", "◉ Working esc interrupt · ctrl+enter enqueue", "busy"},
		{"draft", " Thinking: Allow access?", "@ files · # issues", "prompt"},
		{"picker", " Search models…", "↑/↓ to navigate · enter to select · esc to cancel", "prompt"},
		{"permission", " Yes", "↑/↓ navigate · Enter confirm · Esc cancel", "prompt"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lines := copilotScreen(tc.draft, tc.footer)
			s := &Session{PID: 42, Tool: "copilot", Alive: true}
			refreshScreen(s, true, func(pid uint32, above int) (*peekResult, error) {
				if pid != 42 || above != 0 {
					t.Fatal("wrong console")
				}
				return &peekResult{Title: "Windows PowerShell", Lines: lines}, nil
			})
			if s.State != tc.want || s.Title != "Windows PowerShell" {
				t.Fatalf("screen = %+v, want %s", s, tc.want)
			}
			if waitCanIdle(s.Tool, lines) != (tc.want == "idle") {
				t.Fatal("stable busy/menu/draft must not finish wait as idle")
			}
		})
	}
	if copilotState([]string{"❯ old transcript prompt"}) != "unknown" {
		t.Fatal("transcript mistaken for composer")
	}
}
