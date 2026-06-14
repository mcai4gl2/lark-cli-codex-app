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
| `reactions:write` | `lark slack msg react`, `react remove`, and the gateway processing reaction when enabled. |

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

Recover mode uses Slack's `conversations.replies` API for known participating
threads. If you use recover mode outside DMs, the app also needs the matching
history scope for those conversations, such as `channels:history` for public
channels or `groups:history` for private channels where the bot is installed.

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
export SLACK_AGENT_BACKEND="codex"   # codex or agy
export SLACK_AGENT_BINARY=""         # empty uses backend default
export SLACK_AGENT_ARGS=""           # comma-separated extra backend args
export SLACK_AGENT_WORKSPACE="$HOME/WorkSpace"
export SLACK_AGENT_CODEX_BINARY="codex"  # legacy Codex-only alias
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
| Agent workspace folder | The folder where the selected backend starts for an inbound Slack task. |

The agent workspace is the important folder for backend behavior. With the
default Codex backend, a Slack message runs roughly like this:

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

With the Antigravity CLI backend, first validate the installed `agy` binary:

```bash
agy --help
agy --version
agy --add-dir "$PWD" --prompt "Reply with exactly: agy-ok"
```

Then run the gateway with:

```bash
./lark slack gateway serve \
  --agent \
  --agent-backend agy \
  --agent-workspace "$HOME/WorkSpace/lark-cli-codex-app"
```

Config equivalent:

```yaml
slack:
  agent:
    enabled: true
    backend: "agy"
    binary: "agy"
    workspace: "~/WorkSpace/lark-cli-codex-app"
    model: ""
```

`codex_binary` remains supported for old Codex-only configs, but new configs
should prefer `backend`, `binary`, and `args`.

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
  --memory \
  --memory-root "$HOME/WorkSpace/.slack/conversations" \
  --agent-workspace "$HOME/WorkSpace"
```

then Codex starts from the broader workspace folder. In that mode, Slack
messages should name the project or path:

```text
In lark-cli-codex-app, inspect the Slack gateway tests.
```

When Slack memory is enabled, the gateway writes inbound records to both the
thread log and the channel daily log under `.slack/conversations/`. Final Codex
replies are written to the thread log. Before dispatching a Slack task to Codex,
the gateway loads existing channel memory, thread memory, thread summaries, and
a bounded recent thread transcript into the prompt when those files or records
exist.

Enable it with `--memory`:

```bash
./lark slack gateway serve \
  --agent \
  --memory \
  --memory-root "$HOME/CodexChat/.slack/conversations" \
  --agent-workspace "$HOME/CodexChat"
```

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
  --memory \
  --memory-root "$HOME/CodexChat/.slack/conversations" \
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

Use `lark slack memory append` for durable facts you explicitly want injected
into future prompts:

```bash
./lark slack memory append \
  --channel D123 \
  --scope channel \
  --text "- User prefers concise implementation plans."
```

Automatic summarization is still deferred, so ask Codex to summarize important
threads or append durable facts when you want them persisted.

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
  --memory \
  --memory-root "$HOME/WorkSpace/lark-cli-codex-app/.slack/conversations" \
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

`~/bin/codex-chat-gateway.sh`:

```bash
#!/usr/bin/env bash
set -euo pipefail

export LARK_CONFIG_DIR="$HOME/.lark-codex-chat"
export SLACK_APP_TOKEN="xapp-generic-chat"
export SLACK_BOT_TOKEN="xoxb-generic-chat"
export SLACK_GATEWAY_EVENT_LOG="$HOME/CodexChat/.slack/gateway-events.jsonl"

GATEWAY_LOG="${GATEWAY_LOG:-$HOME/CodexChat/.slack/gateway.log}"
GATEWAY_PID_FILE="${GATEWAY_PID_FILE:-$HOME/CodexChat/.slack/gateway.pid}"

mkdir -p "$(dirname "$SLACK_GATEWAY_EVENT_LOG")" "$(dirname "$GATEWAY_LOG")" "$(dirname "$GATEWAY_PID_FILE")"

