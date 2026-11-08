# agents-cli

ListAgents / SendMessage verbs for every agent TUI on a Windows workstation:
Claude Code, Codex, agy (Gemini), Qwen, OpenCode, Copilot.

Claude Code's own mesh (`ListAgents`, `SendMessage`) only sees other Claude
sessions. Nothing else can join it, so this wraps the console instead: it finds
the windows that host an agent, reads their screens, and types into their input
buffers. Same contract as tasks-cli / oneshot-cli / mailkeep: a local Go binary,
JSON on stdout and nothing else, errors as JSON with a non-zero exit, no daemon,
no port, no MCP.

```
agents-cli ls    [--tool codex|agy|qwen|claude|opencode|copilot] [--all] [--fast]
agents-cli tail  <ref> [--lines N] [--above N]
agents-cli send  <ref> --stdin | "text" [--no-enter] [--via auto|inject|native] [--force]
agents-cli key   <ref> enter|escape|ctrl-c|ctrl-u|tab|up|down|backspace
agents-cli wait  <ref> [--idle-for 10s] [--timeout 10m] [--match REGEX]
agents-cli name  <ref> "label"
agents-cli adopt <pid> --tool T [--name L]
agents-cli stop  <ref> --confirm <pid>
agents-cli stops [--since 7d] [--scrub]
agents-cli version
```

## Stopped-window watcher

Copy the same executable to any Windows PC; the watcher needs no installer,
admin rights, scripts, task files, ringmaster, or provider credentials.

```
agents-cli watch start
agents-cli watch list
agents-cli watch stop
```

`start` detaches a low-priority watcher with a small nonactivating panel and a
tray icon. `run` keeps it attached to the command lifetime for debugging.
`--monitor N` chooses a physical monitor on launch; `watch monitors` lists them.
Settings in the panel remembers the monitor and individually enabled virtual
desktops. All desktops are monitored by default. Each session shows its desktop;
excluded desktops neither appear nor flash. Monitor and desktop choices are
separate. A disconnected monitor falls back to the primary screen.

Hide puts the panel in the tray. The tray tooltip shows the stopped count;
click it to reopen without taking focus. Stopped and unknown sessions on the
current desktop have individual rows with their desktop number; other desktops
have stopped, working and unknown counts. Click a desktop's summary to expand or
collapse its sessions inside the panel, without switching desktop or activating
a window. Expanded rows use the same style and desktop labels, with needs input
and stops ahead of working sessions. Click a session row to go to that window
on its desktop. Current-desktop work has a summary count.
Fixed groups are confirmed needs input, error, finished, working, then
idle/check or unavailable. Ambiguous idle can never outrank a confirmed input wait.
Rows keep their place within a group; a session moves only when its group changes,
joining the end of its new group. New sessions append within their group.
Click a session to bring its window forward, or × to dismiss its row until the
next working-to-stopped cycle. The panel grows to fit the stopped rows within the
chosen monitor's work area; scroll when they exceed the available height.
After Hide, a new or changed stopped/unknown status on the current desktop can
reopen the panel without activation. Age ticks, background-desktop changes and
working counts do not reopen it.
Only an explicit session-row click can activate another window.
Row clicks select the last fully painted row on mouse press. The panel holds
its position and painted rows until release, so a new input wait or a height
change cannot redirect the click. Closed/reused processes and changed window
owners/session targets are rejected instead of guessing. Caption differences are
logged, rather than assuming a title change means a different window; animated
title prefixes are ignored when comparing captions.

`%LOCALAPPDATA%\agents-cli\watch\clicks.jsonl` records each row action or rejection:
press/release position, painted label, PID/HWND, native title, process identity,
activation result and foreground immediately after the request. It keeps one
previous file; each file is limited to 256 KiB. These private diagnostics may
contain session names and window titles. An activation request is not proof the
desktop switch has completed; the live activation check remains an owner check.

