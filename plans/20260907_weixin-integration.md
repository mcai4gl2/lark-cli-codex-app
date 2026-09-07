# Weixin (WeChat) Front-End Integration Study & Plan

**Date:** 2026-09-07
**Goal:** Add a Weixin (WeChat) chat front end to `lark-cli-codex-app`, feature-comparable to the existing Slack integration: a user chats with a bot in WeChat, messages are dispatched to a local `codex` (or `agy` / `grok`) CLI backend, and the result is sent back into the WeChat conversation.

**Study source:** `~/Codes/openclaw-weixin` (`@tencent-weixin/openclaw-weixin` v2.4.8, TypeScript, OpenClaw channel plugin).
**Target:** this repo (`github.com/yjwong/lark-cli`, Go 1.24).

---

## Part 1 — Study: how the OpenClaw Weixin plugin works

### 1.1 Shape of the plugin

It is an OpenClaw *channel plugin*: `index.ts` registers `weixinPlugin` (`src/channel.ts`), which supplies `auth` (QR login), `gateway` (start/stop an account), `outbound` (sendText / sendMedia), `config` (multi-account resolution) and `status` hooks. OpenClaw core supplies the agent/LLM pipeline; the plugin only owns the WeChat transport.

Relevant consequence for us: **only the transport half is reusable.** Everything the plugin delegates to `ctx.channelRuntime` (routing, sessions, reply dispatch, media store, command auth) already has an equivalent in this repo (`internal/inbound`, `internal/agent`, `internal/slackmemory`, `internal/desktop`).

### 1.2 Transport model: HTTP long-poll, not a webhook or socket

The channel talks to a Tencent gateway (`https://ilinkai.weixin.qq.com`) over plain HTTP JSON. There is **no** inbound webhook and no WebSocket — the client long-polls.

| Endpoint (POST, JSON)              | Purpose |
|------------------------------------|---------|
| `ilink/bot/getupdates`             | Long-poll for new messages; returns `msgs[]` + a new `get_updates_buf` cursor |
| `ilink/bot/sendmessage`            | Send one message (text / image / video / file) |
| `ilink/bot/getuploadurl`           | Pre-signed CDN upload params for outbound media |
| `ilink/bot/getconfig`              | Per-user config; source of `typing_ticket` |
| `ilink/bot/sendtyping`             | Show / cancel the "typing" indicator |
| `ilink/bot/msg/notifystart`        | Announce client startup |
| `ilink/bot/msg/notifystop`         | Announce client shutdown |
| `ilink/bot/get_bot_qrcode` (POST)  | Login: fetch QR code |
| `ilink/bot/get_qrcode_status` (GET)| Login: long-poll QR scan status |

Common headers on every call (`src/api/api.ts:buildHeaders`):

```
Content-Type: application/json
AuthorizationType: ilink_bot_token
Authorization: Bearer <bot_token>
X-WECHAT-UIN: base64(decimal string of a random uint32)
iLink-App-Id: <package.json "ilink_appid">          # literally "bot" in this plugin
iLink-App-ClientVersion: <(major<<16)|(minor<<8)|patch>
SKRouteTag: <optional, from config routeTag>
```

Every request body also carries `base_info: { channel_version, bot_agent }`, where `bot_agent` is a UA-style `Name/Version` string used only for server-side observability (`sanitizeBotAgent`).

### 1.3 Long-poll loop (`src/monitor/monitor.ts`)

```
loop until abort:
  resp = POST getupdates { get_updates_buf }        # timeout 35s default
  if resp.longpolling_timeout_ms > 0: next timeout = that value
  if resp.ret != 0 or resp.errcode != 0:
      if errcode == -14 (stale token): pause this account for 1h, continue
      else: failures++; 3 consecutive failures -> sleep 30s, else sleep 2s; continue
  failures = 0
  if resp.get_updates_buf != "": persist it to disk, use it next round
  for msg in resp.msgs: processOneMessage(msg)
```

