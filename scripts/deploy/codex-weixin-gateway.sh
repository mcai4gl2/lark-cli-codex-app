#!/usr/bin/env bash
# Start the Codex Chat WeChat gateway, mirroring codex-chat-gateway.sh.
#
# No tokens live in this file. WeChat credentials are written by
# `codex-weixin-login.sh` into $LARK_CONFIG_DIR/weixin/accounts/ and are read
# from there; this script only has to point at the same config dir.
set -euo pipefail

export LARK_CONFIG_DIR="$HOME/.lark-codex-chat"

GATEWAY_LOG="${GATEWAY_LOG:-$HOME/CodexChat/.weixin/gateway.log}"
GATEWAY_PID_FILE="${GATEWAY_PID_FILE:-$HOME/CodexChat/.weixin/gateway.pid}"
EVENT_LOG="$HOME/CodexChat/.weixin/gateway-events.jsonl"

mkdir -p "$(dirname "$EVENT_LOG")" "$(dirname "$GATEWAY_LOG")" "$(dirname "$GATEWAY_PID_FILE")"

LARK_BIN="${LARK_BIN:-$HOME/.local/bin/lark}"

# Fail early and clearly when no account is bound, rather than starting a
# gateway that can only error.
if [[ ! -f "$LARK_CONFIG_DIR/weixin/accounts.json" ]]; then
  echo "no WeChat account bound yet in $LARK_CONFIG_DIR"
  echo "run: $HOME/bin/codex-weixin-login.sh"
  exit 1
fi

if [[ -f "$GATEWAY_PID_FILE" ]]; then
  existing_pid="$(cat "$GATEWAY_PID_FILE")"
  if [[ -n "$existing_pid" ]] && kill -0 "$existing_pid" 2>/dev/null; then
    echo "lark weixin gateway is already running: pid=$existing_pid"
    echo "log: $GATEWAY_LOG"
    exit 0
  fi
  rm -f "$GATEWAY_PID_FILE"
fi

# Remaining settings (event log, memory root, agent workspace, allow-list) come
# from the weixin block in $LARK_CONFIG_DIR/config.yaml.
gateway_cmd=(
  "$LARK_BIN" weixin gateway serve
  --agent \
  --agent-backend pi \
  --memory \
  --memory-root "$HOME/CodexChat/.weixin/conversations" \
  --agent-workspace "$HOME/CodexChat"
)

nohup setsid -f bash -c '
  pid_file="$1"
  log_file="$2"
  shift 2

  (
    echo "starting lark weixin gateway at $(date -Is)"
    "$@" &
    child_pid=$!
    printf "%s\n" "$child_pid" > "$pid_file"
    wait "$child_pid"
    status=$?
    echo "lark weixin gateway exited at $(date -Is) status=$status"
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
  echo "failed to start lark weixin gateway; recent log:"
  tail -n 40 "$GATEWAY_LOG" || true
  exit 1
fi

echo "started lark weixin gateway: pid=$gateway_pid"
echo "log: $GATEWAY_LOG"
echo "follow: tail -f '$GATEWAY_LOG'"
echo
echo "stop with: kill \$(cat '$GATEWAY_PID_FILE')"
echo "  (SIGTERM lets the gateway send its notifystop; avoid kill -9)"
