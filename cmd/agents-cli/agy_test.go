package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

var agyIntegration = flag.Bool("agy-integration", false, "test drafts in a new hidden agy console; never submits a model prompt")

func agyScreen(draft, footer string) []string {
	return []string{"old transcript: esc to cancel", "────────", ">" + draft, "────────", footer, "", ""}
}

func TestAGYSendWithFakeConsole(t *testing.T) {
	for _, outcome := range []string{"busy", "completed", "menu", "unsubmitted", "no-enter", "muted", "paste-error", "peek-error", "key-error", "unsettled", "already-busy", "already-menu", "existing-draft", "trust", "changed-state"} {
		t.Run(outcome, func(t *testing.T) {
			now := time.Unix(0, 0)
			start := now
			pasted, entered := false, false
			keys, reads := 0, 0
			text := strings.Repeat("£漢😀\r\n", 400)
			c := agyConsole{
				paste: func(pid uint32, got string) error {
					if pid != 42 || got != text {
						t.Fatal("wrong target/text")
					}
					pasted = true
					if outcome == "paste-error" {
						return errors.New("paste failed")
					}
					return nil
				},
				peek: func(pid uint32, above int) (*peekResult, error) {
					if pid != 42 || above != 0 || outcome == "muted" {
						t.Fatal("wrong or muted peek")
					}
					if pasted && outcome == "peek-error" {
						return nil, errors.New("read failed")
					}
					draft, footer := "", "? for shortcuts   Gemini 4 Argon · high"
					if !pasted {
						switch outcome {
						case "already-busy":
							footer = "esc to cancel   Gemini 4 Argon · high"
						case "already-menu":
							footer = "enter Confirm · esc Cancel"
						case "existing-draft":
							draft = " user draft"
						case "trust":
							return &peekResult{Lines: []string{"Do you trust the contents of this project?", "> Yes, I trust this folder", "enter Confirm"}}, nil
						}
					} else if !entered || outcome == "unsubmitted" {
						reads++
						draft = " [Pasted text #1 2000 chars]"
						if reads == 1 || outcome == "unsettled" {
							draft = fmt.Sprintf(" growing %d", reads)
						}
						if outcome == "changed-state" {
							footer = "esc to cancel"
						}
					} else if outcome == "busy" {
						footer = "esc to cancel   Gemini 4 Argon · high"
					} else if outcome == "menu" {
						draft, footer = " Yes", "enter Confirm · esc Cancel"
					}
					return &peekResult{Lines: agyScreen(draft, footer)}, nil
				},
				key: func(pid uint32, tool, name string) error {
					if pid != 42 || tool != "agy" || name != "enter" || entered {
						t.Fatal("wrong or repeated key")
					}
					if outcome != "muted" && now.Sub(start) < 600*time.Millisecond {
						t.Fatal("Enter preceded stable draft")
					}
					entered = true
					keys++
					if outcome == "key-error" {
						return errors.New("key failed")
					}
					return nil
				},
				sleep: func(d time.Duration) { now = now.Add(d) }, now: func() time.Time { return now },
			}
			s := &Session{PID: 42, Tool: "agy", PollingMuted: outcome == "muted"}
			submitted, err := c.send(s, text, outcome != "no-enter", false)
			preflight := strings.HasPrefix(outcome, "already-") || outcome == "existing-draft" || outcome == "trust"
			wantErr := preflight || strings.HasSuffix(outcome, "-error") || outcome == "unsettled" || outcome == "changed-state"
			wantSubmitted := outcome == "busy" || outcome == "completed" || outcome == "menu"
			wantKey := !preflight && outcome != "paste-error" && outcome != "peek-error" && outcome != "unsettled" && outcome != "no-enter" && outcome != "changed-state"
			if submitted != wantSubmitted || (err != nil) != wantErr || pasted == preflight || (keys == 1) != wantKey {
				t.Fatalf("submitted=%v err=%v pasted=%v keys=%d", submitted, err, pasted, keys)
			}
		})
	}
}