Key properties:
- **Client-side timeout is normal control flow.** An `AbortError` from the fetch returns `{ret:0,msgs:[]}` so the loop just re-polls.
- The **cursor `get_updates_buf` is the only de-dup mechanism**, persisted per account at `~/.openclaw/openclaw-weixin/accounts/<accountId>.sync.json`. Losing it replays or skips messages.
- An external `abortSignal` cancels the in-flight poll so channel stop/hot-reload is immediate.
- `errcode -14` (stale token) triggers a process-wide one-hour pause for that account (`src/api/session-guard.ts`), and every outbound call calls `assertSessionActive()` first.

### 1.4 Message model (`src/api/types.ts`)

`WeixinMessage` — `seq`, `message_id`, `from_user_id`, `to_user_id`, `session_id`, `create_time_ms`, `message_type` (1=USER, 2=BOT), `message_state` (0/1/2), `context_token`, `run_id`, and `item_list: MessageItem[]`.

`MessageItem.type`: `1 TEXT, 2 IMAGE, 3 VOICE, 4 FILE, 5 VIDEO, 11 TOOL_CALL_START, 12 TOOL_CALL_RESULT`. Text lives at `text_item.text`; a quoted message arrives as `ref_msg`. **Voice messages carry server-side ASR text at `voice_item.text`** — the plugin uses it directly when present and only downloads SILK audio otherwise.

Two things have no analogue in Slack/Lark and drive most of the design:

1. **`context_token`** — issued per inbound message and **must be echoed on every outbound send** for that user. The plugin keeps an in-memory `accountId:userId -> token` map, persisted to `<accountId>.context-tokens.json`, restored on startup (`src/messaging/inbound.ts`). Sends without it are attempted but logged as a warning.
2. **No threads.** `capabilities.chatTypes = ["direct"]` — WeChat DMs only. `From === To === from_user_id`; there is no thread id to key a session on.

Outbound (`src/messaging/send.ts`): one `item_list` entry per request — a caption is a *separate* TEXT message sent before the media item. `message_type: BOT`, `message_state: FINISH`, `client_id` is a locally generated id returned as the "messageId". Text chunk limit is 4000 chars, and markdown is stripped by a 373-line `StreamingMarkdownFilter` because WeChat renders plain text only.

### 1.5 Auth: QR login (`src/auth/login-qr.ts`)

```
POST get_bot_qrcode?bot_type=3   { local_token_list: [...up to 10 existing tokens] }
  -> { qrcode, qrcode_img_content }      # render qrcode_img_content in terminal
loop until deadline (default 480s):
  GET get_qrcode_status?qrcode=...[&verify_code=...]     # long-poll, 35s
    wait                -> keep polling
    scaned              -> print "verifying"
    need_verifycode     -> read digits from stdin, resend with verify_code
    verify_code_blocked -> refresh QR (max 3 refreshes total)
    expired             -> refresh QR (max 3)
    scaned_but_redirect -> switch polling host to redirect_host (IDC redirect)
    binded_redirect     -> already bound to this client; success, no new creds
    confirmed           -> { bot_token, ilink_bot_id, baseurl, ilink_user_id }
```

On `confirmed` the plugin normalizes `ilink_bot_id` (`hex@im.bot` → `hex-im-bot`) into a filesystem-safe account id, writes `{token, baseUrl, userId}` to `accounts/<id>.json`, appends the id to `accounts.json`, and clears stale accounts that share the same `userId`.

Note the QR endpoints always use the **fixed** host `https://ilinkai.weixin.qq.com`, while `getupdates`/`sendmessage` use the per-account `baseUrl` returned at login (may differ after an IDC redirect).

### 1.6 Authorization (who may talk to the bot)

`processOneMessage` runs every inbound message through the OpenClaw pairing pipeline (`dmPolicy: "pairing"`), whose allow-list is `<credentials>/openclaw-weixin-<accountId>-allowFrom.json`, falling back to the `userId` captured at QR login. Unauthorized senders are **dropped silently**. This matters: a WeChat bot is reachable by anyone who can message it, so an allow-list is not optional for a gateway that runs `codex` with `workspace-write`.

### 1.7 Media