Confirmed input waits have a filled strong amber band with dark text and a
bold NEEDS INPUT label. Errors are red, explicit completion is green, working
is plain neutral, and idle/check or unavailable states are muted grey.
Age appears only as a number;
no row or label fades. The earlier trial-colour/idle switches were removed so
their meanings cannot conflict. The existing theme.json palette remains editable;
legacy fade settings are ignored. Settings reloads the palette (restart for a
changed background).

An empty composer alone cannot reliably distinguish a completed answer from a
natural-language question, so ambiguous idle is labelled idle/check in grey.
Only known input/approval prompts receive the amber highlight.
An unchanged screen alone is not evidence of a hung model. Reader failure is
shown in red, and unknown screens have their own rows as well as a header count.

Each row shows sub-agent counts as `agents: 2/3 running`. Claude and Codex
counts use local child-session lifecycle records when the owning session can
be verified. Completed children remain in the total but not the running count.
Missing identities or ambiguous lifecycle records show unknown, not zero.
Only lifecycle metadata is read; transcript text is not saved in watcher state.
A positive running count keeps the session working even when its composer is
idle. Screen counts remain a fallback: Claude footer totals do not imply all
agents are running, and Codex background terminals are not sub-agents. Copilot
explicit agent summaries can be read when visible. The watcher never opens
these views or prompts a model. agy/Vibe counts are not currently exposed.

The executable and tray use the embedded terminal-and-agents icon in
`cmd/agents-cli/agents.ico`; the checked-in Windows resource object lets normal
Go builds include it without additional build tools.

The watcher reuses the CLI's state classifier. Two matching stopped samples,
two seconds apart, suppress brief pauses and normally notify within about four
seconds. `--stable D` changes that threshold. Startup-idle sessions are listed
without flashing. Working-to-idle/prompt transitions flash only that window's
taskbar button when its desktop is being viewed, until it is foreground.
Off-desktop stops remain in the panel information without taskbar flashing;
switching to their desktop starts their pending flashes. Switching away clears
their flashes, including flags left by older watcher builds. Unknown desktop
identity never starts a flash. Stops append one JSON line with name,
time and wait reason. Unknown/unreadable screens never count as completion;
`watch list` includes grouped `sessions` rows and coverage counts as well as the
separate `stopped` list, so unsupported states remain visible by name and desktop.

One persistent detached reader handles the consoles; it does not repeatedly
spawn `ls`. Console accessibility events invalidate idle-screen snapshots,
busy windows are sampled every two seconds, and a ten-second reread covers
missed events. Discovery refreshes every fifteen seconds. A four-second worker
watchdog bounds Windows calls; a six-second parent heartbeat restarts a failed
reader with backoff. Muted sessions are excluded before reading. Windows
Terminal message-only ConPTY handles cannot be flashed and are skipped; use
classic console windows for taskbar notification. Copilot uses the existing
footer/composer detector, including menus and permission waits.

Local status, settings and event log live in
`%LOCALAPPDATA%\agents-cli\watch`; `--state-dir DIR` overrides it. No screen text
is saved. Session names can contain private information, so keep these files
local. No launch-on-login entry is installed; run `watch start` after sign-in.

`agents-cli mute <ref>` persistently excludes that session from screen polling.
`ls` keeps its metadata row with `polling_muted: true` and `state: "muted"`,
without reading its console; `tail` and `wait` refuse until `unmute <ref>`.
An already-running `wait` checks for a mute before each further read. The mute
is bound to PID plus process start time, so it does not affect a reused PID.
Muting does not pause the agent or send it a message. Explicit `send`/`key`
commands remain available. Use a numeric PID or registered name to mute;
discovery does not read live titles to resolve references.
For generic console sends (including Claude), an explicit `send` with Enter
performs bounded screen checks to verify delivery even when polling is muted.
It does not change the mute setting or emit the screen contents. Copilot's
separate transport retains its no-screen-check behavior while muted.

`ls` and `tail` are passive local console reads: they do not prompt the target
model. Model tokens are spent only if a coordinator interprets their output.
`ls --fast` skips all screen reads when only process metadata is needed.

