package main

import "fmt"

// Keep background desktops visible as counts, never as individual panel rows.
func watchPanelLayout(entries []watchEntry, current string, desktops []watchDesktop) ([]watchEntry, []string) {
	rows := []watchEntry{}
	type counts struct{ stopped, working, unknown int }
	totals := map[string]*counts{}
	for _, entry := range entries {
		s := entry.Sample
		if current != "" && s.DesktopID == current && s.State != "busy" && s.Phase != "working" {
			if entry.Stopped || s.State == "unknown" || s.State == "error" {
				rows = append(rows, entry)
			}
			continue
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
	summaries := []string{}
	for _, desktop := range append(append([]watchDesktop{}, desktops...), watchDesktop{Name: "Desktop unknown"}) {
		if c := totals[desktop.ID]; c != nil {
			if desktop.ID == current {
				if c.working > 0 {
					summaries = append(summaries, fmt.Sprintf("This desktop · %d working", c.working))
				}
			} else {
				text := fmt.Sprintf("%s · %d stopped · %d working", desktop.Name, c.stopped, c.working)
				if c.unknown > 0 {
					text += fmt.Sprintf(" · %d unknown", c.unknown)
				}
				summaries = append(summaries, text)
			}
		}
	}
	return rows, summaries
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