LARK_BIN="${LARK_BIN:-$HOME/.local/bin/lark}"
if [[ -f "$GATEWAY_PID_FILE" ]]; then
  existing_pid="$(cat "$GATEWAY_PID_FILE")"
  if [[ -n "$existing_pid" ]] && kill -0 "$existing_pid" 2>/dev/null; then
    echo "lark slack gateway is already running: pid=$existing_pid"
    echo "log: $GATEWAY_LOG"
    exit 0
  fi
  rm -f "$GATEWAY_PID_FILE"
fi

gateway_cmd=(
  "$LARK_BIN" slack gateway serve
  --agent \
  --memory \
  --memory-root "$HOME/CodexChat/.slack/conversations" \
  --agent-workspace "$HOME/CodexChat"
)

nohup setsid -f bash -c '
  pid_file="$1"
  log_file="$2"
  shift 2

  (
    echo "starting lark slack gateway at $(date -Is)"
    "$@" &
    child_pid=$!
    printf "%s\n" "$child_pid" > "$pid_file"
    wait "$child_pid"
    status=$?
    echo "lark slack gateway exited at $(date -Is) status=$status"
    exit "$status"
  ) 2>&1 | tee -a "$log_file" >/dev/null
' bash "$GATEWAY_PID_FILE" "$GATEWAY_LOG" "${gateway_cmd[@]}" >/dev/null 2>&1 &

for _ in {1..50}; do
  if [[ -s "$GATEWAY_PID_FILE" ]]; then
    break
  fi
  sleep 0.1
done

gateway_pid="$(cat "$GATEWAY_PID_FILE")"
if ! kill -0 "$gateway_pid" 2>/dev/null; then
  echo "failed to start lark slack gateway; recent log:"
  tail -n 40 "$GATEWAY_LOG" || true
  exit 1
fi

echo "started lark slack gateway: pid=$gateway_pid"
echo "log: $GATEWAY_LOG"
echo "follow: tail -f '$GATEWAY_LOG'"
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
  --memory \
  --memory-root "$HOME/WorkSpace/lark-cli-codex-app/.slack/conversations" \
  --agent-workspace "$HOME/WorkSpace/lark-cli-codex-app"
```

Then run each script in a separate terminal, tmux pane, launchd job, or systemd
user service.

The background chat launcher above detaches the gateway with `nohup` and
`setsid`, stores the real `lark` process ID in
`$HOME/CodexChat/.slack/gateway.pid`, and appends stdout/stderr to
`$HOME/CodexChat/.slack/gateway.log`.

Useful operations:

```bash
~/bin/codex-chat-gateway.sh
tail -f "$HOME/CodexChat/.slack/gateway.log"
tail -f "$HOME/CodexChat/.slack/gateway-events.jsonl"
kill "$(cat "$HOME/CodexChat/.slack/gateway.pid")"
```

The plain `gateway.log` file is process-oriented: startup JSON, gateway errors,
agent dispatch errors, and exit status. The `gateway-events.jsonl` file is the
structured Slack event audit log. Treat both as private because Slack payloads
may include channel text, user IDs, and message metadata.

### Slack Memory Files

With `--memory` enabled, the gateway creates a Slack memory/audit store under
the configured `--memory-root`:

```text
.slack/conversations/
  T123/
    C123/
      memory.md
      daily/
        2026-06-10.jsonl
      threads/
        1710000000.000100/
          events.jsonl
          summary.md
          memory.md
