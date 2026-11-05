# TECH-262: process walks and command deadlines

The 5 November incident affected `tail`, `send`, `ls` and `ls --fast`.
Because `--fast` does not attach to consoles, console access alone cannot
explain every affected call.

The shared process traversal contained three infinite loops when a snapshot
contained a multi-process parent cycle: `rootOf`, `ancestorNamed`, and
`callerClaudeAncestor`. Windows retains the PID of a departed parent, and
that PID can be reused. The synthetic two-process cycle regression failed
in all three walks before the fix. Each now has a finite traversal bound;
the depth walk is bounded by the snapshot size as well. Descendant traversal
already tracked visited PIDs.

This proves a matching CPU-spin defect, not the precise historical process
chain: the nine original hung processes had been killed before this work.
A live diagnostic snapshot during this investigation had 731 processes and
no parent cycle. No historical stack was available.

Whole-process watchdogs cover discovery, stdin, console calls and verification:

| Command | Default overall deadline |
| --- | --- |
| `ls` (including `--fast`) | 60 seconds |
| `tail`, `send`, `key` | 30 seconds |
| `_attach` console helper | 15 seconds |
| `wait` | requested `--timeout` (default 10 minutes), plus 1 second to emit its receipt |
| Other commands | 30 seconds |

`ls`, `tail`, `send` and `key` accept `--timeout D`. Timeouts must be positive.
An expired watchdog exits nonzero with JSON code `agents_command_timeout`.
After a send timeout, inspect the target before retrying: delivery may already
have occurred. Helpers inherit the remaining command budget and bound pipe
draining with `exec.Cmd.WaitDelay`. A helper also has its own watchdog, so
it cannot spin indefinitely after its parent exits.

The companion change in `C:/git/ringmaster` bounds each patrol send at
35 seconds, bounds pipe draining, and logs failures while continuing to the
next timer tick. It does not restart or change the supervisor's agent session.

Verification includes:

- Cycle regression failing before the fix and passing afterwards.
- Blocked `send --stdin` failing before the fix and exiting within its
  100 ms configured budget afterwards (plus process startup).
- Subprocess tests terminating CPU spins in `ls`, `tail`, `send`, `key` and `wait`.
- An independently launched `_attach` with blocked stdin exiting at 15 seconds.
- A hung console helper obeying its parent's shorter budget.
- Ringmaster's hung-send regression failing before the fix; the real timer
  loop reaching a second send after the first times out and recording failure.
- Full Go suites, `go vet`, and live read-only scans/tails. A staged full scan
  read 38 sessions in about 1.2 seconds.

TECH-262 remains in progress for owner review. No public push is part of this work.
