package main

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestWatchNameAgentReply(t *testing.T) {
	if limit, err := commandLimit([]string{"_attach", "name-suggest", "1"}); err != nil || limit != 75*time.Second {
		t.Fatalf("bounded naming deadline %v %v", limit, err)
	}
	for _, tc := range []struct{ line, want string }{
		{"  TOKEN: Piano arpeggio practice", "Piano arpeggio practice"},
		{"`TOKEN: Piano arpeggio practice`", "Piano arpeggio practice"},
		{"> TOKEN: Echoed user prompt", ""},
		{"OLD_TOKEN: Historical conversation title", ""},
		{"Reply starting with TOKEN followed by a colon", ""},
		{"TOKEN: <short title>", ""},
		{"TOKEN: " + strings.Repeat("long ", 30), ""},
	} {
		if got := watchAgentNameReply([]string{tc.line}, "TOKEN"); got != tc.want {
			t.Fatalf("%q => %q want %q", tc.line, got, tc.want)
		}
	}
}

func TestWatchRequestAgentNameSingleSend(t *testing.T) {
	idle := []string{"────────", ">", "────────", "? for shortcuts"}
	for _, tc := range []struct {
		name                string
		submitted, replaced bool
	}{
		{"reply", true, false}, {"uncertain", false, false}, {"replacement", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reads, sends, validates := 0, 0, 0
			validate := func() error {
				validates++
				if tc.replaced && validates >= 3 {
					return errors.New("agent changed")
				}
				return nil
			}
			read := func() ([]string, error) {
				reads++
				if reads == 1 {
					return idle, nil
				}
				return []string{"TOKEN: Piano arpeggio practice"}, nil
			}
			send := func(text string) (bool, error) {
				sends++
				if !strings.Contains(text, "Do not rename anything") || strings.Contains(text, "TOKEN:") {
					t.Fatal("request must prohibit renaming and avoid echoable reply prefix")
				}
				return tc.submitted, nil
			}
			name, err := watchRequestAgentName("agy", "TOKEN", validate, read, send, func() error { return nil })
			if sends != 1 {
				t.Fatalf("sent %d requests", sends)
			}
			if tc.submitted && !tc.replaced {
				if err != nil || name != "Piano arpeggio practice" {
					t.Fatalf("%q %v", name, err)
				}
			} else if err == nil || name != "" {
				t.Fatalf("unsafe result %q %v", name, err)
			}
		})
	}
}

func TestWatchRequestAgentNameRefusesBusyDraftAndTimeout(t *testing.T) {
	for _, lines := range [][]string{
		{"────────", "> existing user draft", "────────", "? for shortcuts"},
		{"────────", ">", "────────", "esc to cancel"},
		{},
	} {
		_, err := watchRequestAgentName("agy", "TOKEN", func() error { return nil }, func() ([]string, error) { return lines, nil }, func(string) (bool, error) { t.Fatal("unsafe request sent"); return false, nil }, func() error { return nil })
		if err == nil {
			t.Fatal("unsafe composer accepted")
		}
	}
	sends := 0
	_, err := watchRequestAgentName("agy", "TOKEN", func() error { return nil }, func() ([]string, error) {
		return []string{"────────", ">", "────────", "? for shortcuts"}, nil
	}, func(string) (bool, error) { sends++; return true, nil }, func() error { return errors.New("deadline") })
	if err == nil || sends != 1 {
		t.Fatalf("timeout retried: %d, %v", sends, err)
	}
}

func TestWatchUnnamedAGYOffersNameWithoutLocalSuggestion(t *testing.T) {
	s := watchSample{Session: Session{Tool: "agy", Title: "Command Prompt - agy --dangerously-skip-permissions"}, NamingAgentStarted: "agent"}
	if !watchCanReviewName(s) || watchRowDescription(s) != "Unnamed session - choose name" {
		t.Fatal("unnamed AGY missing naming affordance")
	}
	f := newWatchPanelFrame([]watchEntry{{Sample: s}}, nil, 0, 1, false)
	if p := f.hit(290, 74); p.Kind != "name" {
		t.Fatalf("name control %q", p.Kind)
	}
	if p := f.hit(100, 60); p.Kind != "name" {
		t.Fatalf("description %q", p.Kind)
	}
	s.NamingAgentStarted = ""
	if watchCanReviewName(s) {
		t.Fatal("naming without identity still offered")
	}
}

func TestWatchPermanentRenameNamedSession(t *testing.T) {
	s := watchSample{Session: Session{PID: 1, AgentPID: 2, Tool: "agy", Title: "Music practice", Name: "Old launch label"}, NamingAgentStarted: "agent"}
	if !watchCanReviewName(s) || watchRowDescription(s) != "Old launch label" {
		t.Fatal("named session must retain label and offer rename")
	}
	f := newWatchPanelFrame([]watchEntry{{Sample: s, Parked: true}}, nil, 0, 1, false)
	if p := f.hit(290, 74); p.Kind != "name" {
		t.Fatalf("parked named session control %q", p.Kind)
	}
	if p := f.hit(100, 60); p.Kind != "row" {
		t.Fatalf("named description must still activate: %q", p.Kind)
	}
	watchApplyNameRecord(&s, map[string]watchNameRecord{watchNameKey(s): {Name: "Music practice"}}, false)
	if s.Name != "Music practice" {
		t.Fatal("old launch label hid confirmed renamed title")
	}
}

func TestWatchAgentNameSuggestionIsNotApproval(t *testing.T) {
	dir := t.TempDir()
	s := watchSample{Session: Session{PID: 1, AgentPID: 2, Tool: "agy", Title: "agy --dangerously-skip-permissions"}, RootStarted: "root", NamingAgentStarted: "agent"}
	if err := watchSaveName(dir, s, watchNameRecord{Suggestion: "Piano arpeggio practice"}); err != nil {
		t.Fatal(err)
	}
	watchApplyNameRecord(&s, watchReadNames(dir), true)
	if s.Name != "" || s.Title != "agy --dangerously-skip-permissions" || s.SuggestedName != "Piano arpeggio practice" {
		t.Fatalf("suggestion approved or lost: %+v", s)
	}
	watchApplyNameRecord(&s, watchReadNames(dir), false)
	if s.SuggestedName != "" {
		t.Fatal("disabled suggestion displayed")
	}
	if err := watchSaveName(dir, s, watchNameRecord{Dismissed: true}); err != nil {
		t.Fatal(err)
	}
	watchApplyNameRecord(&s, watchReadNames(dir), true)
	if s.SuggestedName != "" {
		t.Fatal("dismissed model suggestion returned")
	}
}
