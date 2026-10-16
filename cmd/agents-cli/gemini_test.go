package main

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestGeminiExecutableClassification(t *testing.T) {
	for _, tc := range []struct {
		name, cmd string
		want      bool
	}{
		{"gemini.exe", "", true},
		{"node.exe", `node.exe C:\npm\node_modules\@google\gemini-cli\bundle\gemini.js`, true},
		{"node.exe", `"C:\Program Files\node.exe" "C:\npm dir\@google\gemini-cli\dist\index.js"`, true},
		{"node.exe", `node.exe C:/npm/@google/gemini-cli/bundle/gemini.js`, true},
		{"node.exe", `node.exe C:/npm/@google/gemini-cli-other/index.js`, false},
		{"node.exe", `node.exe app.js --prompt C:/npm/@google/gemini-cli/bundle/gemini.js`, false},
		{"cmd.exe", `cmd.exe echo C:/npm/@google/gemini-cli/index.js`, false},
		{"node.exe", `"unclosed`, false},
	} {
		if got := classify(&proc{Name: tc.name, Cmd: strings.ToLower(tc.cmd)}) == "gemini"; got != tc.want {
			t.Errorf("%s %q: Gemini=%v", tc.name, tc.cmd, got)
		}
	}
}

func TestGeminiPasteTransport(t *testing.T) {
	for _, outcome := range []string{"enter", "no-enter", "paste-error", "key-error", "long"} {
		t.Run(outcome, func(t *testing.T) {
			text := "first\r\n£漢😀"
			if outcome == "long" {
				text = strings.Repeat(text, 10000)
			}
			pastes, keys := 0, 0
			var pause time.Duration
			c := geminiConsole{
				paste: func(pid uint32, got string) error {
					if pid != 42 || got != text {
						t.Fatal("wrong paste target/text")
					}
					pastes++
					if outcome == "paste-error" {
						return errors.New("paste failed")
					}
					return nil
				},
				key: func(pid uint32, tool, key string) error {
					if pid != 42 || tool != "gemini" || key != "enter" || pause < 800*time.Millisecond || pause > 4*time.Second {
						t.Fatal("wrong key or unsettled paste")
					}
					keys++
					if outcome == "key-error" {
						return errors.New("key failed")
					}
					return nil
				},
				sleep: func(d time.Duration) { pause += d },
			}
			err := c.inject(42, text, outcome != "no-enter")
			wantErr := strings.HasSuffix(outcome, "-error")
			wantKey := outcome != "no-enter" && outcome != "paste-error"
			if (err != nil) != wantErr || pastes != 1 || (keys == 1) != wantKey {
				t.Fatalf("err=%v pastes=%d keys=%d", err, pastes, keys)
			}
			if !wantKey && pause != 0 {
				t.Fatal("no-enter or failure slept")
			}
		})
	}
}
