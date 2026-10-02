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
agents-cli send  <ref> "text" [--no-enter] [--via auto|inject|native] [--force]
agents-cli key   <ref> enter|escape|ctrl-c|ctrl-u|tab|up|down|backspace
agents-cli wait  <ref> [--idle-for 10s] [--timeout 10m] [--match REGEX]
agents-cli name  <ref> "label"
agents-cli adopt <pid> --tool T [--name L]
agents-cli stop  <ref> --confirm <pid>
agents-cli stops [--since 7d]
agents-cli version
```

`<ref>` is a pid (the window's root cmd.exe or the agent process inside it), a
launcher task slug, a label given with `name`, or a unique substring of the
name or window title. An ambiguous ref errors and lists the candidates.

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
  The text goes in one burst and the Enter in a second write 150 ms later:
  TUIs treat a fast burst as a paste and turn a trailing CR into a newline
  instead of a submit.
- **key** writes a single named virtual key (never a burst), so editing keys
  like `ctrl-u` are interpreted, not pasted.

`send --via auto` uses Codex's queue only for an identified running executable
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

`tail --lines N` returns the last N rows after trimming trailing blank rows;
without it, the full visible screen is retained. Put `--` before literal message
text that begins with flags. Duplicate exact labels are ambiguous, and launcher
metadata with a mismatched process start time is ignored to protect against PID
reuse.

`wait` reports screen stability, not proof that a model completed. `--match`
searches the whole visible screen including echoed prompts; choose an answer
that is absent from the prompt. State is a heuristic: background Claude and
some OpenCode screens legitimately remain `unknown`.

## Safety

- `send` refuses when the target is a Claude session **and** the caller is a
  Claude session, unless `--force`. Claude-to-Claude should use the native
  `SendMessage`; two channels into one TUI interleave text.
- `stop` resolves to exactly one pid, prints what it would kill, and does nothing
  without `--confirm <that pid>`. It refuses the caller's own tree. It kills by
  pid only (root plus descendants), never by image name.
- Every stop appends one JSON line to `%LOCALAPPDATA%\agents-cli\stops.jsonl`
  (`ts, pid, tool, name, title, source, killed, failed, caller_pid,
  caller_cmdline, result`). `agents-cli stops` reads it back. The writer sits
  behind one function so a later log sink is a one-line swap.

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
