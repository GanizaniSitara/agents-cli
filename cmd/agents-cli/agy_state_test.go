package main

import (
	"strings"
	"testing"
)

func TestAGYLiveStateRegions(t *testing.T) {
	frame := func(output string, tasks ...string) []string {
		lines := append(strings.Split(output, "\n"), "", "────────────────────────", ">", "────────────────────────")
		lines = append(lines, tasks...)
		return append(lines, "? for shortcuts     Gemini 3.8 Flash · medium · 1 task(s) · /tasks")
	}
	cases := []struct {
		name, want string
		lines      []string
	}{
		{"background command with idle composer", "busy", frame("The command is running in the background.", "  ● [21:05:35] python.exe example_job.py --input example running")},
		{"foreground footer", "busy", append(frame("Do you want the old option?"), "esc to cancel    Gemini 3.8 Flash · medium")},
		{"foreground spinner without footer", "busy", []string{"", "⣯  Running command..", "", ""}},
		{"file spinner without footer", "busy", []string{"", "⣟  Reading file...", "", ""}},
		{"spinner above composer", "busy", frame("⣽  Running command...")},
		{"explicit patch question", "prompt", frame("Would you like me to apply this patch?")},
		{"approval overrides foreground footer", "prompt", []string{"⣯ Running command...", "Allow this command?", "> Yes, once", "↑/↓ Navigate · enter Confirm"}},
		{"command permission with cancel footer", "prompt", agyCommandPermissionScreen("> 1. Yes, run command")},
		{"command permission other selection", "prompt", agyCommandPermissionScreen("> 4. No, cancel")},
		{"historical command permission", "idle", frame(strings.Join(agyCommandPermissionScreen("> 1. Yes, run command"), "\n") + "\n\nCommand completed.")},
		{"idle", "idle", frame("The requested change is ready.")},
		{"task count alone is not liveness", "idle", frame("Finished the background command.")},
		{"completed task strip", "idle", frame("Done.", "  ● [21:05:35] python.exe example_job.py completed")},
		{"historical running row", "idle", frame("  ● [21:05:35] python.exe example_job.py running\n\nThe job finished.")},
		{"old approval text", "idle", frame("Do you want to Allow this?\n\nApplied successfully.")},
		{"thinking in prose", "idle", frame("Thinking about the next steps is optional.")},
		{"unrecognised screen", "unknown", []string{"Unexpected application output"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			state := guessState("agy", tc.lines)
			if state != tc.want {
				t.Fatalf("state=%s, want %s", state, tc.want)
			}
			phase, _ := watchPhase("agy", tc.lines, state)
			if tc.want == "busy" && phase != "working" || tc.want == "prompt" && phase != "waiting" {
				t.Fatalf("panel phase=%s for state=%s", phase, state)
			}
		})
	}
}

func agyCommandPermissionScreen(selected string) []string {
	return []string{
		"Command", "────────────────────────", "Requesting permission for:", "   git status",
		"", "Run this command?", selected,
		"  2. Yes, and always allow in this conversation", "  3. Yes, and always allow (Persist to settings.json)",
		"  4. No, cancel", "", "  ↑/↓ Navigate · tab Amend · ctrl+g edit/expand command",
		"esc to cancel                 Gemini 3.8 Flash · medium",
	}
}

func TestAGYScheduledTimer(t *testing.T) {
	// Timer strip and footer captured from an idle AGY console (PID 18696).
	border := strings.Repeat("─", 80)
	timer := "  ● [10:21:21] 4h 9m timer running"
	shell := "  ● [10:22:03] python.exe check.py 4h 9m timer running"
	cases := []struct {
		name, output, footer string
		rows                 []string
		work                 int
		want                 string
	}{
		{"timer at empty prompt", "", "? for shortcuts", []string{timer}, 0, "idle"},
		{"foreground tool", "⣯  Running command...", "esc to cancel", nil, 0, "busy"},
		{"timer and spinner", "⣟  Reading file...", "? for shortcuts", []string{timer}, 0, "busy"},
		{"timer and foreground turn", "", "esc to cancel", []string{timer}, 0, "busy"},
		{"timer then shell task", "", "? for shortcuts", []string{timer, shell}, 0, "busy"},
		{"shell task then timer", "", "? for shortcuts", []string{shell, timer}, 0, "busy"},
		{"timer and working child process", "", "? for shortcuts", []string{timer}, 1, "busy"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lines := []string{tc.output, "", border, ">", border}
			lines = append(lines, tc.rows...)
			lines = append(lines, border, tc.footer+"     Gemini 4 Neon · medium · 1 task(s) · /tasks")
			state := agyRuntimeState(guessState("agy", lines), tc.work)
			if state != tc.want {
				t.Fatalf("state = %s, want %s", state, tc.want)
			}
			phase, _ := watchPhase("agy", lines, state)
			if (state == "idle" && phase != "idle") || (state == "busy" && phase != "working") {
				t.Fatalf("unexpected watcher phase %s for state %s", phase, state)
			}
		})
	}
}
