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

## Current disposition: unmodified Claude, classic cmd.exe, filtered tail

The owner withdrew approval for binary patching and explicitly requires classic
cmd.exe. Both installations were briefly patched, then restored from verified
original backups before the later scratch tests. The patch helpers were removed.
Do not binary-patch Claude, recommend Windows Terminal, or reapply a patch after
an update. Backups remain beside the executables with suffix
`.si-byte-original-<original SHA256>`.

| Installation | Version | Restored original SHA256 |
| --- | --- | --- |
| NVM | 2.1.295 | `a30012741c3d0b55e2a6c252dab3781134b75be95896ae89d40a93e10f83bcd6` |
| npm | 2.1.280 | `0e4195524b73eb77efbdf3e2b36de5322a29f0ca575dfd2d9b4f946b1d425469` |

The interim agents-cli mitigation removes SI cells when reading console lines,
before returning `tail` output or classifying state. It preserves surrounding
draft text and leaves other controls alone. It does not edit console input or
fix the glyph displayed by conhost. A live check found one SI in the original
raw reader, zero in installed `tail`, and correctly classified the empty
composer as `idle`. The Go suite has 96 passing tests; vet and build pass.

## Plain cmd.exe settings and release investigation

The standard `launch.ps1 new ... -Tool claude` created classic console PID23232
with Claude child37308, input mode `0x208`, output mode `0x7`, CP65001 and Consolas.
Source inspection found no `SetConsoleMode`, `chcp`, ConsoleCP or encoding setup
in launch.ps1/lib-window-tiling.ps1; the font-profile calls are Codex-specific.
Claude/conhost determine the observed runtime modes.

Output-only probes in that scratch console tested modes 1,3,5,7,15,23,
CP65001/437/850/1252, and Consolas/Lucida Console/Courier New. Every case retained
an unwanted third cell in `A ESC(B SI B`; CP437/850 converted it to U+263C (sun),
which is not a fix. Disabling VT output also exposed the escape sequence and
would break the TUI. Original settings were restored after each experiment.

A subsequent short-lived hidden classic-console probe (no Claude session or
visible window) tested output modes 0,1,2,3,4,5,6,7,8,12,14,15,23 and input modes
0,8,512,520 with output mode7 and CP65001. None produced the correct two-cell
`AB`; toggling ENABLE_VIRTUAL_TERMINAL_INPUT did not remove SI. This covers
processed-output, wrapping, VT processing and newline-mode combinations.

No verified cmd.exe setting fix was found. **No launch.ps1 change is proposed
or applied**, because recommending an unproven flag/code-page/font adjustment
would misrepresent these results. A Terminal comparison was explored, rejected
by the owner as unsuitable, and stopped; all test Terminal windows were closed.

The live GitHub API reports issue88306 closed `not_planned` on 2026-10-11, with
the sole comment closing it for inactivity. The latest release checked was
[v2.1.310](https://github.com/anthropics/claude-code/releases/tag/v2.1.310),
published 2026-10-23. Reviewing 100 official release notes (v2.1.196–v2.1.310)
found no documented conhost SI/charset-reset fix. No fixed release version can
be confirmed; latest-release code was not installed or run. Local 2.1.280 and
2.1.295 remain verified affected. The bracketed-paste reset fix in v2.1.282 is
a separate input issue, not evidence this output bug was fixed.

Cleanup: `agents-cli stop 23232 --confirm 23232` and
`agents-cli stop 26836 --confirm 26836` succeeded. Test Terminal launcher shells
18100/11388 were also stopped through agents-cli. Only the test Terminal HWNDs
1049094/86381924 received WM_CLOSE; the owner's other windows were not targeted.

Evidence under ignored `.verification`: `tech238-conhost-matrix.json`,
`tech238-input-mode-matrix.json`, `tech238-tail-mitigation.json`,
`tech238-issue-status.json`, and `tech238-release-audit.json`.

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

The historical baseline checks above did not exercise post-turn resets.
The current outcome and outstanding upstream display bug are documented above.
