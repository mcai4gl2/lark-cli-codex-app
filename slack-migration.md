# Slack Migration Design

## Goal

Port the current Lark/Feishu Codex control loop to Slack while preserving the core workflow:

```text
Human sends a Slack DM or channel mention
  -> local Slack gateway receives the event
  -> event is normalized and persisted locally
  -> local Codex runs through `codex exec`
  -> result is posted back to the originating Slack thread
  -> optional desktop GUI requests go through the same local desktop task queue
```

The migration should not start as a full rewrite. The existing project already has useful boundaries:

- `internal/inbound` normalizes message events and writes JSONL logs.
- `internal/agent` dispatches normalized messages to `codex exec`.
- `internal/desktop` detects GUI requests and queues local desktop tasks.
- `internal/cmd/gateway.go` exposes a long-running local gateway command.
- `internal/api/messages.go` contains the current Lark-specific send/reply behavior.

The main design task is to introduce Slack-specific adapters around events, auth, and message APIs, then remove Lark assumptions from the shared inbound, agent, and desktop paths.

## Slack Platform Shape

Use Slack Socket Mode for the first implementation. It matches the current Lark WebSocket gateway model: the local process keeps an outbound WebSocket connection and does not need a public HTTPS callback URL.

Slack's Socket Mode uses an app-level token for WebSocket connections and a bot token for Web API calls. Slack documents app-level tokens as `xapp-...` tokens and bot tokens as `xoxb-...` tokens. Socket Mode can receive Events API payloads without exposing a public endpoint.

Primary Slack subscriptions:

- `app_mention`: receive messages that mention the bot in channels.
- `message.im`: receive direct messages sent to the bot.
- Optional later: `message.mpim` and selected `message.channels` / `message.groups` if the app should see broader channel history.

Primary Slack Web API calls:

- `chat.postMessage` for acknowledgements and final replies.
- `conversations.history` for compact chat history reads.
- `conversations.replies` for thread context.
- Optional later: file upload/download, reactions, users lookup, reminders, canvases, lists, and workflow steps.

Official Slack references:

- Socket Mode: https://docs.slack.dev/apis/events-api/using-socket-mode/
- `app_mention`: https://docs.slack.dev/reference/events/app_mention/
- `message.im`: https://docs.slack.dev/reference/events/message.im/
- `chat.postMessage`: https://docs.slack.dev/reference/methods/chat.postMessage/
- Slack token types: https://docs.slack.dev/authentication/tokens/

## Recommended Approach

Build a Slack-first sibling CLI, but share the reusable local Codex bridge.

Concretely:

- Keep the existing Lark CLI behavior intact.
- Add a Slack command namespace and Slack packages.
- Extract platform-neutral interfaces where Lark is currently embedded in shared code.
- Let Slack and Lark each provide their own event decoder and message client.

This gives a working Slack path quickly without forcing all existing Lark API commands into a generic abstraction upfront.

Alternative approaches considered:

- Full fork to `slack-cli-codex-app`: simpler mentally, but duplicates the Codex bridge, desktop queue, event logging, and future fixes.
- Full provider abstraction across every command: cleaner long term, but too large because Lark documents, bitable, minutes, sheets, and contacts do not map one-to-one to Slack.
- Minimal Slack gateway only: fastest, but leaves `internal/agent` and `internal/desktop` coupled to Lark replies and makes the next feature painful.

The recommended path is a middle ground: generalize the transport and reply surfaces now, but leave Lark-specific productivity commands alone until Slack equivalents are explicitly needed.

## Target Package Layout

Proposed package layout:

```text
internal/platform/
  inbound.go        # provider-neutral inbound event and reply interfaces
  messenger.go      # message send/reply abstractions

internal/slack/
  client.go         # Slack Web API client
  events.go         # Slack event envelope parsing and normalization
  socket.go         # Socket Mode gateway
  config.go         # Slack config accessors if not folded into internal/config

internal/larkbridge/
  messenger.go      # adapter from existing internal/api.Client to platform.Messenger

internal/agent/
  codex.go          # uses platform.Messenger instead of internal/api.Client

internal/desktop/
  task.go           # stores provider/thread reply coordinates, not only Lark message_id

internal/cmd/
  slack.go          # `lark slack ...` during transition, or root command renamed later
```

If the project becomes Slack-only, rename the binary and module later. For the first migration, keep the current binary and add commands such as:

```bash
lark slack gateway serve --agent --agent-workspace ~/WorkSpace
lark slack msg send --channel C123 --text "hello"
lark slack msg history --channel C123 --limit 20
```

A later cleanup can rename the executable to `codex-chat` or `slack-codex` once both platforms are not meant to coexist.

## Shared Inbound Event Model

The current `inbound.LoggedEvent` is close to what Slack needs, but field names are Lark-flavored. Replace or wrap it with a provider-neutral model.

Proposed fields:

```go
type MessageEvent struct {
    Provider      string          `json:"provider"`
    ReceivedAt    string          `json:"received_at"`
    EventID       string          `json:"event_id,omitempty"`
    EventType     string          `json:"event_type,omitempty"`
    TeamID        string          `json:"team_id,omitempty"`
    ChannelID     string          `json:"channel_id,omitempty"`
    ChannelType   string          `json:"channel_type,omitempty"`
    MessageID     string          `json:"message_id,omitempty"`
    ThreadID      string          `json:"thread_id,omitempty"`
    UserID        string          `json:"user_id,omitempty"`
    UserName      string          `json:"user_name,omitempty"`
    BotID         string          `json:"bot_id,omitempty"`
    MessageType   string          `json:"message_type,omitempty"`
    MessageText   string          `json:"message_text,omitempty"`
    RawContent    string          `json:"raw_content,omitempty"`
    RawEvent      json.RawMessage `json:"raw_event,omitempty"`
}
```

Slack mapping:

- `TeamID`: event wrapper `team_id`.
- `ChannelID`: event `channel`.
- `ChannelType`: event `channel_type` for message events, or inferred from channel prefix when unavailable.
- `MessageID`: event `ts`.
- `ThreadID`: event `thread_ts` if present, otherwise event `ts`.
- `UserID`: event `user`.
- `MessageText`: event `text`, with the bot mention stripped for `app_mention`.
- `EventID`: event wrapper `event_id`.
- `EventType`: event `type`, e.g. `app_mention` or `message`.

Lark mapping can preserve the existing values:

- `Provider`: `lark`.
- `ChannelID`: current `ChatID`.
- `MessageID`: current `MessageID`.
- `ThreadID`: current `RootID` if present, otherwise `MessageID`.
- `UserID`: current `SenderOpenID` or `SenderUserID`.

This model lets the agent and desktop queue talk about "reply target" instead of Lark-specific IDs.

## Message Reply Interface

The current agent directly calls `api.Client.ReplyMessage`, which is the strongest Lark coupling in the Codex bridge.

Introduce a small interface:

```go
type MessageTarget struct {
    Provider    string
    TeamID      string
    ChannelID   string
    ThreadID    string
    UserID      string
}

type Messenger interface {
    Reply(ctx context.Context, event MessageEvent, text string) error
    Send(ctx context.Context, target MessageTarget, text string) error
}
```

Slack implementation:

- Use `chat.postMessage`.
- Set `channel` to `event.ChannelID`.
- Set `thread_ts` to `event.ThreadID` when replying in a thread.
- Use plain `text` first. Add Block Kit later only when needed.
- Keep `unfurl_links=false` and `unfurl_media=false` by default to reduce noisy replies.

Lark implementation:

- Use the existing `ReplyMessage(messageID, "text", content, rootID, true)`.
- Keep the current JSON content formatting.

Agent changes:

- `agent.Runner` should receive a `platform.Messenger`.
- `buildPrompt` should mention the source platform dynamically.
- Rename `trimForFeishu` to `trimForChat`.
- Increase Slack default `result_max_chars` to around 3500. Slack recommends keeping `text` under 4000 characters for best results and truncates very long messages.

## Slack Gateway Design

Implement `internal/slack/socket.go` as a local gateway equivalent to `internal/gateway/service.go`.

Responsibilities:

- Connect to Slack Socket Mode using `SLACK_APP_TOKEN`.
- Use `SLACK_BOT_TOKEN` for Web API calls.
- Listen for event envelopes.
- Acknowledge Socket Mode envelopes promptly.
- Normalize supported events into `platform.MessageEvent`.
- Ignore bot-originated messages to prevent loops.
- Persist JSONL events through the shared inbound logger.
- Route desktop-looking requests to the desktop queue.
- Dispatch ordinary tasks to `agent.Runner`.

Event handling rules:

- `app_mention`: strip `<@BOTID>` from the beginning or anywhere in the text, then dispatch.
- `message.im`: dispatch all user messages except bot messages, edits, deletes, and subtype events.
- Thread replies: use `thread_ts` if provided; otherwise use the message `ts`.
- Channel messages without a mention should be ignored in the first version.