Inbound (`src/media/media-download.ts`, `src/cdn/pic-decrypt.ts`):
`item.*.media.full_url` (or `cdnBaseUrl + /download?encrypted_query_param=…`) → fetch bytes → **AES-128-ECB decrypt** with a key from `aes_key`. `aes_key` has two encodings in the wild: base64 of 16 raw bytes (images), or base64 of a 32-char hex string (file/voice/video). Voice is SILK → transcoded via `silk-wasm` (only needed when `voice_item.text` is absent).

Outbound (`src/cdn/upload.ts`, `src/cdn/cdn-upload.ts`):
read file → md5 + plaintext size → random 16-byte `filekey` and 16-byte `aeskey` → `getuploadurl` (`filesize` = AES/PKCS7-padded size) → POST ciphertext to `upload_full_url` as `application/octet-stream` → CDN returns the download param in the **`x-encrypted-param` response header** → reference it in the outbound `ImageItem`/`VideoItem`/`FileItem` with `aes_key` base64 and `encrypt_type: 1`. Retries 3× on 5xx, aborts on 4xx.

### 1.8 Other behaviors worth copying

- **Typing indicator**: `getconfig` yields a per-user `typing_ticket` (cached 24h with random refresh and exponential backoff on failure, `src/api/config-cache.ts`); `sendtyping` with `status:1/2` is driven by start/stop callbacks around the agent run, with a 5s keepalive.
- **Slash commands** handled before the AI pipeline (`/echo`, `/toggle-debug`) — a plain prefix check on the text body.
- **Error notices**: send failures produce a user-visible `⚠️ …` message rather than a silent drop.
- **Redaction**: tokens/URLs are redacted in all logs (`src/util/redact.ts`).

---

## Part 2 — Mapping onto this repo

The Slack integration already defines the seam we need. `internal/platform` is provider-neutral, and everything downstream of it is reusable as-is:

| Concern | Slack today | Weixin equivalent |
|---|---|---|
| Transport | Socket Mode WS (`internal/slack/gateway.go`) | HTTP long-poll loop (new `internal/weixin`) |
| Event normalize | `slack.NormalizeEvent` → `platform.MessageEvent` | `weixin.NormalizeMessage` (provider `"weixin"`) |
| Outbound | `slack.Client` implements `platform.Messenger` | `weixin.Messenger` (sendmessage + `context_token`) |
| Persist + auto-reply | `internal/inbound.Handler` | unchanged |
| Agent execution | `internal/agent.Runner` (codex/agy/grok, `/backend` directive, session pins) | unchanged |
| "working on it" signal | `ProcessingObserver` → emoji reaction | `ProcessingObserver` → `sendtyping` start/stop |
| Memory/context | `internal/slackmemory` (keyed off `MessageEvent`) | reusable as-is |
| Desktop tasks | `internal/desktop` | reusable as-is |
| CLI | `lark slack gateway serve` | `lark weixin gateway serve` |
| Config | `slack.*` viper keys | `weixin.*` viper keys |

**No changes to `internal/agent`, `internal/inbound`, `internal/desktop` or `internal/slackmemory` are required**, except adding `"weixin"` to `providerLabel()` in `internal/agent/codex.go` (currently returns "聊天平台" for unknown providers).

### 2.1 Design decisions (and why)

1. **Threading model.** WeChat has no threads, but `agent.SessionKey{Provider, ChannelID, ThreadTS}` drives both codex session resume and the sticky `/backend` pin. Map `ChannelID = from_user_id` and `ThreadID = session_id` (falling back to `from_user_id` when the server omits it). A conversation therefore keeps one codex session; add a `/reset` command to drop the `SessionRecord`, playing the role Slack's ✅-reaction close plays.
2. **`context_token` stays out of `platform.MessageEvent`.** The token store is keyed `(accountID, userID)` inside `internal/weixin`, written on inbound and read by the messenger — exactly the plugin's design. This keeps the shared struct provider-neutral. `RawEvent` still carries the full JSON for debugging.
3. **Allow-list is mandatory-by-default.** `weixin.gateway.allow_from` defaults to the `userId` recorded at QR login; non-matching senders are dropped and logged. Opt out only via an explicit `allow_from: ["*"]`.
4. **Voice v1 = ASR text only.** Use `voice_item.text` when present; do not port SILK decoding (no comparable Go library; `silk-wasm` is a Node/WASM dependency). Voice without text is acknowledged with a "voice not supported" notice.
5. **Media is phased.** Text-only end-to-end first (that is the whole Slack-parity story for a codex bot); inbound download/decrypt next; outbound CDN upload last. AES-128-ECB must be hand-rolled in Go (`crypto/aes` block loop + PKCS7) — stdlib has no ECB mode.
6. **Single account in v1, multi-account-shaped on disk.** Store under `~/.lark-cli/weixin/accounts/<id>.json` + `accounts.json` index (via `config.GetConfigDir()`), so a second account is a later change, not a migration.
7. **Markdown stripping is required, not optional.** Codex output is markdown-heavy; WeChat renders none of it. A modest Go stripper (fences → indented text, headings/emphasis/link syntax removed) is enough; do not port all 373 lines.

