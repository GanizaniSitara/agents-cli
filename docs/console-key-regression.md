# Console key regression check

## Confirmed cause: painted SI, not injected Ctrl-O

Follow-up evidence on 24 October reproduced the glyph after real model turns
in both Claude 2.1.280 and 2.1.295. A six-minute scratch control receiving only
tail reads stayed clean. The initial local `/help` tests below did not exercise
stdin resume after a model turn.

The upstream report [anthropics/claude-code#88306](https://github.com/anthropics/claude-code/issues/88306)
identifies the charset reset emitted by `reassertTerminalModes()` on stdin
resume and after event-loop stalls. Legacy Win10 conhost consumes `ESC(B` but
paints the subsequent SI (`0x0F`) into the output buffer at the cursor.

Local proof, independent of any input injection:

- Suspend only the adopted scratch Claude process; attach a diagnostic helper.
- Write bytes `1b 28 42 0f` to **CONOUT$** through WriteFile, without writing to
  CONIN$. The screen changes from `abcXYZ` to `abcXYZ\u000f`.
- PeekConsoleInputW reports an empty input queue. Resume the scratch process.
- Write `1b 28 42 00` to CONOUT$ the same way: no additional glyph appears.
- A suspended Ctrl-U injection instead queued character `0x15`, as expected,
  not `0x0F`. Trying VK_PACKET or omitting key-up did not cure the glyph;
  those experimental source changes were reverted.

This corrects the original task's inference that a tail-visible SI must be real
composer input. Submitting or repainting can remove a painted cell too. A glyph
appearing after a key call is not proof that the call injected that character.
Overpainting at the cursor can also resemble missing input characters.

Both installed binaries contain exactly one `ESC(B SI` literal. The prepared
workaround preserves file length and changes its final byte to NUL:

| Installation | Version | SI byte offset (decimal) |
| --- | --- | --- |
| NVM | 2.1.295 | 107460427 |
| npm | 2.1.280 | 105699495 |

`C:\scripts\_oneshot_tech238_claude_charset_patch.py` defaults to a read-only
plan with original/patched hashes. Its explicit `--apply` mode verifies the
known original hashes, stages the one-byte patch and retains hash-named original
executables as backups. It never restarts sessions. Patching changes executable
signatures and future Claude updates can overwrite it. Applying it requires
the user's explicit approval to repair these Claude installations; authorization
to rebuild agents-cli alone does not authorize that software change.

The output-only NUL probe verifies conhost behavior, not successful execution of
a patched Claude binary. After approval, verify the patched binaries in fresh
scratch consoles through a model turn, delayed polling, Enter, Ctrl-U and
backspace before calling the workaround complete. Existing processes still map
their original binaries until restarted.

Evidence: ignored `.verification/tech238-{nvm-turn,npm-turn,read-control,live-debug}.json`.
Diagnostic helper: `C:\scripts\_oneshot_tech238_peek_input.py`; optional
`--output-reset`/`--output-nul` probes target only an adopted scratch session.
The helper can briefly suspend that scratch process and always resumes it in
`finally`. No test input reached the owner's existing sessions.

## Repeatable named-key check

Use a disposable Claude console only. Do not clear, submit, or otherwise edit
the owner's existing composers to investigate unexpected control characters.

1. Launch a scratch Claude console with the normal launcher and no task prompt.
   Check its screen and resolve its exact PID before sending input. If discovery
   cannot identify it, adopt only the new PID with `--tool claude` and a unique
   scratch label. Wait for an empty composer; dismiss startup dialogs first.
2. `agents-cli send <scratch-pid> abcXYZ --no-enter --force`, then
   `agents-cli key <scratch-pid> backspace`. Read the screen: the draft must be
   exactly `abcXY`, with no leading or trailing control characters.
3. `agents-cli key <scratch-pid> ctrl-u`. Read the screen: the composer must be
   empty. A `Ctrl+Y to paste deleted text` hint is normal.
4. Repeat steps 2–3 with immediate successive CLI calls to exercise separate
   helper attachments. Check every resulting draft, not just the final screen.
5. Type `/help` with `send --no-enter --force`, allow its autocomplete to settle,
   then send `key enter`. The local Help view must open. This proves Enter acts
   without sending a paid model prompt. Stop this scratch console by its exact
   resolved root PID when finished.

Run against both the installed CLI and a freshly built candidate. Record the
actual Claude versions: two installation paths can contain different versions.
Never assume a long-running process has the version currently on disk.

## Initial investigation on 24 October 2026 (superseded by the cause above)

The reported symptom is literal SI (`0x0F`) in idle Claude composers, sometimes
replacing leading draft text after named-key calls. It has **not reproduced**
in the isolated checks below; this is not a verified fix or a closed root cause.

- Installed CLI build metadata: Go 1.23.3, revision `82b022e`, modified tree.
  Current source candidate: `aebc898`. The console/key implementation is
  identical between those committed revisions; metadata cannot establish the
  exact uncommitted source of the installed executable.
- Launcher-created scratch console, Claude 2.1.280: `abcXYZ` → backspace →
  `abcXY` → Ctrl-U → empty; separate Enter opened `/help`. No SI observed.
- Separate hidden scratch console, Claude 2.1.295: same sequence passed, followed
  by 25 immediate send/backspace/Ctrl-U cycles with checks after each edit.
  Separate Enter opened `/help`. No SI observed.
- Read-only console diagnostics: the two affected consoles sampled and the
  launcher-created scratch console all used input mode `0x208`, with no queued
  input or console selection in six samples each. Matching mode alone does not
  reproduce the affected process's internal state.
- The Go suite passed 95 tests; `go vet ./...` and candidate build passed.
- The freshly built source candidate also passed 25 edit cycles and separate
  `/help` Enter against Claude 2.1.295. A third run passed another 25 cycles,
  then exercised both launcher injector functions with `/color` and `/rename`
  (including the launcher's two extra confirmation Enters). The final composer
  was empty with no SI and `/help` still opened through `key enter`.

`doKey` and `injectInput` both send Enter through the **same**
`keyPair(0x0D, '\r', 0)` encoding, including key-down/key-up. Text uses
VK_PACKET (`0xE7`), but the trailing Enter does not. Thus “key uses raw VK,
whereas send's Enter uses VK_PACKET” is not supported by this source.
Ctrl-U uses VK_U, character `0x15`, and LEFT_CTRL_PRESSED; backspace uses VK_BACK
and `0x08`. The INPUT_RECORD layout is 20 bytes with UnicodeChar at offset 14.

The launcher's `Send-ConsoleInput` and `Send-ConsoleInputBatch` are independent
C# injectors, not calls to `agents-cli key`. Their records also have size 20
and UnicodeChar at offset 14. They use VK=0 for text and VK_RETURN with scan
code `0x1C` for appended CR. They contain no explicit SI injection. Their exact
functions were extracted from the launcher's AST and exercised in the third
isolated run; the full busy-at-startup launcher timing remains unverified.

Local repeat driver: `C:\scripts\_oneshot_tech238_console_keys.py`. It creates,
adopts, and finally stops only its own new console. It accepts `--cli`, `--exe`,
and `--out`; `--launcher-check` calls the guarded helper
`C:\scripts\_oneshot_tech238_launcher_check.ps1`. Screen evidence is under the
ignored `.verification` directory.
Use the already-provisioned Python interpreter to run it. No dependencies or
package updates are required.

Remaining prerequisite: reproduce the failure with an exact triggering action
or identify a relevant state difference in the affected long-running process.
Do not change the transport speculatively or label these passing baseline
checks as proof that the original corruption is fixed.
