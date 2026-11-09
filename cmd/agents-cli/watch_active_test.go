package main

import "testing"

func TestActiveWindowDoesNotShowWaitingAlert(t *testing.T) {
	rows := []watchEntry{{Sample: watchSample{HWND: 42, Phase: "waiting", Session: Session{State: "prompt"}}, Stopped: true}}
	active := watchActiveRows(rows, 42)
	if active[0].Sample.Phase != "active" || active[0].Stopped {
		t.Fatal("foreground window still shows an attention alert")
	}
	if rows[0].Sample.Phase != "waiting" || !rows[0].Stopped {
		t.Fatal("presentation changed underlying request")
	}
	if watchActiveRows(rows, 43)[0].Sample.Phase != "waiting" {
		t.Fatal("background request was hidden")
	}
}
