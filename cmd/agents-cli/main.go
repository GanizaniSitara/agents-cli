// agents-cli — ListAgents / SendMessage verbs for every agent TUI on this box.
//
// Claude Code's own mesh (ListAgents, SendMessage) only sees other Claude
// sessions. Codex, agy, Qwen, OpenCode, Copilot and Vibe windows cannot join it, so
// this tool wraps them instead: it discovers the console windows that host an
// agent, reads their screens, and writes keystrokes into their input buffers.
//
// House rules, shared with tasks-cli / oneshot-cli / mailkeep: a local binary,
// JSON data on stdout, plain-text help, errors as JSON with a non-zero exit, no
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
	if len(os.Args) == 3 && os.Args[1] == "_switch-clicked-desktop" {
		if err := switchClickedDesktop(os.Args[2]); err != nil {
			emitError(err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "master" {
		if err := masterClient(os.Args[2:]); err != nil {
			emitError(err)
			os.Exit(1)
		}
		return
	}
	// An owner-opened editor waits for its owner, not the console-operation
	// deadline. Its parent cancels it when the watcher exits; apply is bounded.
	if len(os.Args) > 2 && ((os.Args[1] == "_attach" && os.Args[2] == "name-review") || (os.Args[1] == "shelf" && (os.Args[2] == "ui" || os.Args[2] == "review"))) {
		if os.Args[1] == "shelf" {
			if err := cmdShelf(os.Args[2:]); err != nil {
				emitError(err)
				os.Exit(1)
			}
			return
		}
		if err := helperMain(os.Args[2:]); err != nil {
			emitError(err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && (os.Args[1] == "watch" || os.Args[1] == "_watch-worker") {
		if err := runWatch(os.Args[1:]); err != nil {
			emitError(err)
			os.Exit(1)
		}
		return
	}
	if err := runWithDeadline(os.Args[1:], run); err != nil {
		emitError(err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 2 && args[0] == "help" && args[1] == "--open" {
		return openWatchHelp()
	}
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
	case "shelf":
		return cmdShelf(args[1:])
	case "park", "unpark", "shelve":
		return cmdSessionAction(args[0], args[1:])
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
	case "sends":
		return cmdSends(args[1:])
	}
	return fmt.Errorf("unknown command %q (try `agents-cli help`)", args[0])
}

func emit(value interface{}) {
	encoded, _ := json.MarshalIndent(value, "", "  ")
	fmt.Println(string(encoded))
}

func emitError(err error) {
	out := map[string]interface{}{"error": map[string]string{"code": "agents_command_failed", "message": err.Error()}}
	if audit := currentSendAudit(); audit != nil {
		audit.addReceipt(out)
	}
	emit(out)
}

func printHelp() {
	fmt.Print(`agents-cli - watch, list, read and drive agent sessions

WATCH
  agents-cli watch start                 run the panel in the background
  agents-cli watch run                   run the panel in the foreground
  agents-cli watch list                  watcher status and sessions (JSON)
  agents-cli watch stop                  exit the watcher; leave agents running
  agents-cli shelf ui                    saved sessions; resume or cancel pending shelving
  agents-cli help --open                 open the offline quick guide

EXAMPLES
  agents-cli ls
  agents-cli tail 1234 --lines 20
  agents-cli send 1234 "Check the latest test result"
  agents-cli key 1234 enter

COMMANDS

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
  sends [--since 1d] [--limit 50]          read send/key audit (metadata only)
  shelf list|ui                           saved sessions (JSON) / Shelf window
  shelf resume|cancel ID                  resume a saved session / cancel pending shelving
  park|unpark self|<ref>                  set parking without closing the session
  shelve self|<ref>                       open confirmation; save and close when idle
  watch start|run [--monitor N] [--stable 2s]
  watch monitors|desktops                  available screens and virtual desktops
  version

ls, tail, send and key accept --timeout D for a hard overall deadline, including
discovery and stdin (defaults: ls 60s, others 30s). Console helpers are capped at
15s. wait's --timeout includes discovery; its watchdog allows 1s for the receipt.
After a send timeout, inspect tail before retrying: input may already be delivered.

<ref> is a pid (root cmd or agent process), a launch-info task slug, a label from
'name', or a unique substring of the name/title. Command data is JSON; help is text.
Supported agents: Claude, Codex, agy, Qwen, OpenCode, Copilot and Vibe. Claude-to-Claude
messaging should keep using the native SendMessage; 'send' refuses that pairing
unless --force.
`)
}
