# Verification — agy input latency, 2026-10-16

Reproduced with Antigravity CLI 1.4.0 in new hidden consoles. The existing
busy agy session received no test input. Its read-only samples showed input
mode `0x288`, no console selection, and zero queued records in six 200 ms
samples (repeated later with the same result).

| Input path | Observation |
|---|---|
| Installed Go helper, 2,000 / 10,000 characters | Disposable raw reader received all records. Helper completed in 163 / 172 ms including its fixed 150 ms Enter pause; bulk VK=0, not per-character VK_PACKET. |
| Bulk VK=0 into fresh agy, about 2,000 characters | Write completed in 2.3 ms; final marker displayed at 7.8 s. Console input queue was already empty while text continued appearing. |
| Bulk VK_PACKET into fresh agy | Write completed in 2.7 ms; draft continued changing for roughly 9 s. |
| Per-character VK_PACKET into fresh agy | Writer completed in 45 ms; final marker appeared at 8.6 s. |
| Existing Go bracketed-paste helper into fresh agy | Complete 2,001-character paste visible by 273 ms; exact 10,000-character paste visible by 288 ms. These are sampled upper bounds including helper launch. |
| Paced character events, 60 characters at 20 ms intervals | Writer took 1.22 s; final marker visible by 1.57 s. This models paced input; it does not measure actual physical keyboard or clipboard input. |
| Enter settling | A bracketed `/help` draft, after dismissing its local autocomplete menu, settled by 645 ms. One separate Enter opened the local help screen without submitting a model prompt. |
| Fake-console tests | Growing drafts delay Enter; busy windows, existing drafts, trust dialogs, state changes and errors prevent inappropriate sends. Uncertain submission never retries Enter. Muted sends do not poll or claim verified submission. |
| Public CLI integration | Opt-in test creates and identity-checks its own agy console, then sends 2,000 and 10,000 characters with `--stdin --no-enter`, verifies paste labels and receipt, and kills only its own process. |

The delay is after console dequeue, in agy's ordinary-character processing /
redraw path. An empty OS queue or a successful WriteConsoleInput call does not
mean its composer is ready. Neither changing VK to VK_PACKET nor splitting
the write into individual characters fixes this reproduction. The raw reader
also tested a 500 ms reader stall: writes still completed promptly and Enter
could already be queued when the reader resumed, so a fixed sleep is not a
consumer-completion acknowledgement.

Live Handy metadata also links dictation to the affected window: at 11:28:26
it targeted `AGY` with Direct input for 339 characters, and reported all 678
events accepted with no Win32 error. The current setting is Direct. This is
evidence that dictation used the susceptible input path, not proof that every
manual-input complaint shares that cause. Real physical typing and clipboard
pasting into the existing session were not tested. No Handy settings changed.

At investigation start, installed agents-cli was Go 1.23.3, stamped `0d0d997`
and 2026-10-16, with `vcs.modified=true`. Its helper still expected text in
argv, wrote VK=0 in bulk and waited 150 ms. Committed Go HEAD was `41d0500`;
the concurrent Claude fix was subsequently committed as `2414bb7` and is the
base of this isolated worktree. Build metadata alone cannot reconstruct the
installed binary's uncommitted source changes. The memory describing a Rust
installation did not describe the installed binary observed here.

The fix routes agy through the existing stdin-only bracketed-paste primitive,
waits for a stable framed composer before Enter, and verifies submission
without automatic Enter retries. Other tools retain their existing routes.
Checks: 67 regular tests passed (live test skipped by default), opt-in live
draft test passed, `go vet ./...`, build, and `git diff --check` passed.

To repeat the isolated integration test after building the local candidate:

```text
go build -o .verification/agy-candidate.exe ./cmd/agents-cli
go test ./cmd/agents-cli -run TestAGYIsolatedDraft -v -args -agy-integration
```

Detailed local evidence is excluded from git under `.verification/agy-*.jsonl`.
Diagnostic driver: `C:\scripts\_oneshot_agy_input_latency.py`. No installation,
deployment, push, ticket completion, or existing-session injection performed.

# Older verification — 2026-10-02

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
