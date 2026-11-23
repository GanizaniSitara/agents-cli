package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unsafe"

	"golang.org/x/sys/windows"
)

type watchNameRecord struct {
	Name       string `json:"name,omitempty"`
	Dismissed  bool   `json:"dismissed,omitempty"`
	Suggestion string `json:"suggestion,omitempty"`
}

func watchApplyNameRecord(s *watchSample, names map[string]watchNameRecord, enabled bool) {
	if r, ok := names[watchNameKey(*s)]; ok {
		s.SuggestedName = ""
		if r.Suggestion != "" && enabled && watchGenericTitle(s.Session) {
			s.SuggestedName = r.Suggestion
		}
		if r.Name != "" && (s.Title == r.Name || (s.Name == "" && watchGenericTitle(s.Session))) {
			s.Name = r.Name
		}
	} else if !watchGenericTitle(s.Session) {
		s.SuggestedName = ""
	}
	if !enabled {
		s.SuggestedName = ""
	}
}

func watchNameKey(s watchSample) string {
	return fmt.Sprintf("%d/%s/%d/%s", s.PID, s.RootStarted, s.AgentPID, s.NamingAgentStarted)
}

func watchReadNames(dir string) map[string]watchNameRecord {
	m := map[string]watchNameRecord{}
	if b, err := os.ReadFile(filepath.Join(dir, "names.json")); err == nil {
		_ = json.Unmarshal(b, &m)
	}
	if m == nil {
		m = map[string]watchNameRecord{}
	}
	return m
}

func watchSaveName(dir string, s watchSample, r watchNameRecord) error {
	m := watchReadNames(dir)
	m[watchNameKey(s)] = r
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, "names.tmp")
	if err = os.WriteFile(tmp, b, 0600); err != nil {
		return err
	}
	return windows.MoveFileEx(windows.StringToUTF16Ptr(tmp), windows.StringToUTF16Ptr(filepath.Join(dir, "names.json")), windows.MOVEFILE_REPLACE_EXISTING)
}

func watchGenericTitle(s Session) bool {
	if s.Name != "" {
		return false
	}
	t := strings.ToLower(strings.TrimSpace(watchComparableTitle(s.Title)))
	for _, prefix := range []string{"command prompt - ", "administrator: ", "cmd.exe - "} {
		t = strings.TrimPrefix(t, prefix)
	}
	if t == "" || t == "command prompt" || t == "cmd" || t == "cmd.exe" || t == "windows powershell" {
		return true
	}
	// AGY leaves its launch command as the console title. This permission
	// switch describes how it was started, not what the conversation is about.
	if s.Tool == "agy" {
		fields := strings.Fields(t)
		if len(fields) == 2 && (fields[0] == "agy" || fields[0] == "agy.exe") && fields[1] == "--dangerously-skip-permissions" {
			return true
		}
	}
	for _, tool := range []string{"codex", "claude", "copilot", "vibe", "agy", "qwen", "opencode"} {
		if t == tool || t == tool+".exe" || strings.HasPrefix(t, tool+" resume ") || strings.HasPrefix(t, tool+" --resume ") {
			return true
		}
	}
	return false
}

// Only explicit user-prompt rows supply context. Never guess from assistant
// prose, a shell command, startup placeholders, or a screen full of tool output.
func watchSuggestName(s Session, lines []string) string {
	if !watchGenericTitle(s) {
		return ""
	}
	for i, line := range lines {
		line = strings.TrimSpace(line)
		text := ""
		for _, marker := range []string{"› ", "❯ ", "> "} {
			if strings.HasPrefix(line, marker) {
				text = strings.TrimSpace(strings.TrimPrefix(line, marker))
				break
			}
		}
		lower := strings.ToLower(text)
		if len(strings.Fields(text)) < 3 || strings.HasPrefix(text, "/") || strings.ContainsAny(text, "\\`|<>{}") {
			continue
		}
		// A composer draft is not conversation context. Require a subsequent
		// assistant/activity marker before offering this submitted request.
		responded := false
		for _, after := range lines[i+1:] {
			after = strings.TrimSpace(after)
			if strings.HasPrefix(after, "› ") || strings.HasPrefix(after, "❯ ") || strings.HasPrefix(after, "> ") {
				break
			}
			for _, marker := range []string{"• ", "● ", "✢ ", "✳ ", "✻ ", "✽ "} {
				if strings.HasPrefix(after, marker) {
					responded = true
				}
			}
		}
		if !responded {
			continue
		}
		skip := false
		for _, prefix := range []string{"ask codex", "try ", "type your", "help ", "git ", "npm ", "go ", "python ", "curl ", "user:", "system:", "please rename", "rename title", "rename this window", "agents-cli naming request"} {
			if strings.HasPrefix(lower, prefix) {
				skip = true
			}
		}
		if skip || strings.Contains(lower, "http:") || strings.Contains(lower, "https:") {
			continue
		}
		words := strings.Fields(text)
		if len(words) > 8 {
			words = words[:8]
		}
		text = strings.TrimRight(strings.Join(words, " "), ".,;:!?")
		r := []rune(text)
		if len(r) > 64 {
			r = r[:64]
			text = strings.TrimSpace(string(r))
		}
		if text != "" {
			r = []rune(text)
			r[0] = unicode.ToUpper(r[0])
			return string(r)
		}
	}
	return ""
}

