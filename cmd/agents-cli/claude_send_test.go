package main

import (
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf16"
)

func TestClaudeUnsubmittedInput(t *testing.T) {
	for _, draft := range []string{
		"hello Claude",
		"[Pasted text #1 +8 lines]",
		"Thinking about Working (and Allow changes)",
	} {
		if isSubmitted("claude", draft, []string{"Working (old transcript)", "❯ " + draft}) {
			t.Errorf("draft must not count as submission: %s", draft)
		}
	}
	// Current Ink renders a non-breaking space after the empty prompt glyph.
	if !isSubmitted("claude", "hello Claude", []string{"❯\u00a0"}) || guessState("claude", []string{"❯\u00a0"}) != "idle" {
		t.Fatal("Unicode whitespace in an empty composer must count as idle")
	}
}

func TestInjectInputPasteThenSeparateEnter(t *testing.T) {
	for _, text := range []string{"hello", strings.Repeat("long text 😃\n", 1000)} {
		for _, enter := range []bool{false, true} {
			writes, slept := 0, false
			err := injectInput(func(records []inputRecord) error {
				writes++
				if writes == 1 {
					var units []uint16
					for i, record := range records {
						if record.VK != 0xE7 || record.KeyDown != int32(1-i%2) {
							t.Fatal("invalid Unicode key event pair")
						}
						if record.KeyDown == 1 {
							units = append(units, record.Char)
						}
					}
					if got := string(utf16.Decode(units)); got != text {
						t.Fatal("paste text changed")
					}
				} else if !slept || len(records) != 2 || records[0].VK != 0x0D || records[0].Char != '\r' {
					t.Fatal("Enter must be a separate write after settling")
				}
				return nil
			}, func(d time.Duration) {
				if writes != 1 || d < 500*time.Millisecond || d > 4*time.Second {
					t.Fatalf("invalid settling delay %v after %d writes", d, writes)
				}
				slept = true
			}, text, enter)
			want := 1
			if enter {
				want++
			}
			if err != nil || writes != want || slept != enter {
				t.Fatalf("err=%v writes=%d slept=%v", err, writes, slept)
			}
		}
	}
}

func TestInjectInputFailureDoesNotSubmit(t *testing.T) {
	want := errors.New("console write failed")
	err := injectInput(func([]inputRecord) error { return want }, func(time.Duration) {
		t.Fatal("must not sleep or submit after failed paste")
	}, "hello Claude", true)
	if !errors.Is(err, want) {
		t.Fatal(err)
	}
}

func TestClaudeMutedSubmissionVerification(t *testing.T) {
	for _, muted := range []bool{false, true} {
		t.Run(map[bool]string{false: "normal", true: "muted"}[muted], func(t *testing.T) {
			now := time.Unix(0, 0)
			keys, reads := 0, 0
			s := &Session{PID: 42, Tool: "claude", PollingMuted: muted}
			v := submissionVerifier{
				peek: func(uint32, int) (*peekResult, error) {
					reads++
					if keys == 0 {
						return &peekResult{Lines: []string{"❯ [Pasted text #1 +8 lines]"}}, nil
					}
					return &peekResult{Lines: []string{"✢ Misting… (1s · ↓ 5 tokens)", "❯"}}, nil
				},
				key: func(pid uint32, tool, key string) error {
					if pid != 42 || tool != "claude" || key != "enter" {
						t.Fatal("incorrect retry target or key")
					}
					keys++
					return nil
				},
				alive: func(uint32) bool { return true },
				sleep: func(d time.Duration) { now = now.Add(d) },
				now:   func() time.Time { return now },
			}
			if !v.verify(s, "hello Claude") || keys != 1 || reads == 0 {
				t.Fatalf("send not verified: keys=%d reads=%d", keys, reads)
			}
			if s.PollingMuted != muted {
				t.Fatal("explicit send changed polling mute")
			}
		})
	}
}

func TestSubmissionRetryRequiresVisibleDraft(t *testing.T) {
	for _, screen := range [][]string{
		{"unrecognized screen"},
		{"Do you want to allow this tool?", "❯ 1. Yes"},
		nil, // failed screen read
	} {
		now := time.Unix(0, 0)
		v := submissionVerifier{
			peek: func(uint32, int) (*peekResult, error) {
				if screen == nil {
					return nil, errors.New("read failed")
				}
				return &peekResult{Lines: screen}, nil
			},
			key:   func(uint32, string, string) error { t.Fatal("blind Enter retry"); return nil },
			alive: func(uint32) bool { return true },
			sleep: func(d time.Duration) { now = now.Add(d) },
			now:   func() time.Time { return now },
		}
		got := v.verify(&Session{PID: 42, Tool: "claude"}, "hello Claude")
		if screen == nil && got {
			t.Fatal("read failure reported submitted")
		}
	}
}
