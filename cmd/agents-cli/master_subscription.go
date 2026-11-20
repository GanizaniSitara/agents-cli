package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type subscriptionConfig struct {
	Thread     string `json:"thread"`
	Binary     string `json:"binary"`
	Generation int64  `json:"generation"`
}
type subscriptionState struct {
	Generation int64  `json:"generation"`
	Watcher    uint32 `json:"watcher"`
	Started    string `json:"watcher_started"`
	Cursor     uint64 `json:"cursor"`
	Delivered  int    `json:"delivered"`
	Status     string `json:"status"`
}

func subscriptionWrite(path string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".subscription-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, err = f.Write(data)
	closing := f.Close()
	if err != nil {
		return err
	}
	if closing != nil {
		return closing
	}
	return os.Rename(f.Name(), path)
}
func subscriptionRead(path string, dst any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, dst)
}

var subscriptionUUID = regexp.MustCompile(`(?i)^[0-9a-f]{8}(-[0-9a-f]{4}){3}-[0-9a-f]{12}$`)

func masterSubscriptionCommand(args []string) error {
	dir, err := watchDirectory(parsed{})
	if err != nil {
		return err
	}
	path := filepath.Join(dir, "master-subscription.json")
	var cfg subscriptionConfig
	switch args[0] {
	case "subscribe":
		if len(args) != 3 || !filepath.IsAbs(args[1]) || !strings.EqualFold(filepath.Base(args[1]), "codex.exe") || !subscriptionUUID.MatchString(args[2]) {
			return errors.New("usage: master subscribe ABSOLUTE_CODEX_EXE THREAD_UUID")
		}
		if _, err = os.Stat(args[1]); err != nil {
			return err
		}
		err = subscriptionRead(path, &cfg)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if cfg.Thread != "" && (!strings.EqualFold(cfg.Thread, args[2]) || !strings.EqualFold(cfg.Binary, args[1])) {
			return errors.New("unsubscribe the current master before changing targets")
		}
		if cfg.Thread == "" {
			cfg = subscriptionConfig{args[2], args[1], time.Now().UnixNano()}
			if err = os.MkdirAll(dir, 0700); err != nil {
				return err
			}
			if err = subscriptionWrite(path, cfg); err != nil {
				return err
			}
		}
	case "unsubscribe":
		if len(args) != 1 {
			return errors.New("usage: master unsubscribe")
		}
		if err = subscriptionWrite(path, cfg); err != nil {
			return err
		}
	case "subscription-status":
		if len(args) != 1 {
			return errors.New("usage: master subscription-status")
		}
		err = subscriptionRead(path, &cfg)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	var state subscriptionState
	if subscriptionRead(filepath.Join(dir, "master-subscription-state.json"), &state) != nil || state.Generation != cfg.Generation {
		state = subscriptionState{}
	}
	live := state.Watcher != 0 && startTime(state.Watcher).Format(time.RFC3339Nano) == state.Started
	emit(map[string]any{"enabled": cfg.Thread != "", "thread": cfg.Thread, "connected": live && cfg.Thread != "", "state": state})
	return nil
}

func subscriptionAllows(s watchSample, thread string) bool {
	if strings.EqualFold(s.ThreadID, thread) || strings.EqualFold(s.SessionID, thread) {
		return false
	}
	// A scanned Codex window can have no proven conversation identity.
	return s.Tool != "codex" || s.ThreadID != "" || s.SessionID != ""
}

func (a *watchApp) runMasterSubscription(interval time.Duration, wake func(string, string, masterEvent) (bool, error)) {
	tick := time.NewTicker(interval)
	defer tick.Stop()
	cfgPath := filepath.Join(a.dir, "master-subscription.json")
	statePath := filepath.Join(a.dir, "master-subscription-state.json")
	var state subscriptionState
	_ = subscriptionRead(statePath, &state)
	var attached int64
	var unsaved bool
	type completedMirror struct{ root, marker string }
	mirrors := map[uint32]completedMirror{}
	for {
		select {
		case <-a.stop:
			return
		case <-tick.C:
		}
		var cfg subscriptionConfig
		if subscriptionRead(cfgPath, &cfg) != nil || cfg.Thread == "" {
			attached = 0
			continue
		}
		if unsaved && state.Generation == cfg.Generation {
			unsaved = subscriptionWrite(statePath, state) != nil
			if unsaved {
				continue
			}
		}
		f := a.master
		f.mu.Lock()
		if !f.ready {
			f.mu.Unlock()
			continue
		}
		if attached != cfg.Generation {
			cursor := f.next
			if state.Generation == cfg.Generation {
				cursor = f.baseline
				if state.Watcher == uint32(os.Getpid()) && state.Started == a.started {
					cursor = state.Cursor
				}
			} else {
				state.Delivered = 0
			}
			state = subscriptionState{cfg.Generation, uint32(os.Getpid()), a.started, cursor, state.Delivered, "listening"}
			for pid, stop := range f.current {
				if stop.event.ReadOnly && stop.event.ID <= cursor && stop.event.Session.MasterCompletion != "" {
					mirrors[pid] = completedMirror{stop.event.Session.RootStarted, stop.event.Session.MasterCompletion}
				}
			}
			f.mu.Unlock()
			if subscriptionWrite(statePath, state) == nil {
				attached = cfg.Generation
			}
			continue
		}
		var ev masterEvent
		for _, stop := range f.current {
			id := stop.event.ID
			if !stop.answered && id > state.Cursor && (ev.ID == 0 || id < ev.ID) {
				ev = stop.event
			}
		}
		f.mu.Unlock()
		if ev.ID == 0 {
			continue
		}
		// Persist before dispatch: a crash after queue acceptance cannot replay this stop.
		next := state
		next.Cursor = ev.ID
		next.Status = "listening"
		s := ev.Session
		lines, moreErr := f.more(s.PID, ev.ID)
		if s.Tool == "codex" && s.ThreadID == "" && s.SessionID == "" && s.AgentPID != 0 && s.NamingAgentStarted != "" && startTime(s.AgentPID).Format(time.RFC3339Nano) == s.NamingAgentStarted {
			s.ThreadID = resumeThread(commandLine(s.AgentPID))
			if s.ThreadID == "" {
				s.ThreadID = completedThread(s, lines, cfg.Thread, time.Now())
			}
		}
		allowed := subscriptionAllows(s, cfg.Thread)
		reason := "identity-unverified"
		if strings.EqualFold(s.ThreadID, cfg.Thread) || strings.EqualFold(s.SessionID, cfg.Thread) {
			reason = "self"
		}
		mirrorIdle := ev.ReadOnly && s.State == "idle" && s.PendingQuestions == 0 && strings.Contains(strings.ToLower(s.Name), "codex")
		marker := completedMirror{s.RootStarted, s.MasterCompletion}
		if mirrorIdle && (marker.marker == "" || mirrors[s.PID] == marker) {
			allowed = false
			reason = "mirror-no-new-completion"
		}
		if moreErr != nil {
			allowed = false
			reason = "expired"
		}
		if allowed {
			next.Status = "dispatch pending confirmation"
		}
		if subscriptionWrite(statePath, next) != nil {
			continue
		}
		state = next
		if !allowed {
			subscriptionAudit(a.dir, ev, reason)
			continue
		}
		var current subscriptionConfig
		if subscriptionRead(cfgPath, &current) != nil || current != cfg {
			subscriptionAudit(a.dir, ev, "subscription-changed")
			continue
		}
		if _, err := f.more(ev.Session.PID, ev.ID); err != nil {
			subscriptionAudit(a.dir, ev, "expired")
			continue
		}
		if mirrorIdle {
			mirrors[s.PID] = marker
		}
		resume, err := wake(cfg.Binary, cfg.Thread, ev)
		if err != nil {
			subscriptionAudit(a.dir, ev, "delivery-unconfirmed")
			state.Status = fmt.Sprintf("delivery unconfirmed: %v", err)
		} else {
			subscriptionAudit(a.dir, ev, "queued")
			state.Delivered++
			state.Status = "listening"
			if !resume {
				state.Status = "queued; this Codex has no resume option"
			}
		}
		unsaved = subscriptionWrite(statePath, state) != nil
	}
}