```

The files have different purposes:

- `daily/YYYY-MM-DD.jsonl` stores inbound records for channel-level audit.
- `threads/<thread-ts>/events.jsonl` stores inbound records and final outbound
  Codex replies for that thread. Recent records are injected into future Codex
  prompts so follow-up replies have conversational context.
- Channel `memory.md` stores durable channel facts and preferences.
- Thread `memory.md` stores durable thread-specific facts.
- Thread `summary.md` stores prompt-efficient summaries for long threads.

The gateway loads channel memory, thread memory, thread summary Markdown, and a
bounded recent transcript from `events.jsonl` into the Codex prompt when those
files or records exist. It does not automatically summarize long threads yet.

Transcript injection is controlled by:

```bash
SLACK_MEMORY_INCLUDE_THREAD_TRANSCRIPT=true
SLACK_MEMORY_MAX_TRANSCRIPT_CHARS=8000
SLACK_MEMORY_MAX_TRANSCRIPT_RECORDS=30
```

The transcript is background context only. The latest user message remains the
primary request.

Useful commands:

```bash
./lark slack memory path --channel D123 --thread-ts 1710000000.000100
./lark slack memory show --channel D123 --scope channel
./lark slack memory show --channel D123 --thread-ts 1710000000.000100 --scope summary
./lark slack memory append --channel D123 --scope channel --text "- User prefers concise implementation plans."
./lark slack memory append --channel D123 --thread-ts 1710000000.000100 --scope summary --text "- Thread discussed Slack memory setup."
```

### Slack Recover Mode

Slack recover mode lets a long-running gateway catch up missed messages in
threads it already knows about. The recovery state file lives under the Slack
memory root:

```text
.slack/conversations/.state/recover-state.json
```

The default is thread mode:

```bash
./lark slack gateway serve --recover-mode thread
```

The equivalent config setting is:

```yaml
slack:
  gateway:
    recover_mode: "thread"
```

In `thread` mode, the gateway remembers Slack threads where it is participating
and, on startup or Socket Mode reconnect, calls `conversations.replies` to catch
up missed messages. New user messages in those known participating threads are
processed even when they do not mention the bot.

Outside known participating threads, channel messages still need a bot mention.
The gateway does not scan whole channels, and a mention is only required to
start participation in a channel thread. DMs can start tasks without a mention.

Other recover modes:

| Mode | Behavior |
| --- | --- |
| `mention-dm` | Catch up only messages that would normally trigger the bot, such as app mentions and DMs. |
| `off` | Disable recover mode. |

The gateway adds an `eyes` reaction by default while it is processing a Slack
message:

```bash
./lark slack gateway serve --processing-reaction eyes
```

The equivalent config setting is:

```yaml
slack:
  gateway:
    processing_reaction: "eyes"
```

Set `--processing-reaction ""` or `slack.gateway.processing_reaction: ""` to
disable processing reactions.

## 10. Recommended Way To Run

For normal use, run the Slack gateway as a long-lived local process:

```bash
./lark slack gateway serve \
  --agent \
  --memory \
  --memory-root "$HOME/WorkSpace/.slack/conversations" \
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
| `--agent` | Dispatch Slack messages to the configured local agent backend. |
| `--agent-backend NAME` | Agent backend: `codex` or `agy`. |
| `--agent-binary PATH` | Backend binary path or command name. |
| `--agent-workspace PATH` | Workspace root used by agent tasks. |
| `--memory` | Persist Slack audit logs and load explicit memory Markdown into prompts. |
| `--memory-root PATH` | Root folder for channel/thread memory and audit files. |
| `--memory-max-section-chars N` | Per-section character limit for loaded memory Markdown. |
| `--event-log PATH` | JSONL event log path. |
| `--recover-mode MODE` | Slack catch-up mode: `thread`, `mention-dm`, or `off`. Defaults to `thread`. |
| `--processing-reaction NAME` | Emoji reaction used while processing Slack messages. Defaults to `eyes`; empty disables reactions. |
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

Agent does not run

- Start the gateway with `--agent` or set `SLACK_AGENT_ENABLED=true`.
- Confirm the selected backend is installed and available. For new configs, use
  `SLACK_AGENT_BACKEND` and `SLACK_AGENT_BINARY`; `SLACK_AGENT_CODEX_BINARY`
  remains supported for legacy Codex configs.
- Confirm `--agent-workspace` or `SLACK_AGENT_WORKSPACE` points to the intended
  workspace.

Build fails in Docker with VCS status errors

- Use the safe-directory build command shown in section 1.
