package main

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Session is one row of `ls`: a console window (or background pty) hosting an
// agent. PID is the console-owning root, the thing the launcher recorded and
// the thing `stop` kills; AgentPID is the agent process itself.
type Session struct {
	PollingMuted      bool   `json:"polling_muted,omitempty"`
	PID               uint32 `json:"pid"`
	AgentPID          uint32 `json:"agent_pid,omitempty"`
	Tool              string `json:"tool"`
	Variant           string `json:"variant,omitempty"` // codex | codex2
	Home              string `json:"home,omitempty"`    // CODEX_HOME for codex sessions
	Exe               string `json:"exe,omitempty"`     // the agent's own binary
	Kind              string `json:"kind"`              // window | bg
	Name              string `json:"name,omitempty"`
	Title             string `json:"title,omitempty"`
	Zone              *int   `json:"zone,omitempty"`
	Started           string `json:"started,omitempty"`
	Alive             bool   `json:"alive"`
	Source            string `json:"source"` // launch-info | scan | adopted | launch-info+scan ...
	State             string `json:"state,omitempty"`
	CanNative         bool   `json:"can_native"`
	NativeUnavailable string `json:"native_unavailable,omitempty"`
	Self              bool   `json:"self,omitempty"`
	SessionID         string `json:"session_id,omitempty"`
	ThreadID          string `json:"thread_id,omitempty"`
}

type discoverOpts struct {
	includeDead bool
	withScreen  bool // peek each session for title + state
	toolFilter  string
}