Failure behavior:

- If Slack reply fails, log the error with channel and thread coordinates.
- If Codex times out, reply with a concise timeout message.
- If a Slack event has no usable text, log it and do not dispatch.
- If event persistence fails, return/log a hard error because replayability is part of the control-plane design.

## Config

Add Slack config alongside the existing Lark config. During the transition, keep secrets in environment variables.

Example:

```yaml
slack:
  bot_token: ""        # prefer SLACK_BOT_TOKEN
  app_token: ""        # prefer SLACK_APP_TOKEN
  signing_secret: ""   # for future webhook mode; prefer SLACK_SIGNING_SECRET
  bot_user_id: ""      # optional; can be discovered with auth.test
  gateway:
    event_log: ".slack/gateway-events.jsonl"
    auto_reply_text: ""
  agent:
    enabled: false
    codex_binary: "codex"
    workspace: "~/WorkSpace"
    model: ""
    ack_text: "Received. Working on it."
    result_max_chars: 3500
    timeout_minutes: 20
```

Environment variables:

- `SLACK_BOT_TOKEN`
- `SLACK_APP_TOKEN`
- `SLACK_SIGNING_SECRET`
- `SLACK_BOT_USER_ID`
- `SLACK_AGENT_ENABLED`
- `SLACK_AGENT_WORKSPACE`
- `SLACK_AGENT_MODEL`
- `SLACK_AGENT_ACK_TEXT`
- `SLACK_AGENT_RESULT_MAX_CHARS`
- `SLACK_GATEWAY_EVENT_LOG`

For local storage, prefer `.slack/` for Slack-specific state and keep `.lark/` untouched.

## Slack App Setup

Create a Slack app with:

- Socket Mode enabled.
- App-level token with `connections:write`.
- Bot token scopes:
  - `app_mentions:read`
  - `im:history`
  - `chat:write`
  - `channels:history` only if channel history commands are needed.
  - `groups:history` only if private-channel history commands are needed.
  - `channels:read`, `groups:read`, `im:read`, `mpim:read` only if listing conversations is needed.
  - `reactions:read` / `reactions:write` only if reaction commands are ported.
  - `files:read` / `files:write` only if attachment support is ported.
- Event subscriptions:
  - `app_mention`
  - `message.im`

Install the app into the workspace, invite the bot to channels where it should respond, and run:

```bash
lark slack gateway serve --agent --agent-workspace ~/WorkSpace
```

## Feature Mapping

| Current Lark feature | Slack equivalent | Migration priority |
| --- | --- | --- |
| Gateway WebSocket events | Slack Socket Mode Events API | P0 |
| Agent bridge to `codex exec` | Same agent with Slack messenger | P0 |
| Reply to originating chat/thread | `chat.postMessage` with `thread_ts` | P0 |
| JSONL event log | Same shape with `provider=slack` | P0 |
| Desktop queue | Same queue with provider-aware reply targets | P0 |
| `msg history` | `conversations.history` / `conversations.replies` | P1 |
| `msg send` | `chat.postMessage` | P1 |
| Message reactions | `reactions.add`, `reactions.remove`, `reactions.get` | P2 |
| Attachments/resources | Slack files APIs | P2 |
| Contacts/user lookup | `users.info`, `users.lookupByEmail`, `users.list` | P2 |
| Calendar | Usually Google/Microsoft calendar, not Slack-native | Defer |
| Docs/wiki/sheets/bitable/minutes | No direct Slack equivalent | Do not port directly |
| Mail cache | Independent IMAP feature | Keep as-is or split later |

The first Slack version should focus on remote-controlling Codex from Slack, not replacing every Lark productivity command.

## Implementation Phases

### Phase 1: Shared Reply Abstraction

Status: Complete.

- [x] Add provider-neutral message event and messenger interfaces.
- [x] Adapt `internal/inbound` to write/read the neutral event model.
- [x] Change `agent.Runner` to depend on `Messenger`.
- [x] Change desktop queue tasks to store provider, channel, message ID, and thread ID.
- [x] Add a Lark messenger adapter so existing gateway behavior remains unchanged.
- [x] Update tests for inbound extraction, agent dispatch, and desktop completion replies.

Implementation notes:

