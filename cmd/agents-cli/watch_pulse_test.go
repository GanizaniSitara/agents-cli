package main

import "testing"

func TestWatchPulseEdgesOnNegativeMonitor(t *testing.T) {
	r := watchRect{-1920, -200, -20, 900}
	edges := watchPulseEdges(r)
	area := int32(0)
	for _, e := range edges {
		if e.Left < r.Left || e.Top < r.Top || e.Right > r.Right || e.Bottom > r.Bottom || e.Right <= e.Left || e.Bottom <= e.Top {
			t.Fatalf("edge outside target: %+v", e)
		}
		area += (e.Right - e.Left) * (e.Bottom - e.Top)
	}
	want := (r.Right-r.Left)*(r.Bottom-r.Top) - (r.Right-r.Left-12)*(r.Bottom-r.Top-12)
	if area != want {
		t.Fatalf("border area %d, want %d", area, want)
	}
}
