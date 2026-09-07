# Weixin (WeChat) Gateway

The `lark weixin` commands add a WeChat front end alongside the Slack and Lark
ones. A user chats with a bot in WeChat, the gateway dispatches each message to
a local agent CLI (`codex`, `agy`, or `grok`), and the result is sent back into
the conversation.

Unlike Slack Socket Mode, the WeChat transport is an HTTP **long poll**: the
client repeatedly calls `getupdates` on Tencent's iLink service. There is no
inbound webhook, no WebSocket, and no public HTTPS URL to expose.

---

## ⚠️ Security: read this before binding an account

The gateway executes an agent with `workspace-write` on your machine for every
message it accepts, and a WeChat bot is reachable by anyone who can message it.
The containment story is:

1. **`weixin.gateway.allow_from`** — the sender allow-list. It defaults to the
   user ID captured when you scanned the QR code, so out of the box only you can
   reach the agent. Messages from anyone else are dropped and logged. Setting it
   to `["*"]` accepts every sender; do that only if you understand that any
   stranger can then run code in your workspace.
2. **`weixin.agent.workspace`** — the directory the agent is allowed to write.
   Point it at a scratch workspace, not your home directory.
3. **`weixin.agent.timeout_minutes`** and **`result_max_chars`** — bound how long
   a single task can run and how much output is echoed back.

The gateway refuses to start when it has neither a configured allow-list nor a
login user ID, rather than accepting everyone by default.

---

## Binding an account

```bash
lark weixin login
```

A QR code is printed in the terminal, with the raw link underneath as a
fallback. Scan it with the WeChat mobile app and confirm. If WeChat asks for a
pairing code, type the digits shown on your phone.

On success the credentials are written to `<config dir>/weixin/accounts/` at
mode `0600`, the account is added to `<config dir>/weixin/accounts.json`, and
the bound `account_id` and `user_id` are printed.

Re-running `lark weixin login` after a successful bind is expected: the server
recognizes the client and reports "already connected", which exits `0`.

```bash
lark weixin login --verbose --timeout 10m   # print poll progress, longer deadline
lark weixin accounts list
lark weixin accounts remove <account-id>    # drops credentials, cursor, and tokens
```

`accounts remove` only clears local state; it does not unbind the account on the
WeChat side.

---

## Running the gateway

```bash
lark weixin gateway serve --agent --agent-workspace ~/WorkSpace --memory
```

Useful flags (all have `weixin.*` config equivalents):

| Flag | Meaning |
|---|---|
| `--account` | account id to serve; empty uses the most recently registered one |
| `--agent` | dispatch inbound messages to the local agent |
| `--agent-backend` | `codex`, `agy`, or `grok` |
| `--agent-binary` | override the backend binary |
| `--agent-workspace` | workspace root for agent tasks |
| `--allow-from` | sender allow-list; overrides config. `*` accepts everyone |
| `--memory` / `--memory-root` | persist conversation memory and audit files |
| `--event-log` | JSONL path for received events |
| `--typing` | show the WeChat typing indicator during a run (default on) |
| `--media` | download and decrypt inbound image/file/video attachments |
| `--desktop-worker` | run the desktop GUI task worker in-process |

The gateway announces itself with `notifystart` on boot and `notifystop` on
shutdown, so stop it with `SIGINT`/`SIGTERM` (Ctrl-C) rather than `SIGKILL`.

---

## Chat commands

| Command | Effect |
|---|---|
| `/reset` | drop the stored agent session for this conversation; the next message starts fresh |
| `/status` | report the active backend, session id, workspace, and whether the agent is enabled |
| `/echo <text>` | reply with the text, bypassing the agent |
| `/codex …`, `/agy …`, `/grok …` | run this message on that backend and pin it for the conversation |
| `/gui …` | queue a desktop GUI task instead of an agent run |

WeChat has no threads, so a conversation is one long session. `/reset` plays the
role Slack's ✅ reaction plays there.

---

## Conversation and session model

| Concern | Value |
|---|---|
| `provider` | `weixin` |
| `team_id` | the account id |
| `channel_id` | the sender's Weixin user id |
| `thread_id` | the server `session_id`, falling back to the user id |
| `channel_type` | `direct` |

Because `thread_id` is stable per conversation, `agent.SessionKey` resolves to
one session per chat, which is what makes `--agent-session-resume` and the
sticky `/backend` pin work.

---

## Media