- Added `internal/platform` for `MessageEvent`, `MessageTarget`, and `Messenger`.
- Added `internal/larkbridge` to adapt the existing Lark API client to the neutral messenger interface.
- Existing Lark gateway and webhook paths still normalize into shared inbound events.
- Verified with `go test ./...` using the Docker-based Go toolchain documented in `AGENTS.md`.

### Phase 2: Slack Gateway MVP

Status: Complete.

- [x] Add Slack config and env bindings.
- [x] Add Slack Web API client for `chat.postMessage` and `auth.test`.
- [x] Add Slack Socket Mode client.
- [x] Normalize `app_mention` and `message.im` events.
- [x] Add `slack gateway serve` command.
- [x] Verify with automated tests:
  - [x] DM events normalize into shared inbound events.
  - [x] Channel mentions normalize into shared inbound events.
  - [x] Replies target the correct Slack thread timestamp.
  - [x] Bot/self messages do not self-trigger.
  - [x] GUI requests are queued with Slack reply coordinates.

Implementation notes:

- Added Slack config/env support for `SLACK_BOT_TOKEN`, `SLACK_APP_TOKEN`, `SLACK_SIGNING_SECRET`, `SLACK_BOT_USER_ID`, Slack gateway settings, and Slack agent settings.
- Added `.slack/` defaults for Slack event logs and desktop task state so existing `.lark/` state is untouched.
- Added `internal/slack` with a minimal Web API client, event normalization, and Socket Mode gateway service.
- Added `lark slack gateway serve` with agent, event-log, auto-reply, workspace, and desktop-worker flags.
- Added tests for Slack config, Web API calls, event normalization, command registration, and desktop routing.
- Verified with Docker-based `gofmt`, focused package tests, full `go test ./...`, and CLI build.

Manual Slack workspace smoke tests remain to be run with real Slack app credentials:

- DM triggers Codex.
- Channel mention triggers Codex.
- Replies land in the correct Slack thread.
- GUI request completion replies to the Slack thread.

### Phase 3: Slack CLI Commands

- Add `slack msg send`.
- Add `slack msg history`.
- Add `slack msg thread`.
- Add optional reaction commands.
- Add compact JSON outputs that mirror the style of current Lark commands.

### Phase 4: Packaging and Naming

- Decide whether this remains a dual-provider repo or becomes a Slack-focused fork.
- If dual-provider, consider a neutral binary name such as `codex-chat`.
- If Slack-only, update README, examples, install script, plugin manifest, and skills.
- Add Slack-specific skills, e.g. `skills/slack-messages/SKILL.md`.

## Testing Plan

Unit tests:

- Slack event normalization for `app_mention`.
- Slack event normalization for `message.im`.
- Bot/self-message filtering.
- Mention stripping.
- Thread target selection.
- Message trimming.
- Messenger interface behavior using a fake Slack HTTP server.
- Desktop queue provider-aware serialization.

Integration tests:

- Run Slack gateway with a fake Socket Mode event source if practical.
- Test `slack msg send` against a fake Slack Web API.
- Preserve existing Lark gateway and webhook tests.

Manual smoke tests:

- Send a DM to the Slack app and confirm ack plus final Codex reply.
- Mention the app in a channel and confirm threaded reply.
- Send `/gui open https://openai.com` and confirm desktop queue behavior.
- Kill and restart the gateway, then confirm event log continuity.

## Risks and Decisions

- Slack Socket Mode protocol can be implemented directly, but using an SDK is lower risk. Prefer `github.com/slack-go/slack` if it supports the needed Socket Mode and Web API operations cleanly.
- Slack has different message formatting from Lark. Start with plain text and avoid rich Block Kit until there is a concrete UI need.
- Slack channel visibility depends on scopes and bot membership. The MVP should respond only to DMs and mentions in channels where the bot is present.
- Slack `chat.postMessage` uses `channel` plus `thread_ts`; any shared task queue must store those fields instead of only Lark message IDs.
- Lark-specific features such as docs, sheets, bitable, and minutes should not be forced into Slack abstractions. Port only the common chat control plane first.

## Definition of Done for MVP

- A local command can run a Slack Socket Mode gateway without a public callback URL.
- Slack DMs can start Codex tasks.
- Slack channel mentions can start Codex tasks.
- Codex acknowledgements and final results reply in the originating Slack thread.
- Event logs include provider, team, channel, user, message timestamp, thread timestamp, text, and raw event.
- Desktop GUI requests from Slack enter the existing queue and can reply on completion.
- Existing Lark gateway tests still pass.
