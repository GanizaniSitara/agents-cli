package main

import "testing"

func TestShortSessionName(t *testing.T) {
	cases := map[string]string{
		"TECH-272-agents-cli-enforce-short-ticket-first-session-names": "TECH-272",
		"◐ CSF-166-conversational-voice-coordinator":                   "CSF-166",
		"mac:JPMT-212 resumed":                                         "JPMT-212",
		"OP-066.1-tiling":                                              "OP-066.1",
		"fix the flaky golf swing upload thing on the phone":           "fix the flaky golf swing",
		"short": "short",
		"工程師 界界界界界界界界界界界界界界界界界界界界界界界界":            "工程師 界界界界界界界界界界界界界界界界界界界界",
		"CSF-166abc-not-a-ticket-but-long-enough": "CSF-166abc-not-a-ticket",
	}
	for in, want := range cases {
		if got := shortSessionName(in); got != want {
			t.Errorf("shortSessionName(%q) = %q, want %q", in, got, want)
		}
	}
	if got := []rune(shortSessionName("naïve-ünïcode-name-that-is-really-long-indeed")); len(got) > sessionNameMax {
		t.Errorf("unicode name too long: %q", string(got))
	}
}

func TestResolveTicketRefIsWholeToken(t *testing.T) {
	sessions := []Session{
		{PID: 1, Name: "CSF-166", Task: "CSF-166-conversational-voice"},
		{PID: 2, Name: "CSF-16", Task: "CSF-16-older-work"},
	}
	for ref, want := range map[string]uint32{"CSF-16": 2, "csf-166": 1, "CSF-166-conversational-voice": 1} {
		s, err := resolve(ref, sessions)
		if err != nil || s.PID != want {
			t.Errorf("resolve(%q) = %v, %v; want pid %d", ref, s, err, want)
		}
	}
	if _, err := resolve("CSF-1", sessions); err == nil {
		t.Errorf("CSF-1 must not match CSF-16 or CSF-166")
	}
}