### 2.2 Risks / open questions

- **Protocol constants must be replicated faithfully.** `iLink-App-Id: bot`, `iLink-App-ClientVersion` (`major<<16|minor<<8|patch`), `AuthorizationType: ilink_bot_token`, and `bot_type=3` are undocumented but fixed by the official Tencent plugin, which is confirmed working in practice. Send exactly what the plugin sends; expose each as a config override so a server-side change can be absorbed without a rebuild. `channel_version` / `bot_agent` are observability-only and should identify this client (`lark-cli/<version>`).
- **Cursor loss = message loss/replay.** `get_updates_buf` must be written durably (temp file + rename, as `agent.SessionStore` does) after every successful poll.
- **No idempotency key from the server we currently use.** Consider tracking the last processed `seq`/`message_id` per account so a crash between "poll succeeded" and "cursor saved" cannot re-run a codex task.
- **Account safety.** This binds a real WeChat account to a machine that executes `codex` with `workspace-write`. The allow-list, `result_max_chars`, and the agent timeout are the containment story; state that in the docs.
- **Rate/abuse behavior of `sendmessage` is unknown** — chunked long replies may need pacing between chunks.

---

## Part 3 — Implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: use `superpowers:subagent-driven-development` or `superpowers:executing-plans`. Steps use checkbox (`- [ ]`) syntax.

### Global constraints

- Docker `golang:1.24` for **all** `gofmt`, `go test`, `go build` (per `CLAUDE.md`); never host `go`. Report verification as blocked if Docker is unavailable.
- Focused tests for touched packages first, then `go test ./...` before claiming completion.
- No behavior change to Slack or Lark paths. `internal/agent`, `internal/inbound`, `internal/desktop`, `internal/slackmemory` stay untouched apart from the `providerLabel` addition.
- Every network call is unit-tested against `httptest`; no test may hit `ilinkai.weixin.qq.com`.
- Tokens, `context_token`, and QR codes are redacted in every log line.

### Phase 0 — Skeleton, types, config

- [ ] Create `internal/weixin/types.go`: `WeixinMessage`, `MessageItem`, `TextItem`, `ImageItem`, `VoiceItem`, `FileItem`, `VideoItem`, `RefMessage`, `CDNMedia`, `BaseInfo`, request/response structs, and the `MessageItemType` / `MessageType` / `MessageState` / `TypingStatus` / `UploadMediaType` constants (mirror `src/api/types.ts`).
- [ ] Add `weixin.*` keys to `internal/config/config.go` mirroring the `slack.*` block: `base_url`, `cdn_base_url`, `app_id` (default `bot`), `bot_type` (default `3`), `bot_agent`, `route_tag`, `gateway.{event_log, allow_from, long_poll_timeout_seconds, typing, auto_reply_text}`, `agent.{enabled, backend, binary, args, codex_binary, grok_binary, workspace, model, ack_text, result_max_chars, timeout_minutes, session_resume}`, `memory.*` (same defaults as Slack). Bind `WEIXIN_*` env vars.
- [ ] Extend `config.example.yaml` with a documented `weixin:` block.
- [ ] Add `"weixin"` → `"微信"` to `providerLabel()` in `internal/agent/codex.go` + test.
- [ ] Verify: `gofmt`, `go test ./internal/config ./internal/agent`.

