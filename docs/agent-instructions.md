# Coordinate Windows agent sessions with agents-cli

Use `agents-cli` through your local shell tool for user-authorized coordination
of existing Windows coding-agent consoles. It returns JSON. The executable must
be on PATH; check `agents-cli version` if it cannot be found.

1. Discover sessions with `agents-cli ls --fast` for metadata, or `agents-cli ls`
   for state and window titles. Resolve one target by PID or a unique label.
2. Inspect it with `agents-cli tail <ref> --lines 20`. Confirm it is the intended
   agent and is ready to receive input. Respect `polling_muted`; do not unmute
   another coordinator's session without authorization.
3. Send an authorized instruction using `agents-cli send <ref> --stdin`, with the
   message supplied through your command tool's stdin input. If stdin input is
   unavailable, an existing protected UTF-8 message file can be redirected in
   cmd.exe: `agents-cli send <ref> --stdin < prompt.txt`. Delete temporary prompt
   files after use. Avoid placing sensitive text in a shell command or an echo
   pipeline; use the workstation's credential tooling for actual secrets.
4. `--no-enter` types without submitting. Use `agents-cli key <ref> enter` only
   after checking that the composer contains the intended text. Other keys
   include `escape`, `up` and `down`. Check the returned `submitted` field when
   present and inspect the subsequent tail; a successful write alone does not
   prove execution. Do not blindly resend text or retry Enter in a dialog.
5. Wait with `agents-cli wait <ref> --idle-for 10s --timeout 10m`, then inspect
   the returned state and tail. Screen stability is a heuristic, not proof that
   the requested work completed. Avoid frequent polling and large screen dumps.
6. Stop only when the user authorized it: preview `agents-cli stop <ref>`, then
   use the exact resolved root PID in `agents-cli stop <ref> --confirm <pid>`.
   Never stop a session by executable name or guess its PID.

Normal sends use console injection. Explicit native Codex queue sends require
`--allow-argv` because that external CLI exposes the message in process
arguments; do not opt into this for sensitive content. If both agents are Claude
sessions and native `SendMessage` is available, use that channel instead.

`tail` and `wait` can return private screen content. Keep output within the
user-authorized context. Do not publish console captures, credentials or session
metadata, and do not start additional model sessions or assign work to peers
unless the user requested it.
