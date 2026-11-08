# Privacy and release review

Reviewed 2026-11-08 for the Windows amd64 beta release.

## Release contents

The release ZIP contains only the compiled executable, README and this privacy
notice. Checksums accompany the ZIP and executable. No settings, screenshots,
session records, logs, provider profiles, task data or credentials are packaged.
Builds use `-trimpath` to remove the builder's absolute source paths.

## Data handled at runtime

The watcher reads local process metadata, console screens and, where an exact
session identity is available, Claude/Codex sub-agent lifecycle records. It
parses transcript files in memory for lifecycle markers; it does not save their
message bodies. Provider credentials are not required by agents-cli.

Local status and diagnostics are private data. By default they live under
`%LOCALAPPDATA%\agents-cli\watch` and can contain session names, window titles,
executable/profile paths, session IDs and process metadata. A window title can
itself contain sensitive text. Click diagnostics also record the foreground
window title. Do not include this directory in a shared bug report without
reviewing and redacting it. `--state-dir` can change its location.

Adopted names and mute settings live under `%APPDATA%\agents-cli`; stop audit
records live under `%LOCALAPPDATA%\agents-cli`. These are runtime files, not
embedded executable assets. File access uses inherited Windows permissions;
these files are not encrypted by agents-cli.

`tail` deliberately returns screen text. Treat its output and any model or log
receiving it as sensitive. Prefer `send --stdin` for message input. Literal
messages on a command line can appear in shell history or process listings.
The optional native Codex queue requires `--allow-argv` for this reason.

## Network behaviour

Source review found no network client, telemetry upload, update checker or
listening server in agents-cli. Console operations use local Windows APIs and
detached copies of the same executable. Explicit native sends can invoke the
target Codex executable; the target agent retains its own provider/network
behaviour. This does not constitute a guarantee about third-party agents.

## Review limits

Tracked files and reachable Git history were scanned for common private-key,
GitHub-token, provider-key and AWS-key patterns. No matches were found in the
249 historical blobs checked before this release. The current source fixture
containing a builder username was anonymised; historical versions retain that
old example path. Internal task IDs and development paths remain in engineering
documentation. The repository stays private.

The release executable is checked separately for personal markers and absolute
build paths, and the archive is checked against an explicit file allowlist.
Pattern scanning and source review cannot prove the absence of every possible
secret. This is a practical release review, not an independent security audit.

## Beta limitations

State/question/review detection is heuristic. Sub-agent counts stay unknown
when identity or lifecycle cannot be established. Numbered shortcuts cover the
first ten displayed rows. A steady blue taskbar button background is not
implemented. The executable is not Authenticode signed.
