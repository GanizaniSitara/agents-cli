package main

import (
	"errors"
	"testing"
)

func TestWatchHarnessRenameConfirmation(t *testing.T) {
	for _, tool := range []string{"agy", "claude", "copilot"} {
		for _, tc := range []struct {
			lines []string
			want  bool
		}{
			{[]string{"> /rename", "  ⎿  Conversation renamed to: Music sheets", ">"}, true},
			{[]string{"❯ /rename Music sheets", "Session renamed to Music sheets", "❯"}, true},
			{[]string{"> /rename Music sheets", "Session renamed to Other name", ">"}, false},
			{[]string{"> /rename Music sheets", ">"}, false},
			{[]string{"> /rename", "Conversation renamed to: Music sheets", "> inspect another topic", "Other reply", ">"}, false},
			{[]string{"Some prose says session renamed to Music sheets", ">"}, false},
		} {
			if got := watchHarnessRenameConfirmed(tool, "Music sheets", tc.lines); got != tc.want {
				t.Fatalf("%s %q => %v", tool, tc.lines, got)
			}
		}
	}
	// Copilot's empty composer shows a placeholder behind the same ❯ marker.
	copilotScreen := []string{"❯ /rename Music sheets", "● Session renamed to: Music sheets", "",
		"────────────", "❯ Type @ to mention files, # for issues/PRs, / for commands, or ? for shortcuts", "────────────", " shift+tab switch mode"}
	if !watchHarnessRenameConfirmed("copilot", "Music sheets", copilotScreen) {
		t.Fatal("Copilot placeholder hid the rename confirmation")
	}
	if watchHarnessRenameConfirmed("copilot", "Music sheets", copilotScreen[2:]) {
		t.Fatal("composer placeholder alone confirmed a rename")
	}
	for _, tc := range []struct {
		line string
		want bool
	}{
		{"GPT-6-Astra · Context 77% left · Music sheets          ⚠ 1 warning", true},
		{"GPT-6-Astra · Context 77% left · Music sheets", true},
		{"GPT-6-Astra · Context 77% left · Music sheets extra", false},
		{"Session renamed to Music sheets", false},
	} {
		if got := watchHarnessRenameConfirmed("codex", "Music sheets", []string{tc.line}); got != tc.want {
			t.Fatalf("%q => %v", tc.line, got)
		}
	}
}

func TestWatchHarnessRenameSingleSendAndGuards(t *testing.T) {
	idle := []string{"────────", ">", "────────", "? for shortcuts"}
	for _, mode := range []string{"success", "uncertain", "timeout", "replaced", "busy", "draft", "independent-title"} {
		t.Run(mode, func(t *testing.T) {
			reads, sends, validations := 0, 0, 0
			validate := func() error {
				validations++
				if mode == "replaced" && validations > 2 {
					return errors.New("replaced")
				}
				return nil
			}
			read := func() (*peekResult, error) {
				reads++
				if reads == 1 {
					lines := idle
					if mode == "busy" {
						lines = []string{"esc to cancel"}
					}
					if mode == "draft" {
						lines = []string{"────────", "> user draft", "────────", "? for shortcuts"}
					}
					return &peekResult{Title: "Old title", Lines: lines}, nil
				}
				title := "Music sheets"
				if mode == "independent-title" {
					title = "Other owner's title"
				}
				return &peekResult{Title: title, Lines: []string{"> /rename", "  ⎿  Conversation renamed to: Music sheets", ">"}}, nil
			}
			send := func(text string) (bool, error) {
				sends++
				if text != "/rename Music sheets" {
					t.Fatalf("sent %q", text)
				}
				return mode != "uncertain", nil
			}
			pause := func() error {
				if mode == "timeout" {
					return errors.New("deadline")
				}
				return nil
			}
			title, err := watchRenameHarness("agy", "Music sheets", validate, read, send, pause)
			if mode == "success" {
				if err != nil || title != "Music sheets" {
					t.Fatalf("%q %v", title, err)
				}
			} else if err == nil || title != "" {
				t.Fatalf("unsafe confirmation: %q %v", title, err)
			}
			wantSends := 1
			if mode == "busy" || mode == "draft" {
				wantSends = 0
				want := map[string]string{"busy": watchRenameBusy, "draft": watchRenameDraft}[mode]
				if err.Error() != want {
					t.Fatalf("%s should queue, got %v", mode, err)
				}
			}
			if sends != wantSends {
				t.Fatalf("%d sends want %d", sends, wantSends)
			}
		})
	}
}

func TestWatchHarnessRenameTitleConfirmsOnTimeout(t *testing.T) {
	idle := []string{"────────", ">", "────────", "? for shortcuts"}
	for _, after := range []string{"Music sheets", "Something else"} {
		reads, pauses := 0, 0
		read := func() (*peekResult, error) {
			reads++
			if reads == 1 {
				return &peekResult{Title: "Old title", Lines: idle}, nil
			}
			return &peekResult{Title: after, Lines: []string{"> /rename Music sheets", ">"}}, nil
		}
		pause := func() error {
			pauses++
			if pauses > 3 {
				return errors.New("deadline")
			}
			return nil
		}
		title, err := watchRenameHarness("copilot", "Music sheets", func() error { return nil }, read,
			func(string) (bool, error) { return true, nil }, pause)
		if after == "Music sheets" && (err != nil || title != "Music sheets") {
			t.Fatalf("retitled window not accepted: %q %v", title, err)
		}
		if after != "Music sheets" && err == nil {
			t.Fatalf("unrelated title accepted: %q", title)
		}
	}
}

func TestWatchHarnessRenameCapability(t *testing.T) {
	for _, tool := range []string{"agy", "claude", "copilot", "codex"} {
		if !watchHarnessRenameSupported(tool) {
			t.Fatal(tool)
		}
	}
	for _, tool := range []string{"vibe", "qwen", "other", ""} {
		if watchHarnessRenameSupported(tool) {
			t.Fatal("unverified harness", tool)
		}
	}
	if !watchRenameComposerEmpty("codex", []string{"› Ask Codex to do anything", "GPT-6 · Context 50% left"}) {
		t.Fatal("Codex placeholder not idle")
	}
	if watchRenameComposerEmpty("codex", []string{"› my unfinished draft", "GPT-6 · Context 50% left"}) {
		t.Fatal("Codex draft overwritten")
	}
}
