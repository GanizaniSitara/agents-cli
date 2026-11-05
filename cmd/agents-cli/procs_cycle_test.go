package main

import (
	"testing"
	"time"
)

// Windows retains a dead parent's PID; reuse can make a snapshot cyclic.
func TestProcessWalksTerminateOnCycles(t *testing.T) {
	for _, self := range []bool{false, true} {
		m := procMap{
			10: {PID: 10, PPID: 20, Name: "cmd.exe"},
			20: {PID: 20, PPID: 10, Name: "node.exe"},
		}
		if self {
			m[10].PPID = 10
		}
		for name, walk := range map[string]func(){
			"root": func() { m.rootOf(m[10]) },
			"ancestor": func() {
				if m.ancestorNamed(10, "claude.exe") {
					t.Error("invented Claude ancestor")
				}
			},
			"caller": func() {
				if m.callerClaudeAncestor(10) != 0 {
					t.Error("invented Claude caller")
				}
			},
			"depth": func() {
				if got := m.depth(m[10], 99); got > len(m) {
					t.Errorf("depth %d exceeds process count %d", got, len(m))
				}
			},
			"descendants": func() {
				for _, pid := range m.descendants(10) {
					if pid == 10 {
						t.Error("root included in descendants")
					}
				}
			},
		} {
			t.Run(name, func(t *testing.T) {
				done := make(chan struct{})
				go func() { walk(); close(done) }()
				select {
				case <-done:
				case <-time.After(100 * time.Millisecond):
					t.Fatal("process walk spun on cyclic parent IDs")
				}
			})
		}
	}
}
