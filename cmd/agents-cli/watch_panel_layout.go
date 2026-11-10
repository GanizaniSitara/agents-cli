package main

import (
	"fmt"
	"sort"
)

type watchPanelSummary struct {
	DesktopID string `json:"desktop_id"`
	Text      string `json:"text"`
	Expanded  bool   `json:"expanded"`
	Toggle    bool   `json:"toggle"`
}

// Background desktop rows are opt-in; opening a section changes only the view.
func watchPanelLayout(entries []watchEntry, current string, desktops []watchDesktop, expanded map[string]bool) ([]watchEntry, []watchPanelSummary) {
	rows := []watchEntry{}
	type counts struct{ stopped, working, unknown int }
	totals := map[string]*counts{}
	for _, entry := range entries {
		s := entry.Sample
		if current != "" && s.DesktopID == current {
			if entry.Stopped || s.State == "unknown" || s.State == "error" || s.State == "busy" || s.Phase == "working" {
				rows = append(rows, entry)
			}
			continue
		}
		if s.DesktopID != current && expanded[s.DesktopID] {
			rows = append(rows, entry)
		}
		c := totals[s.DesktopID]
		if c == nil {
			c = &counts{}
			totals[s.DesktopID] = c
		}
		switch {
		case s.State == "busy" || s.Phase == "working":
			c.working++
		case s.State == "unknown":
			c.unknown++
		case entry.Stopped:
			c.stopped++
		}
	}
	// Keep all stops (including idle/check) ahead of expanded working rows.
	priority := func(row watchEntry) int {
		if row.Parked {
			return 6
		}
		if row.Sample.State == "busy" || row.Sample.Phase == "working" {
			return 5
		}
		return watchPanelGroup(row.Sample)
	}
	sort.SliceStable(rows, func(i, j int) bool { return priority(rows[i]) < priority(rows[j]) })
	summaries := []watchPanelSummary{}
	for _, desktop := range append(append([]watchDesktop{}, desktops...), watchDesktop{Name: "Desktop unknown"}) {
		if c := totals[desktop.ID]; c != nil {
			if desktop.ID == current {
				if c.working > 0 {
					summaries = append(summaries, watchPanelSummary{DesktopID: desktop.ID, Text: fmt.Sprintf("This desktop | %d working", c.working)})
				}
			} else {
				text := fmt.Sprintf("%s | %d stopped | %d working", desktop.Name, c.stopped, c.working)
				if c.unknown > 0 {
					text += fmt.Sprintf(" | %d unknown", c.unknown)
				}
				prefix := "[+] "
				if expanded[desktop.ID] {
					prefix = "[-] "
				}
				summaries = append(summaries, watchPanelSummary{DesktopID: desktop.ID, Text: prefix + text, Expanded: expanded[desktop.ID], Toggle: desktop.ID != ""})
			}
		}
	}
	return rows, summaries
}

func watchPanelSummaryIndex(y, rowCount, capacity, summaryCount int) int {
	if rowCount > capacity {
		rowCount = capacity
	}
	top := 34 + rowCount*watchRowHeight
	if y < top || y >= top+summaryCount*28 {
		return -1
	}
	return (y - top) / 28
}

func watchPanelCapacity(workHeight int32, summaryCount int) int {
	n := (int(workHeight) - 24 - 34 - summaryCount*28) / watchRowHeight
	if n < 1 {
		return 1
	}
	return n
}

func watchPanelStates(rows []watchEntry) map[uint32]string {
	states := make(map[uint32]string, len(rows))
	for _, row := range rows {
		states[row.Sample.PID] = row.Sample.Started + "/" + row.Sample.State + "/" + row.Sample.Phase
	}
	return states
}

func watchPanelNeedsWake(previous map[uint32]string, rows []watchEntry) bool {
	for pid, state := range watchPanelStates(rows) {
		if previous[pid] != state {
			return true
		}
	}
	return false
}
