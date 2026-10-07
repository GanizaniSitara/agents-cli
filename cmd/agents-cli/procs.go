package main

import (
	"path/filepath"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// proc is one row of the process snapshot. Cmd and Start are filled lazily and
// only for processes the classifier cares about: reading another process's
// PEB is cheap but not free, and the snapshot has hundreds of rows.
type proc struct {
	PID   uint32
	PPID  uint32
	Name  string // image name, lower-case
	Cmd   string // command line, lower-case; empty if unreadable
	Start time.Time
}

type procMap map[uint32]*proc

func snapshot() (procMap, error) {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(snap)
	m := procMap{}
	var pe windows.ProcessEntry32
	pe.Size = uint32(unsafe.Sizeof(pe))
	if err := windows.Process32First(snap, &pe); err != nil {
		return nil, err
	}
	for {
		m[pe.ProcessID] = &proc{
			PID:  pe.ProcessID,
			PPID: pe.ParentProcessID,
			Name: strings.ToLower(windows.UTF16ToString(pe.ExeFile[:])),
		}
		if err := windows.Process32Next(snap, &pe); err != nil {
			break
		}
	}
	return m, nil
}

// commandLine reads the target's command line out of its PEB. Fails (returns
// "") for protected or other-user processes; the classifier treats that as
// "no command line", not as an error.
func commandLine(pid uint32) string {
	h, params, ok := processParams(pid)
	if !ok {
		return ""
	}
	defer windows.CloseHandle(h)
	cl := params.CommandLine
	if cl.Length == 0 || cl.Buffer == nil {
		return ""
	}
	buf := make([]uint16, cl.Length/2)
	var n uintptr
	if err := windows.ReadProcessMemory(h, uintptr(unsafe.Pointer(cl.Buffer)), (*byte)(unsafe.Pointer(&buf[0])), uintptr(cl.Length), &n); err != nil {
		return ""
	}
	return windows.UTF16ToString(buf)
}

// envVar reads one variable from the target's environment block. Two Codex
// installs share this box and differ only by CODEX_HOME, so the environment is
// the one place the distinction is visible.
func envVar(pid uint32, name string) string {
	h, params, ok := processParams(pid)
	if !ok {
		return ""
	}
	defer windows.CloseHandle(h)
	if params.Environment == nil || params.EnvironmentSize == 0 || params.EnvironmentSize > 1<<22 {
		return ""
	}
	buf := make([]uint16, params.EnvironmentSize/2)
	var n uintptr
	if err := windows.ReadProcessMemory(h, uintptr(params.Environment), (*byte)(unsafe.Pointer(&buf[0])), params.EnvironmentSize, &n); err != nil {
		return ""
	}
	prefix := strings.ToUpper(name) + "="
	start := 0
	for i, u := range buf {
		if u != 0 {
			continue
		}
		entry := windows.UTF16ToString(buf[start:i])
		start = i + 1
		if entry == "" {
			break // double NUL terminates the block
		}
		if strings.HasPrefix(strings.ToUpper(entry), prefix) {
			return entry[len(prefix):]
		}
	}
	return ""
}

// processParams opens pid and reads its RTL_USER_PROCESS_PARAMETERS from the
// PEB. The caller closes the handle (pointers inside params are remote).
func processParams(pid uint32) (windows.Handle, *windows.RTL_USER_PROCESS_PARAMETERS, bool) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_INFORMATION|windows.PROCESS_VM_READ, false, pid)
	if err != nil {
		return 0, nil, false
	}
	var pbi windows.PROCESS_BASIC_INFORMATION
	var retLen uint32
	if err := windows.NtQueryInformationProcess(h, windows.ProcessBasicInformation, unsafe.Pointer(&pbi), uint32(unsafe.Sizeof(pbi)), &retLen); err != nil {
		windows.CloseHandle(h)
		return 0, nil, false
	}
	var peb windows.PEB
	var n uintptr
	if err := windows.ReadProcessMemory(h, uintptr(unsafe.Pointer(pbi.PebBaseAddress)), (*byte)(unsafe.Pointer(&peb)), unsafe.Sizeof(peb), &n); err != nil {
		windows.CloseHandle(h)
		return 0, nil, false
	}
	params := new(windows.RTL_USER_PROCESS_PARAMETERS)
	if err := windows.ReadProcessMemory(h, uintptr(unsafe.Pointer(peb.ProcessParameters)), (*byte)(unsafe.Pointer(params)), unsafe.Sizeof(*params), &n); err != nil {
		windows.CloseHandle(h)
		return 0, nil, false
	}
	return h, params, true
}

// exePath returns the first token of a command line (quoted or bare).
func exePath(cmd string) string {
	cmd = strings.TrimSpace(cmd)
	if strings.HasPrefix(cmd, `"`) {
		if end := strings.IndexByte(cmd[1:], '"'); end >= 0 {
			return cmd[1 : end+1]
		}
		return strings.Trim(cmd, `"`)
	}
	if sp := strings.IndexByte(cmd, ' '); sp >= 0 {
		return cmd[:sp]
	}
	return cmd
}

func startTime(pid uint32) time.Time {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return time.Time{}
	}
	defer windows.CloseHandle(h)
	var ct, et, kt, ut windows.Filetime
	if err := windows.GetProcessTimes(h, &ct, &et, &kt, &ut); err != nil {
		return time.Time{}
	}
	return time.Unix(0, ct.Nanoseconds()).UTC()
}

func isAlive(pid uint32) bool {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return false
	}
	return code == 259 // STILL_ACTIVE
}

