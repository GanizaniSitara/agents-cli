// agents-cli — ListAgents / SendMessage verbs for every agent TUI on this box.
//
// Claude Code's own mesh (ListAgents, SendMessage) only sees other Claude
// sessions. Codex, agy, Qwen, OpenCode, Copilot and Vibe windows cannot join it, so
// this tool wraps them instead: it discovers the console windows that host an
// agent, reads their screens, and writes keystrokes into their input buffers.
//
// House rules, shared with tasks-cli / oneshot-cli / mailkeep: a local binary,
// JSON on stdout and nothing else, errors as JSON with a non-zero exit, no
// daemon, no port, no MCP.
//
// Console access runs in a hidden child copy of this binary (the `_attach`
// verb). A process can only be attached to one console at a time, and the
// caller's own stdout may *be* a console, so the attach/peek/inject work
// happens in a detached helper whose stdout is a pipe back to the parent.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime"
)

// Stamped by scripts/install.ps1 via -ldflags, so `agents-cli version` can say
// which commit a given host is running.
var (
	buildCommit = "unknown"
	buildTime   = "unknown"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		emitError(err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" || args[0] == "help" {
		printHelp()
		return nil
	}
	if len(args) > 1 && (args[1] == "--help" || args[1] == "-h") {
		printHelp()
		return nil
	}
	switch args[0] {
	case "version", "--version":
		binary, err := os.Executable()
		if err != nil {
			binary = "unknown"
		}
		emit(map[string]interface{}{
			"commit": buildCommit, "built": buildTime, "binary": binary,
			"go": runtime.Version(), "platform": runtime.GOOS + "/" + runtime.GOARCH,
		})
		return nil
	case "_attach":
		return helperMain(args[1:])
	case "ls":
		return cmdLs(args[1:])
	case "mute":
		return cmdMute(args[1:], true)
	case "unmute":
		return cmdMute(args[1:], false)
	case "tail":
		return cmdTail(args[1:])
	case "send":
		return cmdSend(args[1:])
	case "key":
		return cmdKey(args[1:])
	case "wait":
		return cmdWait(args[1:])
	case "name":
		return cmdName(args[1:])
	case "adopt":
		return cmdAdopt(args[1:])
	case "stop":
		return cmdStop(args[1:])
	case "stops":
		return cmdStops(args[1:])
	}
	return fmt.Errorf("unknown command %q (try `agents-cli help`)", args[0])
}

func emit(value interface{}) {
	encoded, _ := json.MarshalIndent(value, "", "  ")
	fmt.Println(string(encoded))
}

func emitError(err error) {
	emit(map[string]interface{}{"error": map[string]string{"code": "agents_command_failed", "message": err.Error()}})
}

func printHelp() {
	emit(map[string]string{"help": `agents-cli — list, read and drive agent TUIs (Claude, Codex, agy, Qwen, OpenCode, Copilot, Vibe)

  ls    [--tool T] [--all] [--fast]        sessions: launch-info + process scan + adopted
  mute|unmute <ref>                       persistently disable/enable screen polling
  tail  <ref> [--lines N] [--above N]       visible screen of a session (read-only)
  send  <ref> --stdin | "text" [--no-enter] [--via auto|inject|native] [--force]
        Native queue additionally requires --allow-argv (exposes message to process listings).
  key   <ref> enter|escape|ctrl-c|ctrl-u|tab|up|down|backspace
  wait  <ref> [--idle-for 10s] [--timeout 10m] [--match REGEX]
  name  <ref> "label"                       label a session (persists by pid + start time)
  adopt <pid> --tool T [--name L]           register a session the scan cannot classify
  stop  <ref> --confirm <pid>               kill the session's process tree (logged)
  stops [--since 7d] [--scrub]              read log, or remove legacy private fields/old records
  version

<ref> is a pid (root cmd or agent process), a launch-info task slug, a label from
'name', or a unique substring of the name/title. Output is JSON. Claude-to-Claude
messaging should keep using the native SendMessage; 'send' refuses that pairing
unless --force.
`})
}
