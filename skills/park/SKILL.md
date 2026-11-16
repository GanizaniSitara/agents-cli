---
name: park
description: Park or unpark the current agent session in the agents-cli watcher when the user asks to park this session. Leaves the session running and its window open.
---

Run `agents-cli park self`. For an explicit request to unpark, run `agents-cli unpark self`.

Use an explicit PID or unique label only when the user identifies another session. Never guess from the foreground window, list position, or a matching project folder. If self-identification fails, run `agents-cli watch list` and ask the user to identify the intended row; do not choose a substitute.

Success requires a JSON receipt with the intended PID and `parked: true` (or false for unpark). Report it in one line. Parking leaves work running, suppresses attention and moves the row below unparked sessions. It does not close, shelve or change desktops.

On a machine without the command or a running watcher, report the exact error. Do not install software, send keystrokes, or change settings to work around it.