func watchValidName(name string) bool {
	if strings.TrimSpace(name) != name || name == "" || len([]rune(name)) > 80 {
		return false
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return false
		}
	}
	if watchNameHasPath(name) {
		return false
	}
	return true
}

func watchNameHasPath(name string) bool {
	if strings.Contains(name, `\`) {
		return true
	}
	for i := 1; i+1 < len(name); i++ {
		if name[i] == ':' && name[i+1] == '/' && ((name[i-1] >= 'A' && name[i-1] <= 'Z') || (name[i-1] >= 'a' && name[i-1] <= 'z')) {
			return true
		}
	}
	for _, word := range strings.Fields(name) {
		if strings.HasPrefix(strings.Trim(word, `"'`), "/") && strings.Count(word, "/") > 1 {
			return true
		}
	}
	return false
}

// cmd can append the running executable and arguments to a useful title.
// Remove only a recognizable launch suffix, before the owner reviews the name.
func watchShortName(name string) string {
	if user := os.Getenv("USERNAME"); user != "" {
		suffix := " | " + user
		if strings.HasSuffix(strings.ToLower(name), strings.ToLower(suffix)) {
			name = strings.TrimSpace(name[:len(name)-len(suffix)])
		}
	}
	for _, separator := range []string{"  - ", " - ", " – "} {
		if i := strings.Index(name, separator); i > 0 && watchNameHasPath(name[i+len(separator):]) {
			name = name[:i]
		}
	}
	if watchNameHasPath(name) {
		return ""
	}
	return strings.TrimSpace(name)
}

type watchNameReview struct {
	Conversation  string
	RenameSession bool
	Dir           string
	Sample        watchSample
	Name          string
	Action        string
	Error         string
}

func watchReviewName(ctx context.Context, dir string, s watchSample) {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	r := watchNameReview{Dir: dir, Sample: s, Name: firstNonEmpty(watchShortName(s.SuggestedName), watchShortName(s.Name), watchShortName(s.Title), s.Tool), RenameSession: watchHarnessRenameSupported(s.Tool)}
	if s.Tool == "claude" {
		_, record, err := claudeSessionRecord(s)
		if err == nil {
			r.Conversation = record.SessionID
		}
	}
	input, _ := json.Marshal(r)
	cmd := helperCommand(ctx, exe, string(input), "name-review", strconv.FormatUint(uint64(s.PID), 10))
	cmd.WaitDelay = 250 * time.Millisecond
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Start(); err != nil {
		return
	}
	// The owner clicked Rename in this process. Pass that foreground right
	// to the new dialog process so its edit can receive keyboard input.
	watchUser.NewProc("AllowSetForegroundWindow").Call(uintptr(cmd.Process.Pid))
	err = cmd.Wait()
	var result watchNameReview
	if err != nil || json.Unmarshal(out.Bytes(), &result) != nil {
		return
	}
	if result.Action == "accept" && result.Error == "" {
		err = watchSaveName(dir, s, watchNameRecord{Name: result.Name})
	}
	if result.Action == "dismiss" {
		err = watchSaveName(dir, s, watchNameRecord{Dismissed: true})
	}
	if err != nil {
		watchRuntimeEvent(dir, "name-save-failed", "", 0)
	}
}

func watchDoRename(s watchSample, name string) error {
	if !watchValidName(name) {
		return errors.New("Use a short name (1–80 characters), without paths or control characters.")
	}
	if problem := watchTargetProblem(s, watchReadTarget(s)); problem != "" {
		return errors.New(problem)
	}
	if s.NamingAgentStarted == "" || startTime(s.AgentPID).Format(time.RFC3339Nano) != s.NamingAgentStarted {
		return errors.New("Agent process changed. Close this review and try again.")
	}
	if err := attach(s.PID); err != nil {
		return err
	}
	defer pFreeConsole.Call()
	h, _, _ := k32.NewProc("GetConsoleWindow").Call()
	if (h != s.HWND && watchConsolePresentation(h) != s.HWND) || watchTerminalConsoleTitle() != s.Title {
		return errors.New("Session or title changed. Close this review and try again.")
	}
	ptr, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return err
	}
	terminal := watchWindowClass(s.HWND) == "CASCADIA_HOSTING_WINDOW_CLASS"
	if terminal {
		probe := s
		probe.Title = name
		if problem := watchDoTerminalTab(probe, true); problem != "terminal-tab-not-found" {
			return errors.New("Suggested title already exists in this Terminal, or tabs could not be checked. Choose another name.")
		}
		if watchTerminalConsoleTitle() != s.Title {
			return errors.New("Session title changed during review.")
		}
	}
	if ok, _, e := k32.NewProc("SetConsoleTitleW").Call(uintptr(unsafe.Pointer(ptr))); ok == 0 {
		return e
	}
	if watchTerminalConsoleTitle() != name {
		return errors.New("Console title change was not confirmed.")
	}
	if terminal {
		// Terminal may suppress application titles. Check the actual tab without
		// selecting it; never rename the shared top-level Terminal HWND.
		original := s.Title
		s.Title = name
		for i := 0; i < 5; i++ {
			if watchDoTerminalTab(s, true) == "" {
				return nil
			}
			time.Sleep(100 * time.Millisecond)
		}
		if watchTerminalConsoleTitle() == name {
			old, _ := windows.UTF16PtrFromString(original)
			k32.NewProc("SetConsoleTitleW").Call(uintptr(unsafe.Pointer(old)))
		}
		return errors.New("Terminal tab did not adopt the title. Check its application-title override.")
	}
	return nil
}