func discover(opts discoverOpts) ([]Session, procMap, error) {
	procs, err := snapshot()
	if err != nil {
		return nil, nil, err
	}
	procs.fillCmd()
	selfRoot := procs.selfRoot()

	byRoot := map[uint32]*Session{}

	// 1. Process scan: every classified agent process, folded onto its root.
	//    When one root hosts several agents (Claude running codex exec in its
	//    Bash tool), the shallowest one is the window's own TUI.
	type cand struct {
		s     *Session
		depth int
	}
	best := map[uint32]cand{}
	for _, p := range procs {
		tool := classify(p)
		if tool == "" {
			continue
		}
		root, kind := procs.rootOf(p)
		d := procs.depth(p, root.PID)
		if cur, ok := best[root.PID]; ok && cur.depth <= d {
			continue
		}
		best[root.PID] = cand{depth: d, s: &Session{
			PID: root.PID, AgentPID: p.PID, Tool: tool, Kind: kind, Alive: true, Source: "scan",
			Exe: exePath(commandLine(p.PID)),
		}}
	}
	for pid, c := range best {
		if c.s.Tool == "copilot" {
			// npm-loader.js launches the native executable. Expose that PID so
			// either the cmd root or the actual Copilot process resolves.
			for _, d := range procs.descendants(c.s.AgentPID) {
				if dp := procs[d]; dp != nil && dp.Name == "copilot.exe" {
					c.s.AgentPID = dp.PID
					c.s.Exe = exePath(commandLine(dp.PID))
					break
				}
			}
		}
		if c.s.Tool == "codex" {
			// The real codex.exe (child of the node shim) carries the profile;
			// prefer it over the shim for home/exe so native sends hit the
			// same install the window runs.
			agent := procs[c.s.AgentPID]
			for _, d := range procs.descendants(c.s.AgentPID) {
				if dp := procs[d]; dp != nil && dp.Name == "codex.exe" {
					agent = dp
					break
				}
			}
			if agent != nil {
				c.s.AgentPID = agent.PID
				c.s.Home, c.s.Variant = codexHome(agent)
				if hasCommandFlag(agent.Cmd, "--no-daemon") {
					c.s.NativeUnavailable = "session uses --no-daemon; use --via inject"
				}
				if agent.Name == "codex.exe" {
					c.s.Exe = exePath(commandLine(agent.PID))
				}
			}
		}
		byRoot[pid] = c.s
	}

	// 2. launch-info: authoritative names/zones/ids for launcher-started tasks.
	for pid, li := range readLaunchInfos() {
		if !launchMatches(li, startTime(pid)) {
			continue // stale launcher metadata must never target a reused PID
		}
		s, ok := byRoot[pid]
		if !ok {
			s = &Session{PID: pid, Tool: strings.ToLower(li.Tool), Kind: "window", Alive: isAlive(pid), Source: "launch-info"}
			if !s.Alive && !opts.includeDead {
				continue
			}
			byRoot[pid] = s
		} else {
			s.Source = "launch-info+scan"
		}
		s.Name = li.TaskSlug
		s.Zone = li.Zone
		s.SessionID = li.SessionId
		s.ThreadID = li.ThreadId
		if s.Title == "" {
			s.Title = li.WindowTitle
		}
		if s.Started == "" {
			s.Started = li.ProcessStartTimeUtc
		}
	}

	// 3. Adopted/labelled sessions.
	adoptedMap := readAdopted()
	for key, a := range adoptedMap {
		pid64, err := strconv.ParseUint(key, 10, 32)
		if err != nil {
			continue
		}
		pid := uint32(pid64)
		start := startTime(pid)
		if _, ok := adoptedFor(adoptedMap, pid, start); !ok {
			continue
		}
		s, ok := byRoot[pid]
		if !ok {
			alive := isAlive(pid)
			if !alive && !opts.includeDead {
				continue
			}
			s = &Session{PID: pid, Tool: a.Tool, Kind: "window", Alive: alive, Source: "adopted"}
			byRoot[pid] = s
		} else {
			s.Source += "+adopted"
			if s.Tool == "" {
				s.Tool = a.Tool
			}
		}
		if a.Name != "" {
			s.Name = a.Name
		}
		s.PollingMuted = a.PollingMuted
	}

	var out []Session
	for _, s := range byRoot {
		if opts.toolFilter != "" && s.Tool != opts.toolFilter {
			continue
		}
		s.Self = s.PID == selfRoot
		if s.Tool == "codex" && s.NativeUnavailable == "" {
			switch {
			case !s.Alive:
				s.NativeUnavailable = "session is not running"
			case s.AgentPID == 0 || !strings.HasSuffix(strings.ToLower(s.Exe), "codex.exe"):
				s.NativeUnavailable = "session executable could not be identified"
			case s.Home == "":
				s.NativeUnavailable = "session profile could not be identified"
			case s.ThreadID == "" && s.SessionID == "":
				s.NativeUnavailable = "session has no thread id in launch-info"
			}
		}
		s.CanNative = s.Tool == "codex" && s.NativeUnavailable == ""
		if s.Started == "" {
			if t := startTime(s.PID); !t.IsZero() {
				s.Started = t.Format(time.RFC3339)
			}
		}
		refreshScreen(s, opts.withScreen, peek)
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool {
		zi, zj := 1<<30, 1<<30
		if out[i].Zone != nil {
			zi = *out[i].Zone
		}
		if out[j].Zone != nil {
			zj = *out[j].Zone
		}
		if zi != zj {
			return zi < zj
		}
		return out[i].PID < out[j].PID
	})
	return out, procs, nil
}

// Muting is checked before console access, including ordinary ls sweeps.
func refreshScreen(s *Session, enabled bool, read func(uint32, int) (*peekResult, error)) {
	if s.PollingMuted {
		s.State = "muted"
		return
	}
	if !enabled || !s.Alive {
		return
	}
	if r, err := read(s.PID, 0); err == nil {
		if r.Title != "" {
			s.Title = r.Title
		}
		s.State = guessState(s.Tool, r.Lines)
	} else if s.State == "" {
		s.State = "unknown"
	}
}

func requirePolling(s *Session) error {
	if s.PollingMuted {
		return fmt.Errorf("session %d polling is muted; use agents-cli unmute %d before reading its screen", s.PID, s.PID)
	}
	return nil
}

