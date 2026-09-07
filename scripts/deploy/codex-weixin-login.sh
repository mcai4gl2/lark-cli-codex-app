#!/usr/bin/env bash
# Bind a WeChat account for the Codex Chat gateway.
#
# The only job of this wrapper is to pin LARK_CONFIG_DIR. `lark` refuses to run
# without it, and running the login with a different value is the one way to end
# up with credentials the gateway cannot find. Always bind through this script.
set -euo pipefail

export LARK_CONFIG_DIR="$HOME/.lark-codex-chat"
LARK_BIN="${LARK_BIN:-$HOME/.local/bin/lark}"
ACCOUNTS_DIR="$LARK_CONFIG_DIR/weixin/accounts"

mkdir -p "$LARK_CONFIG_DIR"
chmod 700 "$LARK_CONFIG_DIR"

echo "config dir: $LARK_CONFIG_DIR"
echo "credentials will be written to: $ACCOUNTS_DIR"
echo

# Scan the printed QR with the WeChat mobile app. Re-running after a successful
# bind is expected to report "already connected" and exit 0.
"$LARK_BIN" weixin login "$@"

echo
echo "stored accounts:"
"$LARK_BIN" weixin accounts list

if [[ -d "$ACCOUNTS_DIR" ]]; then
  echo
  echo "on disk:"
  ls -la "$ACCOUNTS_DIR"
  echo
  echo "back these up before reinstalling or moving machines:"
  echo "  tar czf ~/weixin-credentials-\$(date +%Y%m%d).tgz -C \"$LARK_CONFIG_DIR\" weixin"
fi
