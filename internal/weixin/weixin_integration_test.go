//go:build integration

package weixin

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// This is a local-only integration test. It drives the real Weixin iLink
// service using the credentials already stored by `lark weixin login`.
//
// It is gated twice: the `integration` build tag (so the normal
// `go test ./...` never compiles it) and WEIXIN_INTEGRATION_TEST=1 (so
// `-tags integration` alone still skips). Following the documented exception in
// CLAUDE.md, compile it in Docker and run the binary on the host:
//
//	docker run --rm -v "$PWD:/work" -w /work golang:1.24 \
//	  go test -c -tags integration -o weixin_integration.test ./internal/weixin
//	WEIXIN_INTEGRATION_TEST=1 ./weixin_integration.test -test.v -test.run TestWeixinIntegration
//	rm -f weixin_integration.test
//
// It is read-mostly by design: it never writes an account file, never clears a
// poll cursor, and never runs a login. The one test that sends a message is
// opt-in through WEIXIN_INTEGRATION_TO, because sending pushes a real message
// into someone's WeChat.

const integrationCallTimeout = 30 * time.Second

// integrationAccount loads the stored credentials, skipping when the
// environment is not ready.
func integrationAccount(t *testing.T) (Account, *Store) {
	t.Helper()
	if os.Getenv("WEIXIN_INTEGRATION_TEST") != "1" {
		t.Skip("set WEIXIN_INTEGRATION_TEST=1 to run the Weixin integration test")
	}

	stateDir := strings.TrimSpace(os.Getenv("WEIXIN_INTEGRATION_STATE_DIR"))
	if stateDir == "" {
		configDir := strings.TrimSpace(os.Getenv("LARK_CONFIG_DIR"))
		if configDir == "" {
			t.Skip("LARK_CONFIG_DIR is not set; cannot locate stored Weixin credentials")
		}
		stateDir = filepath.Join(configDir, "weixin")
	}

	store := NewStore(stateDir)
	account, err := store.Resolve(os.Getenv("WEIXIN_INTEGRATION_ACCOUNT"))
	if err != nil {
		t.Skipf("no usable Weixin account in %s: %v", stateDir, err)
	}
	if strings.TrimSpace(account.Token) == "" {
		t.Skipf("account %s has no stored token; run `lark weixin login`", account.AccountID)
	}
	if strings.TrimSpace(account.BaseURL) == "" {
		account.BaseURL = DefaultBaseURL
	}
	return account, store
}

func integrationClient(t *testing.T, account Account) *Client {
	t.Helper()
	return NewClient(ClientConfig{
		BaseURL:        account.BaseURL,
		Token:          account.Token,
		AppID:          DefaultAppID,
		ChannelVersion: "0.0.0",
		BotAgent:       "lark-cli/integration-test",
	})
}

func TestWeixinIntegration(t *testing.T) {
	account, store := integrationAccount(t)
	client := integrationClient(t, account)

	t.Run("prerequisite", func(t *testing.T) {
		t.Logf("account_id=%s base_url=%s user_id=%s token=%s",
			account.AccountID, account.BaseURL, account.UserID, RedactToken(account.Token))
		if account.UserID == "" {
			t.Log("warning: no bound user id recorded; the gateway would have no default allow-list")
		}
	})

	t.Run("notify start and stop", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), integrationCallTimeout)
		defer cancel()
		if err := client.NotifyStart(ctx); err != nil {
			t.Fatalf("NotifyStart() error = %v", err)
		}
		if err := client.NotifyStop(ctx); err != nil {
			t.Fatalf("NotifyStop() error = %v", err)
		}
	})

	t.Run("getconfig returns a typing ticket", func(t *testing.T) {
		if account.UserID == "" {
			t.Skip("no bound user id to query config for")
		}
		ctx, cancel := context.WithTimeout(context.Background(), integrationCallTimeout)
		defer cancel()

		resp, err := client.GetConfig(ctx, account.UserID, "")
		if err != nil {
			t.Fatalf("GetConfig() error = %v", err)
		}
		if resp.Ret != 0 {
			t.Fatalf("GetConfig() ret=%d errmsg=%s", resp.Ret, resp.ErrMsg)
		}
		t.Logf("typing_ticket=%s", RedactToken(resp.TypingTicket))
	})

	t.Run("getupdates long-polls without consuming the stored cursor", func(t *testing.T) {
		// The stored cursor is read but never written back, so running this
		// test cannot make the gateway skip or replay real messages.
		syncPath := store.SyncBufPath(account.AccountID)
		before := NewSyncStore(syncPath).Load()

		ctx, cancel := context.WithTimeout(context.Background(), 2*integrationCallTimeout)
		defer cancel()

		resp, err := client.GetUpdates(ctx, before.GetUpdatesBuf, 10*time.Second)
		if err != nil {
			t.Fatalf("GetUpdates() error = %v", err)
		}
		if resp.ErrCode == StaleTokenErrCode || resp.Ret == StaleTokenErrCode {
			t.Fatalf("the stored token is stale (errcode %d); re-run `lark weixin login`", StaleTokenErrCode)
		}
		if resp.Ret != 0 || resp.ErrCode != 0 {
			t.Fatalf("GetUpdates() ret=%d errcode=%d errmsg=%s", resp.Ret, resp.ErrCode, resp.ErrMsg)
		}
		t.Logf("messages=%d cursor_bytes=%d next_timeout_ms=%d",
			len(resp.Msgs), len(resp.GetUpdatesBuf), resp.LongPollingTimeoutMS)

		after := NewSyncStore(syncPath).Load()
		if after != before {
			t.Fatalf("the stored cursor was modified: %+v -> %+v", before, after)
		}
	})

	t.Run("send a message", func(t *testing.T) {
		to := strings.TrimSpace(os.Getenv("WEIXIN_INTEGRATION_TO"))
		if to == "" {
			t.Skip("set WEIXIN_INTEGRATION_TO=<user-id> to send a real test message")
		}

		tokens := NewContextTokenStore(store.ContextTokenPath(account.AccountID))
		if _, err := tokens.Restore(); err != nil {
			t.Fatalf("Restore() error = %v", err)
		}
		if _, ok := tokens.Get(to); !ok {
			t.Skipf("no context token for %s; message the bot once while a gateway is running", to)
		}

		messenger := NewMessenger(MessengerConfig{
			Client:        client,
			ContextTokens: tokens,
			Guard:         NewSessionGuard(),
			CDNBaseURL:    account.CDNBaseURL,
		})

		ctx, cancel := context.WithTimeout(context.Background(), integrationCallTimeout)
		defer cancel()

		text := "lark-cli integration test " + time.Now().Format(time.RFC3339)
		if err := messenger.SendText(ctx, to, text); err != nil {
			t.Fatalf("SendText() error = %v", err)
		}
		t.Logf("sent to %s", to)
	})
}