// resolve turns a <ref> into exactly one session. Order: pid (root or agent),
// exact name/slug, unique substring of name or title.
func resolve(ref string, sessions []Session) (*Session, error) {
	if n, err := strconv.ParseUint(ref, 10, 32); err == nil {
		for i := range sessions {
			if sessions[i].PID == uint32(n) || sessions[i].AgentPID == uint32(n) {
				return &sessions[i], nil
			}
		}
		return nil, fmt.Errorf("no agent session with pid %d (try `agents-cli ls --all`, or `adopt`)", n)
	}
	lower := strings.ToLower(ref)
	var exact []Session
	for i := range sessions {
		if strings.EqualFold(sessions[i].Name, ref) {
			exact = append(exact, sessions[i])
		}
	}
	if len(exact) == 1 {
		return &exact[0], nil
	}
	if len(exact) > 1 {
		sessions = exact
	}
	var hits []int
	for i := range sessions {
		if strings.Contains(strings.ToLower(sessions[i].Name), lower) || strings.Contains(strings.ToLower(sessions[i].Title), lower) {
			hits = append(hits, i)
		}
	}
	switch len(hits) {
	case 1:
		return &sessions[hits[0]], nil
	case 0:
		return nil, fmt.Errorf("no session matches %q", ref)
	}
	var names []string
	for _, i := range hits {
		names = append(names, fmt.Sprintf("%d:%s", sessions[i].PID, firstNonEmpty(sessions[i].Name, sessions[i].Title)))
	}
	return nil, fmt.Errorf("%q is ambiguous: %s", ref, strings.Join(names, ", "))
}

func trimBlankTail(lines []string) []string {
	end := len(lines)
	for end > 0 && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	return lines[:end]
}

func firstNonEmpty(a ...string) string {
	for _, s := range a {
		if s != "" {
			return s
		}
	}
	return ""
}

// ---- state heuristic -------------------------------------------------------

var (
	// Claude: "✢ Misting… (10s · ↓ 550 tokens)" / "esc to interrupt"; Codex:
	// "Working (4m 43s • esc to interrupt)"; braille spinners elsewhere.
	spinnerRe = regexp.MustCompile(`[\x{2800}-\x{28FF}]|esc to interrupt|Esc to cancel|\(esc\)|Thinking|Working \(|\(\d+[smh][\d\w ]* · |…\s*\(`)
	promptRe  = regexp.MustCompile(`(?i)\(y/n\)|\[y/n\]|Yes, |Do you want|Do you trust|Allow |Approve|press enter to confirm|enter Confirm|❯ 1\.|› 1\.|\? for shortcuts.*\n.*\b1\. `)
	// A bare input prompt, or a TUI's empty-input placeholder.
	brailleOnlyRe = regexp.MustCompile(`^[\s\x{2800}-\x{28FF}]+$`)
	idleRe        = regexp.MustCompile(`^\s*(>|›|❯|\$|%)\s*$|^› Ask Codex|^> Try "|^>\s+Type your message|^[A-Za-z]:\\[^>]*>\s*$`)
)

// guessState reads the last screen lines. Spinner glyphs mean a turn is
// running; a visible question means the TUI is waiting on a human; a bare
// input prompt means idle. Anything else is honestly unknown.
func guessState(tool string, lines []string) string {
	if tool == "copilot" {
		return copilotState(lines)
	}
	// TUIs draw at the top of a tall window and leave the rest blank, so the
	// trailing blank rows carry no signal.
	tail := trimBlankTail(lines)
	if tool == "vibe" {
		// Vibe's start-up logo is braille art; its live spinner line always
		// carries "Generating…"/"to interrupt" text, so glyph-only rows are logo.
		kept := tail[:0:0]
		for _, l := range tail {
			if !brailleOnlyRe.MatchString(l) {
				kept = append(kept, l)
			}
		}
		tail = kept
	}
	if len(tail) > 18 {
		tail = tail[len(tail)-18:]
	}
	joined := strings.Join(tail, "\n")
	if promptRe.MatchString(joined) {
		return "prompt"
	}
	if spinnerRe.MatchString(joined) {
		return "busy"
	}
	for _, l := range tail {
		if idleRe.MatchString(l) {
			return "idle"
		}
	}
	return "unknown"
}
