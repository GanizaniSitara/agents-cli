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
		{"spinner above composer", "busy", frame("⣽  Running command...")},
		{"explicit patch question", "prompt", frame("Would you like me to apply this patch?")},
		{"approval overrides foreground footer", "prompt", []string{"⣯ Running command...", "Allow this command?", "> Yes, once", "↑/↓ Navigate · enter Confirm"}},
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