**Inbound** (`--media`): images, files, and videos are downloaded from the CDN,
decrypted (AES-128-ECB), and saved under `<config dir>/weixin/media/inbound/`.
Each saved path is appended to the prompt as `[附件: /path/to/file]` so the agent
can open it. Attachments are capped at 100 MB.

**Voice** messages use the server-side transcription in `voice_item.text`. SILK
audio is *not* decoded — there is no comparable Go library — so a voice message
that arrives without a transcription gets a "not supported" notice instead.

**Outbound**: an agent reply may include a line of the form

```
MEDIA:/absolute/path/to/file.png
```

which uploads that file and sends it as an attachment; the remaining text
becomes its caption. Only absolute paths are honored. You can also send one
manually:

```bash
lark weixin msg send --to <user-id> --text "here you go" --media ./report.pdf
```

`msg send` needs a conversation context token, which is only recorded when the
recipient has messaged the bot while a gateway was running.

---

## Configuration reference

Everything lives under the `weixin:` key in `config.yaml`; see
`config.example.yaml` for the annotated version. Each key also has a `WEIXIN_*`
environment variable.

| Key | Default | Notes |
|---|---|---|
| `base_url` | `https://ilinkai.weixin.qq.com` | overridden per account by the `baseurl` issued at login |
| `cdn_base_url` | `https://novac2c.cdn.weixin.qq.com/c2c` | media transfer host |
| `app_id` | `bot` | `iLink-App-Id` header |
| `bot_type` | `3` | QR login query parameter |
| `bot_agent` | `lark-cli/<version>` | UA-style identity, observability only |
| `route_tag` | *(empty)* | optional `SKRouteTag` header |
| `account_id` | *(empty)* | account to serve; empty picks the newest |
| `gateway.allow_from` | *(empty → login user)* | sender allow-list |
| `gateway.long_poll_timeout_seconds` | `35` | the server may lower this per response |
| `gateway.typing` | `true` | typing indicator during agent runs |
| `gateway.event_log` | `.weixin/gateway-events.jsonl` | received events |
| `gateway.auto_reply_text` | *(empty)* | static template; supports `{{text}}`, `{{user_id}}`, … |
| `memory.*` | same defaults as Slack | conversation memory and audit files |
| `agent.*` | same defaults as Slack | backend, binary, workspace, timeout, session resume |

`app_id`, `bot_type`, and the client version header are undocumented but fixed
by the official Tencent plugin. They are exposed as configuration so a
server-side change can be absorbed without rebuilding.

---

## Durability

The poll cursor (`get_updates_buf`) is the server's only de-duplication
mechanism, so it is written durably (temp file plus rename, mode `0600`) to
`<config dir>/weixin/accounts/<id>.sync.json` after every successful poll.
The same file also tracks the last processed `seq`, which makes a crash between
"poll succeeded" and "message handled" non-duplicating rather than re-running an
agent task.

Context tokens live in `<id>.context-tokens.json` and are restored on startup,
so replies still carry the right conversation reference after a restart.

If the server reports errcode `-14` (stale token), the gateway pauses **all**
requests for that account for one hour and logs the reason. Re-run
`lark weixin login` to re-bind.

---

## Running as a service

A systemd user unit (Linux):

```ini
# ~/.config/systemd/user/lark-weixin-gateway.service
[Unit]
Description=lark Weixin gateway
After=network-online.target

[Service]
Type=simple
Environment=LARK_CONFIG_DIR=%h/.lark-codex-chat
ExecStart=%h/.local/bin/lark weixin gateway serve --agent --memory --agent-workspace %h/CodexChat
Restart=on-failure
RestartSec=10
# Give the gateway time to send its notifystop on shutdown.
KillSignal=SIGTERM
TimeoutStopSec=30

[Install]
WantedBy=default.target
```

```bash
systemctl --user daemon-reload
systemctl --user enable --now lark-weixin-gateway
journalctl --user -u lark-weixin-gateway -f
```

On macOS, copy `launchd/com.local.lark-cli-codex-app.weixin-gateway.plist.example`
into `~/Library/LaunchAgents/`, replace `YOUR_USERNAME`, and
`launchctl load` it.

---

## Testing

Unit tests run entirely against `httptest`; no test contacts
`ilinkai.weixin.qq.com`.

```bash
docker run --rm -v "$PWD:/work" -w /work golang:1.24 go test ./internal/weixin
```

An integration test that drives the real service is gated twice — the
`integration` build tag *and* `WEIXIN_INTEGRATION_TEST=1` — so the normal suite
never compiles it. See the header of
`internal/weixin/weixin_integration_test.go`.
