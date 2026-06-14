# Codex Slack Gateway Deployment

This deployment runs the `lark` Slack Socket Mode gateway as a local background
process for the generic Codex Chat Slack app. Slack sends DMs and channel
mentions to the gateway, the gateway dispatches work to `codex exec`, and
replies are posted back to the originating Slack thread.

## Current Layout

- Repository: `/home/ligeng/Codes/lark-cli-codex-app`
- Binary: `/home/ligeng/.local/bin/lark`
- Startup script: `/home/ligeng/bin/codex-chat-gateway.sh`
- Codex workspace: `/home/ligeng/CodexChat`
- Lark config root: `/home/ligeng/.lark-codex-chat`
- Slack event log: `/home/ligeng/CodexChat/.slack/gateway-events.jsonl`
- Process log: `/home/ligeng/CodexChat/.slack/gateway.log`
- PID file: `/home/ligeng/CodexChat/.slack/gateway.pid`
- Memory root: `/home/ligeng/CodexChat/.slack/conversations`
- Recover state: `/home/ligeng/CodexChat/.slack/conversations/.state/recover-state.json`

## Build

Builds use Docker, not host Go:

```bash
cd /home/ligeng/Codes/lark-cli-codex-app
docker run --rm -v "$PWD:/work" -w /work golang:1.24 sh -c \
  'git config --global --add safe.directory /work && go build -ldflags "-s -w" -o ./lark ./cmd/lark'
install -m 0755 ./lark "$HOME/.local/bin/lark"
```

## Startup Script Behavior

`/home/ligeng/bin/codex-chat-gateway.sh` exports the Slack tokens and local
paths, then starts:

```bash
/home/ligeng/.local/bin/lark slack gateway serve \
  --agent \
  --memory \
  --memory-root "$HOME/CodexChat/.slack/conversations" \
  --agent-workspace "$HOME/CodexChat"
```

The script detaches the gateway with `nohup setsid -f`, writes the real `lark`
PID to `gateway.pid`, and pipes stdout/stderr through `tee` into `gateway.log`.
If the PID file points at a live process, rerunning the script reports the
existing process instead of starting a duplicate Socket Mode client.

## Operations

Start or confirm the gateway:

```bash
$HOME/bin/codex-chat-gateway.sh
```

Follow process logs:

```bash
tail -f "$HOME/CodexChat/.slack/gateway.log"
```

Follow structured Slack event audit logs:

```bash
tail -f "$HOME/CodexChat/.slack/gateway-events.jsonl"
```

Check the process:

```bash
pid="$(cat "$HOME/CodexChat/.slack/gateway.pid")"
ps -p "$pid" -o pid,ppid,sid,stat,etime,cmd
```

Stop the gateway:

```bash
kill "$(cat "$HOME/CodexChat/.slack/gateway.pid")"
```

If it does not stop cleanly, verify the PID still belongs to this gateway before
using a stronger signal.

Recover mode defaults to `thread`, which is the intended mode for dedicated
Codex Chat threads. The gateway records known participating threads in
`/home/ligeng/CodexChat/.slack/conversations/.state/recover-state.json` and
uses `conversations.replies` to catch up missed messages on startup or Socket
Mode reconnect. In known participating threads, new user replies can continue
the Codex task flow without mentioning the bot.

The gateway adds an `eyes` processing reaction by default while Codex handles a
Slack message. Configure it with `--processing-reaction` or
`slack.gateway.processing_reaction`; set it to an empty value to disable
processing reactions.

## Smoke Test

1. Start the gateway with `$HOME/bin/codex-chat-gateway.sh`.
2. Send a DM to the Codex Chat Slack app or mention it in an invited channel.
3. Confirm `gateway-events.jsonl` records the inbound Slack event.
4. Confirm the thread file under `.slack/conversations/.../threads/...` records
   both inbound and outbound entries.
5. Confirm Slack receives the final reply.

The first successful local smoke test received an `app_mention` event and
recorded an outbound Codex reply in the thread audit log.

## Notes

- Socket Mode uses an outbound WebSocket connection, so no public HTTPS callback
  URL is required.
- `gateway.log` is for process diagnostics and includes startup JSON, gateway
  errors, agent dispatch errors, and wrapper exit status.
- `gateway-events.jsonl` and conversation thread logs include raw Slack payloads
  and should be treated as private.
- The gateway starts Codex in `/home/ligeng/CodexChat`; Slack requests should
  mention a specific repository or path when the task is project-specific.
- Plain channel messages outside known participating threads are not scanned;
  mention the bot to start participation in a channel thread, or use a DM.
