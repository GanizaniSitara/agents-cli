# Verification — 2026-10-02

The earlier implementation handoff already recorded live Claude/Codex/agy
discovery, Codex screen parity with console-peek.ps1, agy prompt/reply,
injection into cmd, and stop logging. This pass completed the remaining checks.

| Check | Evidence |
|---|---|
| Qwen, OpenCode, Copilot | Spawned separate hidden consoles. Each appeared once with the correct tool and answered 19 + 23 with 42 after `send --via inject`. |
| Codex2 | Separate live process reported `variant: codex2`, `.codex-second` home, and the Codex2 executable. Stopped at sandbox setup; no sandbox was installed. |
| Codex standalone queue | The installed executable rejects `--no-daemon queue`: queuing requires discovering the shared server. All current standalone sessions now report `can_native: false`. Explicit native send to the disposable Codex2 returned a JSON error without submission. |
| Injection and keys | Adopted cmd received `echo AGENTS_TRANSPORT_OK --no-enter`; screen showed unsubmitted text. A separate `key enter` executed it and the output appeared. |
| Tail and wait | `tail --lines 6` returned six rows. Qwen returned `idle` after 2s; OpenCode returned `match` on the answer 42; a nonmatching expression returned `timeout`. |
| Stop safety | Wrong confirmation refused. Own-session stop refused. All five disposable consoles stopped successfully through the CLI and every PID appeared in `stops --since 1h`. Subsequent discovery contained none of them. |
| Automated checks | 13 Go tests pass; `go vet ./...` passes. Toolchain and dependency downloads disabled. |

Regression coverage includes bounded tail output and invalid limits, duplicate
exact labels, reused launcher PIDs, exact no-daemon flag detection, and literal
message arguments after `--`. Existing tests cover classification, console-root
folding, reference resolution, argument parsing and state heuristics.

The native queue's successful daemon-connected path is not live-verified: all
current Codex TUIs use `--no-daemon`. This is the fallback outcome explicitly
provided by the handoff, not a claim that a standalone TUI can receive queues.
Background Claude and some OpenCode screen states stay honestly `unknown`.

The existing `rad_agents` skill and Claude pointer were verified; the missing
Codex global AGENTS pointer was added. Local disposable-screen evidence is in
`.verification/live-results.json` (excluded from git). Test driver:
`C:\scripts\_oneshot_agents_cli_verify.py` (attributed and indexed).

Unexpected side effect: Qwen's startup reported a successful automatic update
despite `--safe-mode`. No update command was requested. Its installed source
shows `general.enableAutoUpdate` defaults to true. Further Qwen launches are
disabled in the test helper until that setting is explicitly disabled.
Copilot's desktop-app installation offer was declined.

Ticket remains in progress for the user's review, as requested in its handoff.