// descendants returns every process below root, breadth-first.
func (m procMap) descendants(root uint32) []uint32 {
	children := map[uint32][]uint32{}
	for _, p := range m {
		children[p.PPID] = append(children[p.PPID], p.PID)
	}
	var out []uint32
	queue := []uint32{root}
	seen := map[uint32]bool{root: true}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, c := range children[cur] {
			if seen[c] {
				continue
			}
			seen[c] = true
			out = append(out, c)
			queue = append(queue, c)
		}
	}
	return out
}

// ---- classification -------------------------------------------------------

// Image names that can sit between an agent process and the cmd.exe that owns
// the window. The root walk climbs through these and stops at anything else
// (explorer, WindowsTerminal, services).
var chainNames = map[string]bool{
	"cmd.exe": true, "bash.exe": true, "sh.exe": true, "powershell.exe": true,
	"pwsh.exe": true, "node.exe": true, "claude.exe": true, "codex.exe": true,
	"agy.exe": true, "python.exe": true, "pythonw.exe": true, "conhost.exe": true,
}

// needsCmd lists image names whose classification depends on the command line.
var needsCmd = map[string]bool{
	"node.exe": true, "claude.exe": true, "codex.exe": true, "cmd.exe": true,
}

// codexHome names the Codex profile a session runs under. Two installs live on
// this box (the npm codex and "Codex2" with CODEX_HOME=.codex-second); the tool
// name alone cannot tell them apart. Returns the CODEX_HOME directory and a
// short variant label ("codex" or "codex2").
func codexHome(p *proc) (home, variant string) {
	home = envVar(p.PID, "CODEX_HOME")
	if home == "" {
		if profile := envVar(p.PID, "USERPROFILE"); profile != "" {
			home = filepath.Join(profile, ".codex")
		}
	}
	lower := strings.ToLower(home)
	switch {
	case strings.Contains(lower, ".codex-second"), strings.Contains(p.Cmd, `\codex2\`):
		return home, "codex2"
	case home == "":
		return "", "codex"
	}
	return home, "codex"
}

func hasCommandFlag(cmd, flag string) bool {
	for _, arg := range strings.Fields(cmd) {
		if strings.EqualFold(strings.Trim(arg, `"`), flag) {
			return true
		}
	}
	return false
}

// classify names the agent a process is, or "" if it is not one. The command
// line is lower-cased.
func classify(p *proc) string {
	switch {
	case p.Name == "claude.exe", strings.Contains(p.Cmd, `@anthropic-ai\claude-code`):
		// Daemon and pty-host processes are infrastructure, not sessions.
		if strings.Contains(p.Cmd, "daemon run") || strings.Contains(p.Cmd, "--bg-pty-host") {
			return ""
		}
		return "claude"
	case p.Name == "codex.exe", strings.HasPrefix(p.Name, "codex-"), strings.Contains(p.Cmd, `@openai\codex`):
		return "codex"
	case p.Name == "agy.exe":
		return "agy"
	case strings.Contains(p.Cmd, "@qwen-code"):
		return "qwen"
	case strings.Contains(p.Cmd, `@github\copilot`):
		return "copilot"
	case p.Name == "opencode.exe", strings.Contains(p.Cmd, "opencode-ai"):
		return "opencode"
	case strings.Contains(p.Cmd, "@google/gemini-cli"), strings.Contains(p.Cmd, `@google\gemini-cli`):
		return "gemini"
	case p.Name == "vibe.exe":
		// Mistral Vibe: a uv trampoline whose python children are the
		// interpreter, not separate sessions; the shallowest-wins fold keeps
		// the trampoline.
		return "vibe"
	}
	return ""
}

// fillCmd reads the command line for every process whose classification or
// chain position depends on it.
func (m procMap) fillCmd() {
	for _, p := range m {
		if needsCmd[p.Name] {
			p.Cmd = strings.ToLower(commandLine(p.PID))
		}
	}
}

// rootOf climbs from an agent process to the console-owning root. A Claude
// session spawned by the background daemon has a pty-host parent rather than a
// window; the walk stops there and reports kind "bg".
func (m procMap) rootOf(p *proc) (root *proc, kind string) {
	cur := p
	for {
		parent, ok := m[cur.PPID]
		if !ok || parent.PID == cur.PID {
			return cur, "window"
		}
		if strings.Contains(parent.Cmd, "--bg-pty-host") {
			return cur, "bg"
		}
		if !chainNames[parent.Name] {
			return cur, "window"
		}
		cur = parent
	}
}

// depth is the number of hops from p up to root.
func (m procMap) depth(p *proc, root uint32) int {
	d := 0
	for cur := p; cur != nil && cur.PID != root; d++ {
		cur = m[cur.PPID]
		if d > 64 {
			break
		}
	}
	return d
}

// ancestorNamed reports whether any ancestor of pid (inclusive) has the image
// name. Used to decide whether the caller is itself a Claude session.
func (m procMap) ancestorNamed(pid uint32, name string) bool {
	for cur, ok := m[pid]; ok; cur, ok = m[cur.PPID] {
		if cur.Name == name {
			return true
		}
		if cur.PPID == cur.PID || cur.PPID == 0 {
			break
		}
	}
	return false
}

// selfRoot finds the console root of the running agents-cli process.
func (m procMap) selfRoot() uint32 {
	self, ok := m[uint32(windows.GetCurrentProcessId())]
	if !ok {
		return 0
	}
	root, _ := m.rootOf(self)
	return root.PID
}