// Opt-in only. Build .verification/agy-candidate.exe first. A test executable
// owns this disposable process; validate discovery before writing any input.
func TestAGYIsolatedDraft(t *testing.T) {
	if !*agyIntegration {
		t.Skip("requires agy and a locally built candidate; use -args -agy-integration")
	}
	agy, err := exec.LookPath("agy.exe")
	if err != nil {
		t.Fatal(err)
	}
	binary, _ := filepath.Abs(filepath.Join("..", "..", ".verification", "agy-candidate.exe"))
	child := exec.Command(agy)
	child.Dir = t.TempDir()
	child.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_CONSOLE, HideWindow: true}
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = child.Process.Kill(); _ = child.Wait() }()
	pid := strconv.Itoa(child.Process.Pid)
	read := func() []string {
		t.Helper()
		raw, err := exec.Command(binary, "_attach", "peek", pid, "0").Output()
		var screen peekResult
		if err != nil || json.Unmarshal(raw, &screen) != nil || screen.Error != "" {
			t.Fatalf("peek failed: %v %s", err, raw)
		}
		return screen.Lines
	}
	time.Sleep(2 * time.Second)
	lines := read()
	if strings.Contains(strings.Join(lines, "\n"), "Do you trust the contents of this project?") {
		if _, err := exec.Command(binary, "_attach", "key", pid, "enter").Output(); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Second)
	}
	raw, err := exec.Command(binary, "ls", "--fast").Output()
	var listing struct{ Sessions []Session }
	if err != nil || json.Unmarshal(raw, &listing) != nil {
		t.Fatalf("discovery failed: %v", err)
	}
	matched := false
	for _, s := range listing.Sessions {
		if s.PID == uint32(child.Process.Pid) && s.AgentPID == s.PID && s.Tool == "agy" {
			matched = true
		}
	}
	if !matched {
		t.Fatal("disposable PID did not resolve to its own agy console; refusing to inject")
	}
	for _, length := range []int{2000, 10000} {
		if draft, ok := copilotComposer(read()); !ok || draft != "" {
			t.Fatal("isolated composer is not empty")
		}
		start := time.Now()
		cmd := exec.Command(binary, "send", pid, "--stdin", "--no-enter")
		cmd.Stdin = strings.NewReader(strings.Repeat("x", length))
		raw, err = cmd.Output()
		var receipt struct {
			PID              uint32
			Tool             string
			Enter, Submitted bool
		}
		if err != nil || json.Unmarshal(raw, &receipt) != nil || receipt.PID != uint32(child.Process.Pid) || receipt.Tool != "agy" || receipt.Enter || receipt.Submitted {
			t.Fatalf("send failed or wrong receipt: %v %s", err, raw)
		}
		for {
			draft, _ := copilotComposer(read())
			if strings.Contains(draft, fmt.Sprintf("%d chars]", length)) {
				t.Logf("%d characters visible in %s", length, time.Since(start))
				break
			}
			if time.Since(start) > 3*time.Second {
				t.Fatalf("paste did not display promptly: %q", draft)
			}
			time.Sleep(50 * time.Millisecond)
		}
		if _, err := exec.Command(binary, "_attach", "key", pid, "ctrl-u").Output(); err != nil {
			t.Fatal(err)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// TECH-229: --force skips only the empty-composer precheck; busy and menu
// screens are still refused, and a task line no longer hides an idle composer.
func TestAGYSendForceAndTaskLine(t *testing.T) {
	rule := "───────────────────────────────────────────────"
	task := "  ● [16:05:14] Schedule 120s check on NGH-050 and NGH-052 running"
	footer := "? for shortcuts                       Gemini 4 Argon · high · 1 task(s) · /tasks"
	taskScreen := func(draft, footer string) []string { return []string{rule, ">" + draft, rule, task, rule, footer} }
	for _, tc := range []struct {
		name         string
		before       []string
		force, enter bool
		wantPaste    bool
		wantSubmit   bool
		wantErr      bool
	}{
		{"idle with task line", taskScreen("", footer), false, false, true, false, false},
		{"idle with task line submits", taskScreen("", footer), false, true, true, true, false},
		{"existing draft refused", taskScreen(" user draft", footer), false, false, false, false, true},
		{"existing draft forced", taskScreen(" user draft", footer), true, false, true, false, false},
		{"existing draft forced submits", agyScreen(" user draft", "? for shortcuts"), true, true, true, true, false},
		{"no composer forced", []string{rule, task, rule, footer}, true, false, true, false, false},
		{"busy forced", taskScreen("", "esc to cancel   Gemini 4 Argon · high"), true, false, false, false, true},
		{"menu forced", taskScreen("", "enter Confirm · esc Cancel"), true, false, false, false, true},
		{"trust forced", []string{"Do you trust the contents of this project?", "> Yes, I trust this folder", "enter Confirm"}, true, false, false, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Unix(0, 0)
			pasted, entered := false, false
			c := agyConsole{
				paste: func(pid uint32, got string) error {
					if pid != 42 || got != "ping" {
						t.Fatal("wrong target/text")
					}
					pasted = true
					return nil
				},
				peek: func(pid uint32, above int) (*peekResult, error) {
					switch {
					case !pasted:
						return &peekResult{Lines: tc.before}, nil
					case !entered:
						return &peekResult{Lines: taskScreen(" user draft ping", footer)}, nil
					default:
						return &peekResult{Lines: taskScreen("", footer)}, nil
					}
				},
				key: func(pid uint32, tool, name string) error {
					if pid != 42 || tool != "agy" || name != "enter" || entered {
						t.Fatal("wrong or repeated key")
					}
					entered = true
					return nil
				},
				sleep: func(d time.Duration) { now = now.Add(d) }, now: func() time.Time { return now },
			}
			submitted, err := c.send(&Session{PID: 42, Tool: "agy"}, "ping", tc.enter, tc.force)
			if pasted != tc.wantPaste || submitted != tc.wantSubmit || (err != nil) != tc.wantErr || entered != (tc.enter && tc.wantPaste) {
				t.Fatalf("pasted=%v submitted=%v entered=%v err=%v", pasted, submitted, entered, err)
			}
		})
	}
}
