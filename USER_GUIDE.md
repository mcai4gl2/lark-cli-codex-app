# Slack User Guide

This guide explains how to set up a Slack app for this CLI, run the local
Socket Mode gateway, and use the Slack message commands.

The recommended operating mode is:

```text
Slack DM or channel mention
  -> local `lark slack gateway serve`
  -> local `codex exec`
  -> reply back to the originating Slack thread
```

Socket Mode is recommended because it keeps an outbound WebSocket connection
from your machine to Slack. You do not need a public HTTPS callback URL.

Official Slack references:

- Socket Mode: https://docs.slack.dev/apis/events-api/using-socket-mode/
- Events API: https://docs.slack.dev/apis/events-api/
- Token types: https://docs.slack.dev/authentication/tokens/
- OAuth scopes: https://docs.slack.dev/reference/scopes
- `chat.postMessage`: https://docs.slack.dev/reference/methods/chat.postMessage/
- `conversations.history`: https://docs.slack.dev/reference/methods/conversations.history/
- `conversations.replies`: https://docs.slack.dev/reference/methods/conversations.replies/
- `reactions.add`: https://docs.slack.dev/reference/methods/reactions.add/
- `reactions.get`: https://docs.slack.dev/reference/methods/reactions.get/
- `reactions.remove`: https://docs.slack.dev/reference/methods/reactions.remove/

## 1. Build Or Install The CLI

From the repository root:

```bash
docker run --rm -v "$PWD:/work" -w /work golang:1.24 \
  go build -ldflags "-s -w" -o ./lark ./cmd/lark
```

If Docker reports a git safe-directory error during build, rerun with:

```bash
docker run --rm -v "$PWD:/work" -w /work golang:1.24 sh -c \
  'git config --global --add safe.directory /work && go build -ldflags "-s -w" -o ./lark ./cmd/lark'
```

You can also install the Codex plugin wrapper and bundled skills:

```bash
./scripts/install-codex-plugin.sh
```

## 2. Create The Slack App

1. Open https://api.slack.com/apps and choose **Create New App**.
2. Choose **From scratch**.
3. Pick an app name, for example `Local Codex`.
4. Select the Slack workspace where you want to use it.

## 3. Enable Socket Mode

1. In the Slack app settings, open **Socket Mode**.
2. Enable Socket Mode.
3. Create an app-level token with the `connections:write` scope.
4. Copy the generated `xapp-...` token. This is `SLACK_APP_TOKEN`.

Socket Mode lets this local process receive Events API payloads through Slack's
WebSocket connection, so there is no Request URL to expose publicly.

## 4. Configure Bot Scopes

Open **OAuth & Permissions** and add these bot token scopes.

Minimum scopes for remote-controlling Codex:

| Scope | Why it is needed |
| --- | --- |
| `app_mentions:read` | Receive `app_mention` events when the bot is mentioned in channels. |
| `im:history` | Receive and read direct messages sent to the bot. |
| `chat:write` | Send acknowledgements and final replies. |

Scopes for Slack message CLI commands:

| Scope | Commands |
| --- | --- |
| `channels:history` | `lark slack msg history` for public channels where the bot has access. |
| `groups:history` | `lark slack msg history` for private channels where the bot has access. |
| `im:history` | `lark slack msg history` and `thread` for bot DMs. |
| `mpim:history` | Optional, only if you use multi-person DMs. |
| `reactions:read` | `lark slack msg react list`. |
| `reactions:write` | `lark slack msg react` and `react remove`. |

Optional read/list scopes if you later add richer Slack commands:

| Scope | Why you might add it |
| --- | --- |
| `channels:read` | Read public channel metadata. |
| `groups:read` | Read private channel metadata. |
| `im:read` | Read DM metadata. |
| `mpim:read` | Read multi-person DM metadata. |

After changing scopes, click **Install to Workspace** or **Reinstall to
Workspace**. Copy the `xoxb-...` **Bot User OAuth Token**. This is
`SLACK_BOT_TOKEN`.

## 5. Subscribe To Events

Open **Event Subscriptions** and enable events.

Under **Subscribe to bot events**, add:

| Event | Purpose |
| --- | --- |
| `app_mention` | Let channel mentions trigger Codex tasks. |
| `message.im` | Let Slack DMs trigger Codex tasks. |

With Socket Mode enabled, Slack does not require a public Request URL for these
event subscriptions.

## 6. Invite The Bot To Channels

For channel mentions and channel history to work, the bot needs access to the
conversation.

In each Slack channel where you want to use it:

```text
/invite @Local Codex
```

Direct messages do not require channel invitation. Open a DM with the app and
send a message.

## 7. Configure Local Environment

This project still uses `LARK_CONFIG_DIR` for the shared config root. Slack
state defaults to `.slack/` beside that root.

