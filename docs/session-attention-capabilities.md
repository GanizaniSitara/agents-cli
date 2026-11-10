# Native/browser attention capability investigation

10 November 2026. Read-only feasibility evidence for the [session attention plan](desktop-attention-plan.md). The existing panel format stays unchanged. No new adapter is installed or enabled.

## Evidence levels

`Documented candidate` means an official product or platform document identifies a possible source/locator. `Observed metadata` means a process or window was seen without activation. Neither proves current session state, clear lifecycle, completion or exact navigation. Those require a controlled live conversation and recorded transitions. Unknown is not no pending requests.

| Surface | Current evidence | Completion / input wait | Conversation pointer | Remaining gate |
| --- | --- | --- | --- | --- |
| ChatGPT browser in Brave | Brave process and top-level window observed; selected title did not identify ChatGPT. Background tabs were not enumerated or selected. | Unverified. Browser presence and an idle composer are insufficient. | Unverified; an opted-in tab's verified conversation link/document identity is a candidate. | Owner-coordinated existing ChatGPT tab; scoped read-only observation; running, completed, waiting, cleared and replaced-document cases. |
| ChatGPT Windows desktop | Official Activity/notification and local-chat deep-link documentation; no native ChatGPT window identified by this limited inventory. This does not prove it is uninstalled. | Documented candidate, not observed or externally consumable yet. | Documented local-chat link candidate; source identity/account applicability unverified. | Actual available app/version; permitted evidence source; conversation identity and exact-pointer validation without focus changes. |
| Claude desktop / Code | Existing plan identifies desktop documentation. Current `claude.exe` processes are not proof of a desktop app; CLI support does not establish native hooks. | Unverified for desktop-created conversations. | App/window or conversation link unverified. | Identify actual desktop surface/version and supported lifecycle or scoped UIA evidence. |
| Other Copilot apps | Copilot CLI exists, but consumer, Microsoft 365 and IDE surfaces are separate products. | Unverified outside the CLI. | Product-specific and unverified. | Confirm intended product and an available test surface. |

No conversation body, composer text, browser database, cookie store, screenshot or accessibility tree was collected for this inventory. No app was launched, tab selected, desktop switched or window activated. The process inventory is a moment-in-time observation, not a capability certificate.

## Candidate mechanisms and limits

ChatGPT documents desktop completion, question and permission notifications and an Activity view with chat states. This identifies evidence to investigate; it is not a documented external stream usable by agents-cli. Browser notification settings do not establish an external session API. [Official documentation](https://learn.chatgpt.com/docs/notifications).

The documented `codex://threads/<thread-id>` form targets a local chat in the desktop app. It is a candidate only after the source provides that exact technical ID and the intended app/account is verified. Do not fabricate this ID from a web URL or assume cloud chats share a local ID. [Official desktop command reference](https://learn.chatgpt.com/docs/app/commands).

Windows UI Automation is a possible read-only native source. Any spike uses an isolated process and a non-window-owning COM MTA thread, a scoped traversal, and a parent wall deadline. A visible status control must prove its own semantics; missing controls, minimized windows and unavailable providers cannot be treated as completion. [Microsoft threading guidance](https://learn.microsoft.com/en-us/windows/win32/winauto/uiauto-threading).

A provider-scoped extension is a browser candidate, not an installed integration. Chrome supports extension/native-host communication over framed stdin/stdout, with an exact extension-origin allowlist and host registration. Content scripts require declared page access. Prepare exact permissions and native-host changes before seeking installation approval; verify Brave separately. No debugger/history/cookie/all-sites permissions or new network listener are proposed. [Native messaging](https://developer.chrome.com/docs/extensions/develop/concepts/native-messaging), [content scripts](https://developer.chrome.com/docs/extensions/develop/concepts/content-scripts).

## Pilot acceptance record to collect

Record product/version and transport, opaque conversation and turn/request identities, source scope/revision, and normalized transitions for running, completed, pending question/approval while running, answered/cleared, cancel/error, new turn and view/tab replacement. Initial idle and stop-button disappearance alone must never become FINISHED. Verify missed-event recovery, background/minimized/discarded coverage, stale expiry and reconnect before enabling alerts.

Record the strongest proven pointer independently: exact existing tab/conversation, verified conversation link, or app/window only. Do not claim navigation was tested unless the owner explicitly performs or authorizes that controlled navigation. Passive observation never opens links or changes the selected conversation.

The present blocker to a live pilot is the absence of an identified permitted AI conversation surface in the limited inventory. This is an owner-coordination gap, not a missing package: no installation, runtime change or application launch has been attempted.
