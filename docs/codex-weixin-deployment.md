# Codex Weixin Gateway Deployment

This deployment runs the `lark` Weixin (WeChat) long-poll gateway as a local
background process, alongside the existing Slack gateway. A user DMs the bot in
WeChat, the gateway dispatches work to `codex exec`, and the reply is sent back
into the same conversation.

It mirrors [`codex-slack-deployment.md`](./codex-slack-deployment.md); see
[`weixin-gateway.md`](./weixin-gateway.md) for the feature reference.

## Current Layout

- Repository: `/home/ligeng/Codes/lark-cli-codex-app`
- Binary: `/home/ligeng/.local/bin/lark`
- Login script: `/home/ligeng/bin/codex-weixin-login.sh`
- Startup script: `/home/ligeng/bin/codex-weixin-gateway.sh`
- Codex workspace: `/home/ligeng/CodexChat` (shared with the Slack gateway)
- Lark config root: `/home/ligeng/.lark-codex-chat`
- **WeChat credentials: `/home/ligeng/.lark-codex-chat/weixin/accounts/`**
- Poll cursor: `.../weixin/accounts/<account-id>.sync.json`
- Context tokens: `.../weixin/accounts/<account-id>.context-tokens.json`
- Event log: `/home/ligeng/CodexChat/.weixin/gateway-events.jsonl`
- Process log: `/home/ligeng/CodexChat/.weixin/gateway.log`
- PID file: `/home/ligeng/CodexChat/.weixin/gateway.pid`
- Memory root: `/home/ligeng/CodexChat/.weixin/conversations`
- Agent sessions: `.../conversations/.state/sessions.json`

The Slack gateway keeps its own `.slack/` tree, so the two front ends share a
workspace but never share conversation memory, cursors, or session pins.

## Where state lives, and how it survives

This is the part that matters for not losing a binding.

`lark` **refuses to start** when `LARK_CONFIG_DIR` is unset — it does not fall
back to a default directory. That is deliberate: it means credentials can never
be silently written somewhere the gateway will not look. The corollary is that
every invocation must set the same value, which is the only job of the two
wrapper scripts:

```bash
export LARK_CONFIG_DIR="$HOME/.lark-codex-chat"
```

Binding through `codex-weixin-login.sh` and starting through
`codex-weixin-gateway.sh` therefore cannot disagree. Running a bare
`lark weixin login` with a different `LARK_CONFIG_DIR` is the one way to end up
with an account the gateway cannot find.

What is written, and when:

| File | Written | Durability |
|---|---|---|
| `weixin/accounts/<id>.json` | on `confirmed` at login | temp file + rename, mode `0600` |
| `weixin/accounts.json` | on `confirmed` at login | index of bound accounts |
| `<id>.sync.json` | after every successful poll | temp file + rename, mode `0600` |
| `<id>.context-tokens.json` | on every inbound message | restored on gateway start |

Nothing is written on a failed or abandoned login, so an interrupted scan leaves
no half-bound state behind.

The poll cursor (`get_updates_buf`) is the server's only de-duplication
mechanism. Losing it replays or skips messages, which is why it is written
atomically rather than in place. The same file tracks the last processed `seq`,
so a crash between "poll succeeded" and "message handled" cannot re-run an agent
task.

### Back up the binding

The account file is the only thing that cannot be regenerated without another
phone scan:

```bash
tar czf ~/weixin-credentials-$(date +%Y%m%d).tgz -C "$HOME/.lark-codex-chat" weixin
```

Restore by extracting it back into `$HOME/.lark-codex-chat`. Treat the archive
as a secret: it contains a bot token.

## Configuration

Settings live in `~/.lark-codex-chat/config.yaml` under `weixin:` — in a file,
not in the startup script's environment, so they survive a script edit:

```yaml
weixin:
  gateway:
    event_log: "/home/ligeng/CodexChat/.weixin/gateway-events.jsonl"
    allow_from: []
    typing: true
  memory:
    enabled: true
    root: "/home/ligeng/CodexChat/.weixin/conversations"
  agent:
    enabled: true
    backend: "codex"
    workspace: "/home/ligeng/CodexChat"
    result_max_chars: 3500
    timeout_minutes: 20
    session_resume: false
```

