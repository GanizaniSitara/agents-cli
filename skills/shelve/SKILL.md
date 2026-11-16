---
name: shelve
description: Request confirmed shelving of the current agent session through agents-cli, saving its conversation for later resume and closing its window when idle. Use when the user asks to shelve or store this session.
---

Run `agents-cli shelve self`. This opens the existing Session Shelf confirmation. Leave that confirmation for the user; do not click, accept or bypass it.

If the command returns `review_opened: true`, say only that the Shelf confirmation is open. This does not mean shelving has completed. Finish the turn so a confirmed pending request can run once the session is idle. Do not start more work, wait for yourself to become idle, or send `/exit` yourself.

Use an explicit PID or unique label only when the user identifies another session. If self-identification fails, run `agents-cli watch list` and ask which row; never guess from a title, foreground window, list position or project folder.

Verified shelving currently supports Claude and Codex only. Other providers must remain open; explain that limit rather than saving an unverified resume command. Retrieve saved sessions from `agents-cli shelf ui` using Resume, or `agents-cli shelf resume ID` after identifying an exact saved entry with `agents-cli shelf list`.

Do not install software, force-stop a process, discard a draft, switch desktops, or claim success from the review-opened receipt.