Example local setup:

```bash
export LARK_CONFIG_DIR="$HOME/.lark"
mkdir -p "$LARK_CONFIG_DIR"

export SLACK_APP_TOKEN="xapp-..."
export SLACK_BOT_TOKEN="xoxb-..."
```

Optional agent configuration:

```bash
export SLACK_AGENT_ENABLED=true
export SLACK_AGENT_WORKSPACE="$HOME/WorkSpace"
export SLACK_AGENT_CODEX_BINARY="codex"
export SLACK_AGENT_ACK_TEXT="Received. Working on it."
export SLACK_AGENT_RESULT_MAX_CHARS=3500
export SLACK_AGENT_TIMEOUT_MINUTES=20
```

Optional event log path:

```bash
export SLACK_GATEWAY_EVENT_LOG="$HOME/.slack/gateway-events.jsonl"
```

You can also put equivalent Slack settings in `.lark/config.yaml`; see
`config.example.yaml`.

## 8. How The Local Workspace Folder Works

There are two different local folders to understand:

| Folder type | Purpose |
| --- | --- |
| Config/state folder | Stores config, event logs, and desktop task queue state. |
| Codex workspace folder | The folder where `codex exec` is launched for an inbound Slack task. |

The Codex workspace is the important folder for agent behavior. When a Slack
message triggers the agent, this project runs Codex roughly like this:

```bash
codex -a never -s workspace-write exec \
  -C "$WORKSPACE" \
  --skip-git-repo-check \
  --output-last-message /tmp/.../last-message.txt \
  "$PROMPT"
```

The `-C "$WORKSPACE"` argument controls where Codex starts. If your Slack
message does not mention a folder or repository, Codex still starts in this
configured workspace and interprets the request from there.

Slack workspace selection order:

1. `--agent-workspace` passed to `lark slack gateway serve`.
2. `SLACK_AGENT_WORKSPACE`.
3. `slack.agent.workspace` in `.lark/config.yaml`.
4. Generic Lark agent workspace.
5. If nothing is configured:
   - `~/WorkSpace` if that folder exists.
   - otherwise the directory where you started `lark slack gateway serve`.
   - otherwise the parent of `LARK_CONFIG_DIR`.

Examples:

```bash
./lark slack gateway serve \
  --agent \
  --agent-workspace "$HOME/WorkSpace/lark-cli-codex-app"
```

In this setup, a Slack message like:

```text
fix the failing tests
```

is handled from:

```text
$HOME/WorkSpace/lark-cli-codex-app
```

If instead you run:

```bash
./lark slack gateway serve \
  --agent \
  --agent-workspace "$HOME/WorkSpace"
```

then Codex starts from the broader workspace folder. In that mode, Slack
messages should name the project or path:

```text
In lark-cli-codex-app, inspect the Slack gateway tests.
```

Current limitation: the agent prompt receives the current Slack message and
basic Slack coordinates. It does not automatically load prior Slack thread
history or previous event logs into the prompt. Event logs are useful for audit
and debugging, but they are not memory by themselves.

## 9. Recommended Two-Mode Setup

For your use case, the cleanest setup is to run two separate Slack apps and two
separate gateway processes:

1. A generic chat app for random topics and personal memory.
2. One project-specific app per active project, or at least one project app per
   workspace family.

This keeps permissions, event logs, Slack app identity, and Codex workspace
boundaries easy to reason about.

### Generic Chat App

Use this for open-ended questions, personal notes, research, and durable memory.

Recommended Slack app:

```text
Name: Codex Chat
Primary use: DMs and maybe one private #codex-chat channel
Workspace: ~/CodexChat
```

Recommended local folder:

```bash
mkdir -p "$HOME/CodexChat/topics"
cd "$HOME/CodexChat"
git init
cat > MEMORY.md <<'EOF'
# Memory

Use this file for durable preferences, facts, decisions, and long-running
context that should be remembered across generic chat sessions.
EOF
```

Recommended gateway command:

```bash
LARK_CONFIG_DIR="$HOME/.lark-codex-chat" \
SLACK_APP_TOKEN="xapp-generic-chat" \
SLACK_BOT_TOKEN="xoxb-generic-chat" \
SLACK_GATEWAY_EVENT_LOG="$HOME/CodexChat/.slack/gateway-events.jsonl" \
./lark slack gateway serve \
  --agent \
  --agent-workspace "$HOME/CodexChat"
```

Recommended Slack usage:

```text
Remember that I prefer concise implementation plans. Save that in MEMORY.md.
```

```text
Summarize this discussion into topics/2026-06-10-slack-setup.md.
```

```text
What do you remember about my preferred Slack/Codex setup?
```

Because current code does not automatically summarize Slack history, make memory
updates explicit when you want them persisted. The generic chat workspace gives
Codex a stable place to write and read those notes.

