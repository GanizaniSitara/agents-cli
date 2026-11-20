package main

import "testing"

func TestWSLSessionIdentity(t *testing.T) {
	if wslSessionKey(" E1036CEC-25F7-4685-9706-2A30423DC26E\r") != "e1036cec-25f7-4685-9706-2a30423dc26e" || wslSessionKey("copilot") != "" {
		t.Fatal("invalid pane identity")
	}
	if got := wslText([]byte{0xff, 0xfe, 'U', 0, 'b', 0}); got != "Ub" {
		t.Fatal(got)
	}
}
func TestExplicitDesktopClickStep(t *testing.T) {
	list := []watchDesktop{{ID: "a"}, {ID: "b"}, {ID: "c"}}
	for _, tc := range []struct {
		from, to string
		step     int
		ok       bool
	}{{"a", "c", 2, true}, {"c", "a", -2, true}, {"b", "b", 0, true}, {"", "a", 0, false}, {"a", "gone", 0, false}} {
		step, ok := desktopStep(list, tc.from, tc.to)
		if step != tc.step || ok != tc.ok {
			t.Fatal(tc, step, ok)
		}
	}
}
