package main

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"sync"
)

// A feed lives for one watcher lifetime. Its cursor is scoped to that instance.
type masterEvent struct {
	ID       uint64
	Session  watchSample
	Lines    []string
	ReadOnly bool
}

type masterStop struct {
	event    masterEvent
	context  []string
	answered bool
}

type masterFeed struct {
	mu       sync.Mutex
	next     uint64
	current  map[uint32]*masterStop
	changed  chan struct{}
	ready    bool
	baseline uint64
}

var selectedMasterChoice = regexp.MustCompile(`^[›❯]\s*[0-9]+[.)]\s+\S`)

func newMasterFeed() *masterFeed {
	return &masterFeed{current: make(map[uint32]*masterStop), changed: make(chan struct{})}
}

func masterLines(lines []string) []string {
	var result []string
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" || strings.Trim(line, "─━") == "" ||
			(strings.HasPrefix(line, "›") || strings.HasPrefix(line, "❯")) && !selectedMasterChoice.MatchString(line) ||
			strings.Contains(line, "Context ") && strings.Contains(line, "% left") ||
			strings.Contains(line, "for shortcuts") || strings.Contains(line, "f2 to view") ||
			strings.HasPrefix(line, "Worked for ") {
			continue
		}
		r := []rune(line)
		if len(r) > 400 {
			line = string(r[:400])
		}
		result = append(result, line)
	}
	if len(result) > 24 {
		result = result[len(result)-24:]
	}
	return result
}

// observe consumes the tracker's decision and lines from the same cached read.
// Call forget on removal. No console access takes place in this adapter.
func (f *masterFeed) observe(e watchEntry, lines []string, mirror bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := e.Sample
	old := f.current[s.PID]
	if !e.Stopped || e.Parked || e.Dismissed || s.PollingMuted || !e.Attention {
		delete(f.current, s.PID)
		return
	}
	ctx := masterLines(lines)
	if old != nil && watchSameClickSession(old.event.Session, s) && old.event.Session.RootStarted == s.RootStarted &&
		old.event.Session.Phase == s.Phase && old.event.Session.PendingQuestions == s.PendingQuestions && old.event.ReadOnly == mirror && old.event.Session.MasterCompletion == s.MasterCompletion {
		old.context = ctx
		return
	}
	f.next++
	excerpt := ctx
	if len(excerpt) > 4 {
		excerpt = excerpt[len(excerpt)-4:]
	}
	f.current[s.PID] = &masterStop{event: masterEvent{f.next, s, excerpt, mirror}, context: ctx}
	close(f.changed)
	f.changed = make(chan struct{})
}

func (f *masterFeed) forget(pid uint32) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.current, pid)
}

func (f *masterFeed) wait(ctx context.Context, after uint64) (masterEvent, error) {
	for {
		f.mu.Lock()
		var next *masterStop
		for _, s := range f.current {
			if !s.answered && s.event.ID > after && (next == nil || s.event.ID < next.event.ID) {
				next = s
			}
		}
		ch := f.changed
		if next != nil {
			event := next.event
			event.Lines = append([]string(nil), event.Lines...)
			f.mu.Unlock()
			return event, nil
		}
		f.mu.Unlock()
		select {
		case <-ctx.Done():
			return masterEvent{}, ctx.Err()
		case <-ch:
		}
	}
}

func (f *masterFeed) more(pid uint32, id uint64) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := f.current[pid]
	if s == nil || s.event.ID != id || s.answered {
		return nil, errors.New("stop expired")
	}
	return append([]string(nil), s.context...), nil
}

// Claim once before dispatch. A transport with uncertain delivery must not retry.
func (f *masterFeed) claim(pid uint32, id uint64) (watchSample, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := f.current[pid]
	if s == nil || s.event.ID != id || s.answered {
		return watchSample{}, errors.New("stop expired")
	}
	if s.event.ReadOnly {
		return watchSample{}, errors.New("read-only mirror")
	}
	s.answered = true
	return s.event.Session, nil
}