**No token appears in `config.yaml` or in either script.** WeChat credentials
only ever live in the account store. This is a difference from the Slack
deployment, whose startup script carries `SLACK_APP_TOKEN` / `SLACK_BOT_TOKEN`
inline.

### Access control

`allow_from: []` means "fall back to the user id captured at QR login", so out
of the box only the WeChat account that scanned the code can reach the agent.
Everything else is dropped and logged.

This matters more than on Slack: a WeChat bot is reachable by anyone who can
message it, and every accepted message runs `codex` with `workspace-write` in
`/home/ligeng/CodexChat`. Setting `allow_from: ["*"]` accepts every sender.
The gateway refuses to start with neither an allow-list nor a bound user id.

## Build

Builds use Docker, not host Go:

```bash
cd /home/ligeng/Codes/lark-cli-codex-app
docker run --rm -v "$PWD:/work" -w /work golang:1.24 sh -c \
  'git config --global --add safe.directory /work && make build'
install -m 0755 ./lark "$HOME/.local/bin/lark"
```

Replacing the binary does not disturb a running gateway: the live process keeps
its open inode and only picks up the new build on restart. Restart both gateways
after installing if you want them on the same version.

## Bind the account

```bash
$HOME/bin/codex-weixin-login.sh
```

A QR code is printed, with a fallback link underneath. Scan it with the WeChat
mobile app and confirm; if WeChat shows a pairing number, type it at the prompt.
On success the script prints the bound `account_id` and `user_id` and lists what
landed on disk.

Re-running after a successful bind reports "already connected" and exits `0`.

```bash
$HOME/bin/codex-weixin-login.sh --verbose --timeout 15m
```

## Operations

Start or confirm the gateway:

```bash
$HOME/bin/codex-weixin-gateway.sh
```

It exits non-zero with instructions if no account is bound yet.

Follow process logs:

```bash
tail -f "$HOME/CodexChat/.weixin/gateway.log"
```

Follow structured event audit logs:

```bash
tail -f "$HOME/CodexChat/.weixin/gateway-events.jsonl"
```

Check the process:

```bash
pid="$(cat "$HOME/CodexChat/.weixin/gateway.pid")"
ps -p "$pid" -o pid,ppid,sid,stat,etime,cmd
```

Stop the gateway:

```bash
kill "$(cat "$HOME/CodexChat/.weixin/gateway.pid")"
```

Use `SIGTERM`, not `kill -9`: the gateway sends a `notifystop` to the service on
a clean shutdown.

Inspect or remove bindings:

```bash
LARK_CONFIG_DIR=$HOME/.lark-codex-chat $HOME/.local/bin/lark weixin accounts list
LARK_CONFIG_DIR=$HOME/.lark-codex-chat $HOME/.local/bin/lark weixin accounts remove <account-id>
```

`accounts remove` clears local state only; it does not unbind on the WeChat side.

## Smoke Test

1. Bind with `$HOME/bin/codex-weixin-login.sh` and scan the QR.
2. Start the gateway with `$HOME/bin/codex-weixin-gateway.sh`.
3. Send the bot a WeChat DM.
4. Confirm `gateway-events.jsonl` records the inbound event with
   `"provider":"weixin"`.
5. Confirm `<account-id>.context-tokens.json` gained an entry for your user id.
6. Confirm `<account-id>.sync.json` has a non-empty `get_updates_buf`.
7. Confirm WeChat receives the reply.

Then check the chat commands: `/status` should report the backend, session id,
and workspace; `/reset` should drop the session.

## Notes

- The transport is an HTTP long poll, so no public HTTPS callback URL is
  required — the same property that makes the Slack Socket Mode setup local-only.
- WeChat has no threads. A conversation is one long agent session; `/reset` is
  what closes it, playing the role Slack's ✅ reaction plays there.
- Voice messages rely on the server-side transcription. SILK audio is not
  decoded, so an untranscribed voice message gets a "not supported" notice.
- `gateway-events.jsonl` and the conversation memory files contain raw WeChat
  payloads and message text; treat them as private.
- If the log reports errcode `-14`, the bot token is stale: all requests pause
  for one hour, and re-binding with `codex-weixin-login.sh` is the fix.
