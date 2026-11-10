package main

import "time"

const watchReadDeadline = 4 * time.Second

// Allow a bounded read, the normal two-second pause and scheduling slack.
const watchHeartbeatTimeout = 9 * time.Second

// A progressing scan can take longer than one read's hard deadline. Publish
// each completed row before the next read; partial batches cannot remove rows.
func watchPublishProgress(batch watchBatch, published *int, encode func(any) error, reset func(time.Duration) bool) error {
	progress := watchBatch{Partial: true, ReadingPID: batch.ReadingPID, Samples: batch.Samples[*published:], Error: batch.Error, ConsoleEvents: batch.ConsoleEvents}
	if err := encode(progress); err != nil {
		return err
	}
	*published = len(batch.Samples)
	reset(watchReadDeadline)
	return nil
}

// A restarted reader begins after the console it last attempted, so one hung
// console cannot repeatedly prevent every later session from being sampled.
func watchReadOrder(sessions []Session, afterPID uint32) []Session {
	for i, s := range sessions {
		if s.PID == afterPID && i+1 < len(sessions) {
			ordered := append([]Session(nil), sessions[i+1:]...)
			return append(ordered, sessions[:i+1]...)
		}
	}
	return sessions
}

func watchUnavailableSample(s watchSample, reason string) watchSample {
	s.State, s.Phase, s.Reason = "unknown", "unknown", reason
	s.Working, s.PendingQuestions, s.WorkProcesses = false, 0, 0
	s.Agents = watchAgents{Source: "status unavailable"}
	return s
}

func (w *watchTracker) unavailable(reason string, now time.Time) {
	for _, entry := range w.entries {
		entry.unavailable(reason, now)
	}
}

func (entry *watchEntry) unavailable(reason string, now time.Time) {
	if entry.Sample.State != "unknown" {
		entry.Changed = now
	}
	entry.Sample = watchUnavailableSample(entry.Sample, reason)
	entry.Stopped, entry.Attention = false, false
	entry.Candidate = time.Time{}
}
