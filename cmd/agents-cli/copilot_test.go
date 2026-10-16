package main

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf16"
	"unsafe"
)

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

func TestCopilotPasteAndKeyRecords(t *testing.T) {
	text := "first\r\nsecond\r£漢😀\n" + strings.Repeat("long text ", 180)
	records := copilotPasteRecords(text)
	var units []uint16
	for i := 0; i < len(records); i += 2 {
		down, up := records[i], records[i+1]
		if down.EventType != keyEvent || down.KeyDown != 1 || down.RepeatCount != 1 || down.VK != 0 || down.Ctrl != 0 {
			t.Fatalf("bad paste record: %+v", down)
		}
		down.KeyDown = 0
		if down != up {
			t.Fatal("unpaired key event")
		}
		units = append(units, down.Char)
	}
	want := "\x1b[200~first\nsecond\n£漢😀\n" + strings.Repeat("long text ", 180) + "\x1b[201~"
	if got := string(utf16.Decode(units)); got != want {
		t.Fatalf("paste changed: %q", got)
	}
	if unsafe.Sizeof(inputRecord{}) != 20 || unsafe.Offsetof(inputRecord{}.Char) != 14 {
		t.Fatal("Windows INPUT_RECORD layout changed")
	}
	for name, want := range map[string]struct{ vk, ch uint16 }{
		"enter": {0x0d, '\r'}, "escape": {0x1b, 0x1b}, "up": {0x26, 0}, "down": {0x28, 0},
	} {
		k := namedKeys[name]
		r := keyPair(k.vk, k.ch, k.ctrl)
		if r[0].VK != want.vk || r[0].Char != want.ch || r[0].Ctrl != 0 || r[0].KeyDown != 1 || r[1].KeyDown != 0 {
			t.Fatalf("%s: %+v", name, r)
		}
	}
}

func TestCopilotSendWithFakeConsole(t *testing.T) {
	for _, outcome := range []string{"busy", "idle", "menu", "unsubmitted", "no-enter", "muted", "paste-error", "peek-error", "key-error", "unsettled", "already-busy", "already-menu"} {
		t.Run(outcome, func(t *testing.T) {
			text := "long\n" + strings.Repeat("£漢😀 ", 400)
			now := time.Unix(0, 0)
			start := now
			var operations []string
			pasted, entered := false, false
			reads := 0
			c := copilotConsole{
				paste: func(pid uint32, got string) error {
					if pid != 42 || got != text {
						t.Fatal("wrong paste target/text")
					}
					operations = append(operations, "paste")
					pasted = true
					if outcome == "paste-error" {
						return errors.New("paste failed")
					}
					return nil
				},
				peek: func(pid uint32, above int) (*peekResult, error) {
					if outcome == "muted" {
						t.Fatal("muted console polled")
					}
					if pid != 42 || above != 0 {
						t.Fatal("wrong peek target")
					}
					if outcome == "peek-error" && pasted {
						return nil, errors.New("read failed")
					}
					draft, footer := "", "/ commands · ? help"
					if !pasted {
						if outcome == "already-busy" {
							footer = "◎ Working esc interrupt"
						}
						if outcome == "already-menu" {
							footer = "enter to select · esc to cancel"
						}
					} else if !entered || outcome == "unsubmitted" {
						reads++
						draft = " [Pasted text]"
						if reads == 1 || outcome == "unsettled" {
							draft += strings.Repeat("x", reads)
						}
					} else if outcome == "busy" {
						footer = "◎ Working esc interrupt"
					} else if outcome == "menu" {
						draft, footer = " Search models…", "↑/↓ to navigate · enter to select · esc to cancel"
					}
					return &peekResult{Lines: copilotScreen(draft, footer)}, nil
				},
				key: func(pid uint32, tool, name string) error {
					if pid != 42 || tool != "copilot" || name != "enter" {
						t.Fatal("wrong key")
					}
					if now.Sub(start) < 1600*time.Millisecond {
						t.Fatal("long paste submitted too early")
					}
					if entered {
						t.Fatal("blind Enter retry")
					}
					entered = true
					operations = append(operations, "enter")
					if outcome == "key-error" {
						return errors.New("key failed")
					}
					return nil
				},
				sleep: func(d time.Duration) { now = now.Add(d) }, now: func() time.Time { return now },
			}
			s := &Session{PID: 42, Tool: "copilot", PollingMuted: outcome == "muted"}
			submitted, err := c.send(s, text, outcome != "no-enter")
			wantSubmitted := outcome == "busy" || outcome == "idle" || outcome == "menu"
			wantErr := strings.HasSuffix(outcome, "-error") || outcome == "unsettled" || strings.HasPrefix(outcome, "already-")
			if submitted != wantSubmitted || (err != nil) != wantErr {
				t.Fatalf("submitted=%v err=%v", submitted, err)
			}
			wantOps := []string{"paste", "enter"}
			if strings.HasPrefix(outcome, "already-") {
				wantOps = nil
			} else if outcome == "no-enter" || outcome == "paste-error" || outcome == "peek-error" || outcome == "unsettled" {
				wantOps = []string{"paste"}
			}
			if !reflect.DeepEqual(operations, wantOps) {
				t.Fatalf("operations = %v", operations)
			}
		})
	}
}