### Project-Specific App

Use this for code changes, repo maintenance, CI debugging, and project-specific
decisions.

Recommended Slack app:

```text
Name: Codex Project - <project-name>
Primary use: project channel mentions and project DMs
Workspace: exact project repository path
```

Recommended gateway command:

```bash
LARK_CONFIG_DIR="$HOME/.lark-codex-project-lark-cli" \
SLACK_APP_TOKEN="xapp-project" \
SLACK_BOT_TOKEN="xoxb-project" \
SLACK_GATEWAY_EVENT_LOG="$HOME/WorkSpace/lark-cli-codex-app/.slack/gateway-events.jsonl" \
./lark slack gateway serve \
  --agent \
  --agent-workspace "$HOME/WorkSpace/lark-cli-codex-app"
```

Recommended Slack usage:

```text
Run the focused Slack package tests and fix any failures.
```

```text
Update USER_GUIDE.md with the workspace folder behavior.
```

In project mode, avoid pointing the gateway at a broad folder unless you want
Codex to choose among multiple repositories. The safest default is one gateway
process per important project, with `--agent-workspace` set to the exact repo.

### Why Multiple Slack Apps

Multiple Slack apps are cleaner than one app trying to serve every purpose:

| Reason | Benefit |
| --- | --- |
| Separate bot identity | You can tell whether you are talking to generic Codex or project Codex. |
| Separate tokens | A leaked or rotated project token does not affect generic chat. |
| Separate logs | Generic history and project audit trails stay apart. |
| Separate workspaces | Codex starts in the right folder without guessing. |
| Separate scopes | Project apps can have channel/history scopes only where needed. |

If you prefer fewer Slack apps, a reasonable compromise is one generic app and
one project app pointed at `~/WorkSpace`, but project Slack messages should then
name the target repo explicitly.

### Suggested Launch Scripts

Create small local scripts so each mode is repeatable.

`~/bin/codex-chat-gateway`:

```bash
#!/usr/bin/env bash
set -euo pipefail

export LARK_CONFIG_DIR="$HOME/.lark-codex-chat"
export SLACK_APP_TOKEN="xapp-generic-chat"
export SLACK_BOT_TOKEN="xoxb-generic-chat"
export SLACK_GATEWAY_EVENT_LOG="$HOME/CodexChat/.slack/gateway-events.jsonl"

mkdir -p "$(dirname "$SLACK_GATEWAY_EVENT_LOG")"

LARK_BIN="${LARK_BIN:-$HOME/.local/bin/lark}"
exec "$LARK_BIN" slack gateway serve \
  --agent \
  --agent-workspace "$HOME/CodexChat"
```

`~/bin/codex-project-lark-cli-gateway`:

```bash
#!/usr/bin/env bash
set -euo pipefail

export LARK_CONFIG_DIR="$HOME/.lark-codex-project-lark-cli"
export SLACK_APP_TOKEN="xapp-project"
export SLACK_BOT_TOKEN="xoxb-project"
export SLACK_GATEWAY_EVENT_LOG="$HOME/WorkSpace/lark-cli-codex-app/.slack/gateway-events.jsonl"

mkdir -p "$(dirname "$SLACK_GATEWAY_EVENT_LOG")"

LARK_BIN="${LARK_BIN:-$HOME/.local/bin/lark}"
exec "$LARK_BIN" slack gateway serve \
  --agent \
  --agent-workspace "$HOME/WorkSpace/lark-cli-codex-app"
```

Then run each script in a separate terminal, tmux pane, launchd job, or systemd
user service.

### Future Memory Extension

The current gateway stores raw normalized events in a JSONL event log, but it
does not yet create channel folders, thread folders, or conversation summaries.

A useful extension would be to add a Slack memory/audit store such as:

```text
.slack/
  gateway-events.jsonl
  conversations/
    T123/
      C123/
        channel.json
        daily/
          2026-06-10.jsonl
        threads/
          1710000000.000100/
            events.jsonl
            summary.md
            memory.md
```

Recommended behavior for that extension:

- Write each inbound/outbound message to a channel and thread JSONL file.
- Periodically summarize long threads into `summary.md`.
- Store durable user-approved facts in `memory.md`.
- Include relevant summaries in the Codex prompt before dispatch.
- Keep raw JSONL as audit data and summaries as prompt-efficient memory.

Until that is implemented, the cleanest memory pattern is to use a generic
workspace such as `~/CodexChat` and explicitly ask Codex to update `MEMORY.md`
or topic files when something should persist.

## 10. Recommended Way To Run

For normal use, run the Slack gateway as a long-lived local process:

```bash
./lark slack gateway serve \
  --agent \
  --agent-workspace "$HOME/WorkSpace"
```

Recommended operational pattern:

1. Keep one terminal, tmux pane, systemd user service, or launchd job running
   `lark slack gateway serve`.
2. Use Slack DMs for private tasks.
3. Use channel mentions for shared tasks:

   ```text
   @Local Codex investigate the failing tests in repo X
   ```

4. Let the gateway reply in the originating Slack thread.
5. Keep `.slack/gateway-events.jsonl` for audit/debugging.

The gateway accepts:

```bash
./lark slack gateway serve --help
```

Common flags:

| Flag | Purpose |
| --- | --- |
| `--agent` | Dispatch Slack messages to `codex exec`. |
| `--agent-workspace PATH` | Workspace root used by Codex tasks. |
| `--event-log PATH` | JSONL event log path. |
| `--auto-reply-text TEXT` | Static reply template when not using the agent. |
| `--desktop-worker` | Run the local desktop task worker in the gateway process. |

For GUI requests, the more robust pattern is to keep the gateway focused on
Slack events and run the desktop helper in a foreground session with the right
desktop permissions:

```bash
./lark desktop helper serve
```

## 11. How To Use It From Slack

DM the app:

```text
Summarize the current status of /home/me/WorkSpace/project
```

Mention the app in a channel:

```text
@Local Codex please inspect the latest CI failure and suggest a fix
```

Ask for GUI work:

```text
/gui open https://example.com
```

The gateway ignores bot-originated messages to avoid loops. Channel messages
must mention the bot; ordinary channel chatter is ignored by this implementation.

## 12. Slack Message Commands

These commands use the bot token directly. They are useful for smoke tests,
manual replies, and compact history reads.

Send a message:

```bash
./lark slack msg send --channel C123 --text "hello"
```

Reply in a thread:

```bash
./lark slack msg send \
  --channel C123 \
  --thread-ts 1710000000.000100 \
  --text "reply"
```

Read channel history:

```bash
./lark slack msg history --channel C123 --limit 20
```

Read thread replies:

```bash
./lark slack msg thread \
  --channel C123 \
  --thread-ts 1710000000.000100 \
  --limit 20
```

Add a reaction:

```bash
./lark slack msg react \
  --channel C123 \
  --ts 1710000000.000100 \
  --reaction eyes
```

List reactions:

```bash
./lark slack msg react list \
  --channel C123 \
  --ts 1710000000.000100 \
  --full
```

Remove a reaction:

```bash
./lark slack msg react remove \
  --channel C123 \
  --ts 1710000000.000100 \
  --reaction eyes
```

## 13. Smoke Test Checklist

After installation and configuration:

1. Run:

   ```bash
   ./lark slack gateway serve --agent --agent-workspace "$HOME/WorkSpace"
   ```

2. Send a DM to the app.
3. Confirm the app posts an acknowledgement and final result.
4. Invite the bot to a test channel.
5. Mention the bot in that channel.
6. Confirm the reply lands in the Slack thread.
7. Run:

   ```bash
   ./lark slack msg history --channel C123 --limit 5
   ```

8. Add and remove a reaction on a known message timestamp.

## 14. Troubleshooting

`SLACK_APP_TOKEN is required`

- Set `SLACK_APP_TOKEN` to the `xapp-...` app-level token.
- Confirm the app-level token has `connections:write`.

`SLACK_BOT_TOKEN is required`

- Set `SLACK_BOT_TOKEN` to the `xoxb-...` bot token from **OAuth &
  Permissions**.
- Reinstall the app after changing scopes.

The gateway connects but channel mentions do not trigger tasks

- Confirm `app_mention` is subscribed under **Subscribe to bot events**.
- Confirm the bot is invited to the channel.
- Mention the bot explicitly; ordinary channel messages are ignored.

DMs do not trigger tasks

- Confirm `message.im` is subscribed under **Subscribe to bot events**.
- Confirm the bot token has `im:history`.
- Reinstall the app after changing scopes.

History commands fail with `missing_scope`

- Add the correct history scope for the conversation type:
  `channels:history`, `groups:history`, `im:history`, or `mpim:history`.
- Reinstall the app after changing scopes.

Reaction commands fail with `missing_scope`

- Add `reactions:read` for list.
- Add `reactions:write` for add/remove.
- Reinstall the app after changing scopes.

The bot cannot see a channel

- Invite the bot to the channel.
- For private channels, invite the bot and grant `groups:history` if you need
  history reads.

Codex does not run

- Start the gateway with `--agent` or set `SLACK_AGENT_ENABLED=true`.
- Confirm `codex` is installed and available, or set `SLACK_AGENT_CODEX_BINARY`.
- Confirm `--agent-workspace` or `SLACK_AGENT_WORKSPACE` points to the intended
  workspace.

Build fails in Docker with VCS status errors

- Use the safe-directory build command shown in section 1.