`<ref>` is a pid (the window's root cmd.exe or the agent process inside it), a
launcher task slug, a label given with `name`, or a unique substring of the
name or window title. An ambiguous ref errors and lists the candidates.

## Give your LLM access

Install the Windows executable into a directory on PATH, such as `C:\Tools`,
then check `agents-cli version` from the agent's own shell. The agent needs
permission to execute local commands and access the target Windows console.
The existing agent sessions keep their own provider sign-ins; agents-cli needs
no model API key or MCP server registration.

Copy [the agent instructions](docs/agent-instructions.md) into the instruction
file your CLI loads. Merge them with existing instructions rather than
overwriting your rules. For use across projects, the usual user-level locations
are:

| Agent | User-level instruction file |
|---|---|
| [Claude Code](https://code.claude.com/docs/en/memory) | `~/.claude/CLAUDE.md` |
| [Codex](https://developers.openai.com/codex/guides/agents-md) | `~/.codex/AGENTS.md` (or `$CODEX_HOME/AGENTS.md`) |
| [GitHub Copilot CLI](https://docs.github.com/en/copilot/how-tos/copilot-cli/customize-copilot/add-custom-instructions) | `~/.copilot/copilot-instructions.md` |
| [OpenCode](https://docs.opencode.ai/docs/rules/) | `~/.config/opencode/AGENTS.md` |
| [Mistral Vibe](https://docs.mistral.ai/vibe/code/cli/agents) | `~/.vibe/AGENTS.md` (or `$VIBE_HOME/AGENTS.md`) |

Here `~` means the Windows user's home directory. Alternatively, put the
instructions in the CLI's project-level instruction file to limit their scope.
Restart the agent session after changing its global instructions. These files
teach the model how to invoke the CLI; command approvals still belong to the
agent's own permission system.

For a read-only setup check, ask the agent:

> Run `agents-cli version`, then `agents-cli ls --fast`, and summarize the
> sessions found. Do not send messages, launch agents or stop anything.

This verifies the executable and shell access without prompting other models.
Mempalace and other MCP tools are separate integrations; agents-cli works
without them.

## How sessions are found

`ls` unions three sources and folds them onto the console-owning root process:

1. **Process scan.** Every process classified as an agent (`claude.exe`,
   `codex.exe`, `agy.exe`, `node.exe` running `@qwen-code`, `@github\copilot`,
   `opencode-ai`, `@openai\codex` or `@google/gemini-cli`) is walked up through
   cmd/bash/powershell/node parents to the top-most shell. One row per root; if a
   root hosts several agents (a Claude session running `codex exec` in its Bash
   tool), the shallowest one is the window's own TUI. Claude's daemon and
   `--bg-pty-host` processes are infrastructure and skipped; sessions under a
   pty-host are reported with `kind: "bg"`.
2. **launch-info.** `%USERPROFILE%\tasks\in-progress\*.launch-info.json`, written
   by the task launcher, supplies task slug, zone, session and thread ids. Dead
   pids show with `alive: false` only under `--all`.
3. **Adopted.** `%APPDATA%\agents-cli\sessions.json`, written by `adopt` and
   `name`, keyed by pid with the process start time so a reused pid is ignored.

Without `--fast`, `ls` also peeks each live session for its window title and a
`state` guess: `busy` (spinner, "esc to interrupt"), `prompt` (a y/n or
permission question is on screen), `idle` (bare input prompt), else `unknown`.

## How the console is reached

A process can be attached to only one console at a time, and the caller's own
stdout may *be* a console. So every console operation runs in a hidden,
detached child copy of this binary (`agents-cli _attach ...`) which calls
`FreeConsole` + `AttachConsole(pid)`, does the work, and writes JSON back over a
pipe:

- **peek** opens `CONOUT$` (the *active* buffer, so alternate-screen TUIs are
  captured) and reads the visible window rows with `ReadConsoleOutputCharacterW`.
  Read-only.
- **inject** opens `CONIN$` and writes key events with `WriteConsoleInputW`.
  The text goes in one burst and Enter in a separate write after a length-scaled
  pause (500 ms to 4 seconds for nonempty text):
  TUIs treat a fast burst as a paste and turn a trailing CR into a newline
  instead of a submit.
  `send` then checks for submission and may retry Enter once, only when a fresh
  screen still shows the draft or paste pill. Claude's `[Pasted text ...]` and
  Codex's `[Pasted Content ...]` both count as unsubmitted input. A receipt with
  `submitted: false` means delivery could not be confirmed; inspect the target
  before retrying. `--no-enter` only types and never retries or verifies.
- **key** writes a single named virtual key (never a burst), so editing keys
  like `ctrl-u` are interpreted, not pasted.

`send --via auto --allow-argv` uses Codex's queue only for an identified running executable
and profile with a thread id, without `--no-daemon`. Codex explicitly rejects
queuing to a standalone `--no-daemon` TUI; those rows report `can_native: false`
and `native_unavailable`, and auto uses injection. Explicit native sends fail
before invoking Codex when unavailable. Queue failures never silently fall back
to injection, which could duplicate a message.

Native sends run the target's exact executable with its own `CODEX_HOME`,
including the default `.codex` home when the caller uses `.codex-second`.
`variant: codex2` distinguishes the second account. `--no-enter` always uses
injection in auto mode; explicit native plus `--no-enter` is rejected.
Queue calls are bounded to 20 seconds; a timeout does not prove non-delivery.
Without `--allow-argv`, auto uses console injection. Explicit native sends
require this opt-in because Codex queue accepts the message only as an argument.

`tail --lines N` returns the last N rows after trimming trailing blank rows;
without it, the full visible screen is retained. Put `--` before literal message
text that begins with flags. Duplicate exact labels are ambiguous, and launcher
metadata with a mismatched process start time is ignored to protect against PID
reuse.

`wait` reports screen stability, not proof that a model completed. `--match`
searches the whole visible screen including echoed prompts; choose an answer
that is absent from the prompt. State is a heuristic: background Claude and
some OpenCode screens legitimately remain `unknown`.

## Copilot CLI

Copilot is discovered from `copilot.exe` or the npm `@github/copilot` loader
(Windows or forward-slash paths). The loader and native child fold into one
session; either the root cmd PID or native Copilot PID resolves. Detection does
not depend on the window title: the live cmd.exe test window retained the title
`Windows PowerShell` after Copilot initialized its shell support.

Copilot's footer says `Working esc interrupt`, without the `to` used by other
agents. `ls` and `tail` inspect that footer and the bordered composer, ignoring
old transcript text. An empty composer is `idle`, a draft or selection dialog
is `prompt`, and the interrupt footer is `busy`. `wait` requires an idle
composer as well as a stable screen; a stationary busy indicator or model
picker does not count as idle.

`send` uses bracketed paste, including multiline/Unicode text, then waits for
the composer to settle before sending a separate Enter. This also prevents
slash-command completion from consuming text midway through a paste. The
initial delay scales with length (800 ms plus 100 ms per 200 characters, capped
at 4 seconds), followed by bounded screen checks. `submitted: true` means an
empty composer or a newly opened dialog was observed, not that the model has
finished. After Enter, `send` verifies the pane and, while the composer still
shows the draft at a live prompt with no dialog open, retries Enter with
backoff (up to 3 retries) instead of leaving the text unsubmitted. Busy and
menu screens are never retried: a second Enter could select a model or approve
a tool. If `submitted` is false or a console check fails, inspect `tail`
before retrying. `--no-enter` only pastes and returns `submitted: false`.
Muted sessions skip screen checks and cannot confirm submission. For unmuted
sessions, sending while busy or in a menu is refused; use `wait` or `key` first.

`key enter`, `escape`, `up` and `down` use ordinary Win32 key events. Enter uses
the same record shape as send's submission, and arrows navigate the model
picker. No Copilot-specific VT-key workaround is needed. `stop` retains the
usual exact root-PID `--confirm` requirement.

Authentication uses `copilot login` or interactive `/login` (OAuth, with
browser/device-code flows). Supported environment tokens take precedence in
the order `COPILOT_GITHUB_TOKEN`, `GH_TOKEN`, `GITHUB_TOKEN`; fine-grained PATs
need the Copilot Requests permission, and classic PATs are unsupported. See
[GitHub's authentication guide](https://docs.github.com/en/copilot/how-tos/copilot-cli/set-up-copilot-cli/authenticate-copilot-cli).
The tested CLI has no standalone subscription-status command. `/usage` shows
session usage; a successful trivial `copilot -p` prompt verifies usable model
access, not the billing plan or renewal status. Organization policy can also
block access; see [GitHub's auth troubleshooting guide](https://docs.github.com/en/copilot/how-tos/copilot-cli/set-up-copilot-cli/troubleshoot-copilot-cli-auth).
If login is needed, the user completes it; agents-cli does not handle credentials.

Live verification on 2026-10-16: `copilot --version` reported 1.0.83 and a trivial
prompt succeeded. The single cmd.exe session launched with `--no-auto-update`
displayed 1.0.74 from the npm-bundled native executable. Version output and the
interactive banner differed on this installation; compare both when reporting
issues. The repo build was tested against that session with short, long,
multiline and Unicode prompts, the four named keys, menu/busy/idle states,
`wait`, and confirmed stop. Fake-console tests run with `go test ./...`.

## Safety

- `send` refuses when the target is a Claude session **and** the caller is a
  Claude session, unless `--force`. Claude-to-Claude should use the native
  `SendMessage`; two channels into one TUI interleave text.
- `stop` resolves to exactly one pid, prints what it would kill, and does nothing
  without `--confirm <that pid>`. It refuses the caller's own tree. It kills by
  pid only (root plus descendants), never by image name.
- Stop auditing is local only. `%LOCALAPPDATA%\agents-cli\stops.jsonl` retains
  `ts, pid, tool, source, killed, failed, caller_pid, result`. It omits labels,
  titles and caller command lines, strips these fields from older entries and
  removes entries older than 30 days whenever a stop is logged. `agents-cli
  stops --scrub` applies that cleanup immediately. Updates use a process lock
  and a temporary file so concurrent stops preserve each other's records.

## Privacy

Use `agents-cli send <ref> --stdin` and supply the message through the caller's
stdin pipe (or redirect an existing protected file). Positional message text
remains supported, but appears in the original process command line and may be
captured by shell history or the calling agent. Do not construct a shell command
containing a credential, including an `echo` pipeline. For real credentials use
the workstation's secret-cli workflow instead of sending them to an agent.

Console helpers receive prompt text through stdin, not process arguments. Send
receipts contain delivery metadata, never the message or raw native CLI output.
Malformed helper output is not echoed in errors. Codex native queue requires
`--allow-argv`; its external CLI cannot currently accept the prompt privately.

`tail` and `wait` deliberately return the target screen, which may contain
sensitive material. `ls` returns session metadata. These outputs are not
automatically redacted: review where you send or retain them. A receiving AI
provider may store prompts and screen output under its own policies.

## Install

```
powershell -NoProfile -ExecutionPolicy Bypass -File scripts\install.ps1
```

Uses the installed Go toolchain and cached modules only (no downloads), runs
the tests, builds with the commit stamped in, and swaps the binary into
`C:\Tools\agents-cli.exe`. `agents-cli version` reports the commit.

Config is environment-only: `AGENTS_CLI_TASKS_DIR` overrides the launch-info
directory.

## Not in scope

Cross-machine sessions (that is `csf`, which talks to the Mac gateway), a TUI,
byte-exact PTY mirroring (peek is a screen snapshot; spinners are approximate),
OpenCode's ACP server and Qwen's shared board (both candidates for a later
`--via native`).