### Phase 1 — API client + account store + QR login

- [ ] `internal/weixin/client.go`: `Client` with `BaseURL`, `Token`, `http.Client`; `postJSON`/`getRaw` helpers that build the common headers (incl. `X-WECHAT-UIN` = base64 of a random uint32's decimal string), inject `base_info`, honor `context.Context`, and classify network errors (dns/tcp/tls/timeout) for logging. Methods: `GetUpdates`, `SendMessage`, `GetConfig`, `SendTyping`, `NotifyStart`, `NotifyStop`, `GetUploadURL`.
- [ ] `internal/weixin/accounts.go`: account record `{AccountID, Token, BaseURL, CDNBaseURL, UserID, SavedAt}`; load/save/list/remove under `<config dir>/weixin/`; `normalizeAccountID` (`hex@im.bot` → `hex-im-bot`) and its reverse for compat.
- [ ] `internal/weixin/syncbuf.go`: durable `get_updates_buf` load/save (temp file + `os.Rename`, 0600).
- [ ] **QR registration** (`internal/weixin/login.go` + `qrterm.go`) — full spec in [Appendix B](#appendix-b--qr-registration-spec). `StartQRLogin`, `WaitForLogin`, `DisplayQRCode`, plus `RunInteractiveLogin` tying them together.
- [ ] Add a terminal QR dependency (`github.com/mdp/qrterminal/v3`, the direct analogue of the plugin's `qrcode-terminal`); `go mod tidy` inside Docker. Always print the raw URL underneath as a fallback.
- [ ] `internal/cmd/weixin.go`: `lark weixin login [--force] [--verbose] [--timeout 480s]` (QR flow, persists the account, prints the bound `account_id` / `user_id`), `lark weixin accounts list|remove`. Register `weixinCmd` in `internal/cmd/root.go`.
- [ ] Tests: `httptest` coverage for header construction, `base_info`, each endpoint's happy path and `ret != 0`; the QR state machine table-driven over status sequences (see Appendix B); account/syncbuf round-trips.
- [ ] Verify: `gofmt`, `go test ./internal/weixin ./internal/cmd ./internal/config`.
- [ ] **Manual smoke:** `lark weixin login` against the real service — scan, confirm, and check that `accounts/<id>.json` holds a token and the bound `user_id`. End-to-end send is exercised once Phase 2 adds `lark weixin msg send`.

### Phase 2 — Gateway: long-poll → normalize → agent → reply (text only)

- [ ] `internal/weixin/context_token.go`: in-memory `(accountID,userID) -> token` map with disk persistence (`<config dir>/weixin/accounts/<id>.context-tokens.json`) and restore-on-start.
- [ ] `internal/weixin/events.go`: `NormalizeMessage(msg, accountID) (platform.MessageEvent, bool)` — skip `message_type == BOT` and empty bodies; body from the first TEXT item (with `[引用: …]` prefix for `ref_msg`) or `voice_item.text`; `Provider="weixin"`, `TeamID=accountID`, `ChannelID=from_user_id`, `ThreadID=session_id||from_user_id`, `MessageID=message_id`, `ChannelType="direct"`, `RawEvent` = raw JSON.
- [ ] `internal/weixin/messenger.go`: implements `platform.Messenger`; looks up the `context_token`, strips markdown, splits at 4000 chars (rune-safe, prefer newline boundaries), sends each chunk as its own `sendmessage` with a fresh `client_id`.
- [ ] `internal/weixin/markdown.go`: markdown → plain text (fences, inline code, headings, emphasis, links, list bullets).
- [ ] `internal/weixin/sessionguard.go`: stale-token (`errcode -14`) pause with a 1h window; consulted before every outbound call.
- [ ] `internal/weixin/gateway.go`: `Config`/`Gateway`/`NewGateway`/`Serve` shaped after `internal/slack/gateway.go`. Serve: `NotifyStart` → restore tokens → poll loop (35s default, honor `longpolling_timeout_ms`, 3-strike 30s backoff / 2s retry, stale-token pause, persist cursor) → per message: allow-list check → `NormalizeMessage` → `handler.Process` → memory `RecordInbound` → `desktop.ExtractRequest` branch → `agent.Dispatch`; `NotifyStop` on shutdown (with a fresh context, since the run context is already canceled).
- [ ] Allow-list: `weixin.gateway.allow_from`, defaulting to the login `userId`; drop + log unauthorized senders.
- [ ] Wire `agent.SessionStore` (for `/backend` pins and codex resume) at `<memory root>/.state/sessions.json`, mirroring Slack.
- [ ] `lark weixin gateway serve` command with the Slack command's flag surface (`--agent`, `--agent-backend`, `--agent-workspace`, `--event-log`, `--memory`, …) and `agent.ValidateDefaultBackend`.
- [ ] `lark weixin msg send --to <user_id> --text …` for manual testing.
- [ ] Tests: normalize table tests; messenger chunking/markdown; a fake-server gateway test proving poll → dispatch → reply and cursor persistence; backoff and stale-token paths; allow-list filtering.
- [ ] Verify: `gofmt`, `go test ./internal/weixin ./internal/cmd`, then `go test ./...`.

### Phase 3 — Presence, commands, robustness

- [ ] `internal/weixin/configcache.go`: per-user `typing_ticket` cache (24h randomized TTL, exponential backoff to 1h on failure).
- [ ] `typingObserver` implementing `agent.ProcessingObserver`: `sendtyping` start with a 5s keepalive ticker, cancel on finish; enabled by `weixin.gateway.typing`.
- [ ] Slash commands handled before the agent: `/reset` (drop the `SessionRecord` for this conversation), `/status` (backend, session id, workspace), `/echo`. Leave `/codex`, `/agy`, `/grok` to the existing `agent.ParseBackendDirective`.
- [ ] User-visible error notices on send/agent failure (`⚠️ …`), matching `error-notice.ts`.
- [ ] Last-processed `seq`/`message_id` per account to make a crash between poll and cursor-save non-duplicating.
- [ ] Log redaction helpers for token / context_token / QR / URLs.
- [ ] Verify: focused tests + `go test ./...`.

### Phase 4 — Inbound media

- [ ] `internal/weixin/crypto.go`: AES-128-ECB encrypt/decrypt with PKCS7 (hand-rolled block loop), `aesEcbPaddedSize`, and `parseAESKey` handling both encodings (16 raw bytes, or 32 ASCII hex chars).
- [ ] `internal/weixin/media_download.go`: resolve `full_url` or build `<cdnBaseUrl>/download?encrypted_query_param=…`, fetch, decrypt, size-cap (100MB), save under `<config dir>/weixin/media/inbound/`; guess MIME from `file_name`.
- [ ] Attach the saved path to the prompt as a line (`[附件: /path/to/file]`) so codex can open it; images/files/videos supported, voice falls back to `voice_item.text`.
- [ ] Tests with synthetic ciphertext fixtures for both key encodings; a golden test for `aesEcbPaddedSize`.
- [ ] Verify: focused tests + `go test ./...`.

### Phase 5 — Outbound media

- [ ] `internal/weixin/media_upload.go`: read → md5 → random filekey/aeskey → `GetUploadURL` → POST ciphertext (`application/octet-stream`, 3 retries on 5xx, abort on 4xx) → read `x-encrypted-param` → build `ImageItem`/`VideoItem`/`FileItem` (`encrypt_type: 1`, base64 `aes_key`, correct size fields).
- [ ] Caption sent as a separate TEXT item *before* the media item (one item per request).
- [ ] Outbound `MEDIA:<abs path>` directive in agent output (matching the plugin's convention), plus `lark weixin msg send --media`.
- [ ] Tests against an `httptest` CDN returning `x-encrypted-param`.
- [ ] Verify: focused tests + `go test ./...`.

### Phase 6 — Docs, ops, integration test

- [ ] `docs/` page + `README.md`/`USAGE.md` sections: QR login, allow-list, config reference, `/reset` + `/backend` directives, security warning about `workspace-write`.
- [ ] `launchd`/service snippet for `lark weixin gateway serve`, mirroring the Slack setup.
- [ ] `internal/weixin/weixin_integration_test.go` behind the `integration` build tag **and** `WEIXIN_INTEGRATION_TEST=1`, following the documented exception pattern in `CLAUDE.md` (compile in Docker, run on the host against real credentials; never mutate stored accounts).
- [ ] Final: `docker run --rm -v "$PWD:/work" -w /work golang:1.24 go test ./...` and a Docker `make build`.

---

## Appendix A — file-by-file correspondence

| openclaw-weixin (TS) | this repo (Go, planned) |
|---|---|
| `src/api/types.ts` | `internal/weixin/types.go` |
| `src/api/api.ts` | `internal/weixin/client.go` |
| `src/api/session-guard.ts` | `internal/weixin/sessionguard.go` |
| `src/api/config-cache.ts` | `internal/weixin/configcache.go` |
| `src/monitor/monitor.ts` | `internal/weixin/gateway.go` |
| `src/messaging/process-message.ts` | `internal/weixin/gateway.go` + `internal/inbound` + `internal/agent` |
| `src/messaging/inbound.ts` | `internal/weixin/events.go` + `context_token.go` |
| `src/messaging/send.ts` | `internal/weixin/messenger.go` |
| `src/messaging/markdown-filter.ts` | `internal/weixin/markdown.go` (reduced) |
| `src/messaging/slash-commands.ts` | `internal/weixin/commands.go` |
| `src/auth/login-qr.ts` | `internal/weixin/login.go` + `qrterm.go` (see Appendix B) |
| `src/auth/accounts.ts` | `internal/weixin/accounts.go` |
| `src/auth/pairing.ts` | `weixin.gateway.allow_from` config |
| `src/storage/sync-buf.ts` | `internal/weixin/syncbuf.go` |
| `src/cdn/aes-ecb.ts`, `pic-decrypt.ts` | `internal/weixin/crypto.go`, `media_download.go` |
| `src/cdn/upload.ts`, `cdn-upload.ts` | `internal/weixin/media_upload.go` |
| `src/media/silk-transcode.ts` | *not ported* — rely on `voice_item.text` |
| `src/channel.ts` (OpenClaw plugin surface) | `internal/cmd/weixin.go` |

---

## Appendix B — QR registration spec

Terminal-QR pairing is the only way to bind a WeChat account, so it is specified here in full. It reproduces `src/auth/login-qr.ts` exactly; deviations are called out.

### B.1 Endpoints

Both QR endpoints use the **fixed** host `https://ilinkai.weixin.qq.com`, *not* the per-account `baseUrl` (which only exists after login, and may differ once the server issues an IDC redirect).

```
POST ilink/bot/get_bot_qrcode?bot_type=3
  body: { "local_token_list": ["<token>", ...] }     # up to 10 existing tokens, newest first
  resp: { "qrcode": "<opaque id>", "qrcode_img_content": "<URL to encode>" }

GET  ilink/bot/get_qrcode_status?qrcode=<qrcode>[&verify_code=<digits>]
  long-poll, 35s client timeout
  resp: { "status": "...", "bot_token"?, "ilink_bot_id"?, "baseurl"?,
          "ilink_user_id"?, "redirect_host"? }
```

`local_token_list` lets the server recognize an already-bound client (it is what produces `binded_redirect`). Send the tokens of locally stored accounts, newest first, capped at 10. Headers are the common set from §1.2; these two calls carry **no** `Authorization` (the POST helper omits it when no token is set, and the GET helper sends only `iLink-App-Id` / `iLink-App-ClientVersion` / `SKRouteTag`).

### B.2 State machine

Poll every 1s (except `need_verifycode`, which re-polls immediately after reading input). Overall deadline 480s, configurable. QR refresh budget: 3.

| `status` | Action |
|---|---|
| `wait` | keep polling (print a dot in `--verbose`) |
| `scaned` | print "正在验证" once; clear any pending verify code (it was accepted) |
| `need_verifycode` | prompt on stdin (`输入手机微信显示的数字，以继续连接：`; on a repeat, `❌ 你输入的数字不匹配，请重新输入：`); stash the code and re-poll **immediately** with `&verify_code=` |
| `verify_code_blocked` | print "多次输入错误，请稍后再试"; clear the pending code; consume one refresh and re-issue the QR, or give up when the budget is spent |
| `expired` | consume one refresh, fetch a new QR, re-render, reset the "scanned" print flag; give up after 3 |
| `scaned_but_redirect` | switch the polling host to `https://<redirect_host>` and continue; if `redirect_host` is missing, log a warning and keep the current host |
| `binded_redirect` | this client is already bound to the scanned bot — **success**, no new credentials; keep existing ones and exit 0 (a re-run must not look like a failure) |
| `confirmed` | require `ilink_bot_id`, else fail; persist and finish |

Network/gateway errors while polling (including Cloudflare 524 and client-side aborts) are treated as `wait` and retried — only a hard failure of the *login session* aborts.

### B.3 On `confirmed`

1. Normalize `ilink_bot_id` into a filesystem-safe account id: `hex@im.bot` → `hex-im-bot`, `hex@im.wechat` → `hex-im-wechat` (keep the reverse mapping for reading legacy files).
2. Write `<config dir>/weixin/accounts/<accountId>.json` with `{token, baseUrl, userId, savedAt}` at mode 0600 (temp file + `os.Rename`). `baseUrl` comes from the response; fall back to the default host when empty.
3. Append the id to `<config dir>/weixin/accounts.json` (the index).
4. Remove any other stored account carrying the same `userId`, along with its `.sync.json` and `.context-tokens.json`, so context-token lookups stay unambiguous.
5. Seed `weixin.gateway.allow_from` from `ilink_user_id` when the operator has not configured one — this is what keeps the gateway from executing codex for strangers.
6. Print the bound `account_id` and `user_id`.

### B.4 Terminal rendering

`qrcode_img_content` is a URL; encode *that string* as the QR. Render with `qrterminal.GenerateHalfBlock` (level L, `os.Stdout`) so it fits a normal terminal, then always print:

```
若二维码未能显示或无法使用，你可以访问以下链接以继续：
<url>
```

If rendering fails for any reason, print the URL alone rather than aborting — the link is a complete fallback path.

### B.5 CLI surface

```
lark weixin login [--force] [--verbose] [--timeout 480s]
lark weixin accounts list
lark weixin accounts remove <account-id>
```

`--force` re-issues a QR even when a fresh in-process login session exists. Re-running `login` after a successful bind is expected to hit `binded_redirect` and exit 0. Running it with different WeChat accounts registers additional entries (the store is multi-account-shaped from day one; the gateway serves the single default account in v1 and gains `--account` later).

Sample session:

```
$ lark weixin login
正在启动...

用手机微信扫描以下二维码，以继续连接：
  ▄▄▄▄▄▄▄  ▄  ▄▄ ▄▄▄▄▄▄▄
  ...
若二维码未能显示或无法使用，你可以访问以下链接以继续：
https://...

正在等待操作...
正在验证
✅ 已连接到微信  account_id=b0f5860fdecb-im-bot  user_id=xxxx@im.wechat
```

### B.6 Tests

Drive `WaitForLogin` against an `httptest` server that replays a scripted status sequence, asserting on the resulting `WeixinQrWaitResult` and on what was persisted:

- `wait → scaned → confirmed` → account file written, index updated, allow-list seeded
- `need_verifycode → scaned → confirmed` → the second request carries `verify_code=`
- `need_verifycode → need_verifycode` → prompt text switches to the mismatch wording (inject the reader; do not read real stdin in tests)
- `verify_code_blocked` ×4 → gives up after 3 refreshes, no credentials written
- `expired → expired → expired → expired` → same
- `scaned_but_redirect` → subsequent polls hit the redirect host
- `binded_redirect` → success with `alreadyConnected`, existing credentials untouched
- `confirmed` without `ilink_bot_id` → error, nothing persisted
- deadline exceeded → timeout error, nothing persisted
- transport error mid-poll → treated as `wait`, loop continues
- account-id normalization + stale-same-`userId` cleanup

Stdin reading and the clock go behind small interfaces so every case is deterministic.
