package weixin

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// DefaultLoginTimeout bounds a whole QR login attempt.
const DefaultLoginTimeout = 480 * time.Second

// defaultQRPollInterval is the pause between get_qrcode_status long-polls.
const defaultQRPollInterval = time.Second

// maxQRRefreshCount caps how many QR codes one login attempt may burn through.
// The counter starts at 1 for the initial code, so an expiry can be recovered
// from twice before the attempt is abandoned.
const maxQRRefreshCount = 3

// LoginConfig configures an interactive QR login.
type LoginConfig struct {
	// Client must point at the fixed iLink host and carry no token.
	Client *Client
	// Store persists the account on success and supplies local_token_list.
	Store      *Store
	BotType    string
	CDNBaseURL string
	Timeout    time.Duration
	// PollInterval is the pause between status polls.
	PollInterval time.Duration
	Verbose      bool
	Stdout       io.Writer
	// Stdin supplies the pairing digits when the server asks for them.
	Stdin io.Reader
	Now   func() time.Time
	Sleep func(ctx context.Context, d time.Duration) error
	// RenderQR draws the QR code. Nil uses DisplayQRCode.
	RenderQR func(w io.Writer, qrcodeURL string)

	stdinReader *bufio.Reader
}

func (c *LoginConfig) stdout() io.Writer {
	if c.Stdout == nil {
		return os.Stdout
	}
	return c.Stdout
}

func (c *LoginConfig) now() time.Time {
	if c.Now == nil {
		return time.Now()
	}
	return c.Now()
}

func (c *LoginConfig) sleep(ctx context.Context, d time.Duration) error {
	if c.Sleep != nil {
		return c.Sleep(ctx, d)
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (c *LoginConfig) renderQR(qrcodeURL string) {
	if c.RenderQR != nil {
		c.RenderQR(c.stdout(), qrcodeURL)
		return
	}
	DisplayQRCode(c.stdout(), qrcodeURL)
}

func (c *LoginConfig) botType() string {
	if strings.TrimSpace(c.BotType) == "" {
		return DefaultBotType
	}
	return strings.TrimSpace(c.BotType)
}

func (c *LoginConfig) pollInterval() time.Duration {
	if c.PollInterval <= 0 {
		return defaultQRPollInterval
	}
	return c.PollInterval
}

// readVerifyCode prompts on stdout and reads one line of pairing digits.
func (c *LoginConfig) readVerifyCode(prompt string) (string, error) {
	fmt.Fprint(c.stdout(), prompt)
	if c.Stdin == nil {
		c.Stdin = os.Stdin
	}
	if c.stdinReader == nil {
		c.stdinReader = bufio.NewReader(c.Stdin)
	}
	line, err := c.stdinReader.ReadString('\n')
	line = strings.TrimSpace(line)
	if err != nil && line == "" {
		return "", fmt.Errorf("read verify code: %w", err)
	}
	return line, nil
}

// LoginSession is an in-flight QR login.
type LoginSession struct {
	QRCode    string
	QRCodeURL string
	StartedAt time.Time

	// client tracks the current polling host, which an IDC redirect may change.
	client            *Client
	pendingVerifyCode string
}

// LoginResult reports the outcome of a QR login attempt.
type LoginResult struct {
	Connected bool
	// AlreadyConnected marks `binded_redirect`: the scanned bot is already bound
	// to this client, so no new credentials were issued and the existing ones
	// remain valid. Callers must treat this as success.
	AlreadyConnected bool
	AccountID        string
	UserID           string
	BaseURL          string
	Message          string
}

// StartQRLogin fetches a login QR code from the fixed iLink host.
func StartQRLogin(ctx context.Context, cfg *LoginConfig) (*LoginSession, error) {
	if cfg == nil || cfg.Client == nil {
		return nil, fmt.Errorf("weixin login requires an API client")
	}
	var localTokens []string
	if cfg.Store != nil {
		localTokens = cfg.Store.Tokens(maxLocalTokenListSize)
	}
	resp, err := cfg.Client.FetchQRCode(ctx, cfg.botType(), localTokens)
	if err != nil {
		return nil, err
	}
	return &LoginSession{
		QRCode:    resp.QRCode,
		QRCodeURL: resp.QRCodeImgContent,
		StartedAt: cfg.now(),
		client:    cfg.Client,
	}, nil
}

// WaitForLogin drives the QR status state machine until the login completes,
// the deadline passes, or the QR refresh budget is spent. On `confirmed` it
// persists the account before returning.
func WaitForLogin(ctx context.Context, session *LoginSession, cfg *LoginConfig) (LoginResult, error) {
	if session == nil {
		return LoginResult{}, fmt.Errorf("weixin login session is required")
	}
	if cfg == nil || cfg.Client == nil {
		return LoginResult{}, fmt.Errorf("weixin login requires an API client")
	}
	if session.client == nil {
		session.client = cfg.Client
	}

	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = DefaultLoginTimeout
	}
	deadline := cfg.now().Add(timeout)
	scannedPrinted := false
	qrRefreshCount := 1
	out := cfg.stdout()

	for cfg.now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			return LoginResult{}, err
		}

		status, err := session.client.PollQRStatus(ctx, session.QRCode, session.pendingVerifyCode)
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return LoginResult{}, ctxErr
			}
			// Client timeouts and gateway errors (Cloudflare 524 and friends) are
			// normal for a long-poll: keep waiting.
			if cfg.Verbose {
				fmt.Fprintf(out, "\n轮询出错，将重试: %v\n", err)
			}
			if err := cfg.sleep(ctx, cfg.pollInterval()); err != nil {
				return LoginResult{}, err
			}
			continue
		}

		switch status.Status {
		case QRStatusWait:
			if cfg.Verbose {
				fmt.Fprint(out, ".")
			}

		case QRStatusScanned:
			// Reaching `scaned` while a code was pending means it was accepted.
			session.pendingVerifyCode = ""
			if !scannedPrinted {
				fmt.Fprint(out, "\n正在验证\n")
				scannedPrinted = true
			}

		case QRStatusNeedVerifyCode:
			prompt := "输入手机微信显示的数字，以继续连接："
			if session.pendingVerifyCode != "" {
				prompt = "❌ 你输入的数字不匹配，请重新输入："
			}
			code, readErr := cfg.readVerifyCode(prompt)
			if readErr != nil {
				return LoginResult{}, readErr
			}
			session.pendingVerifyCode = code
			// Re-poll immediately so the code is submitted without delay.
			continue

		case QRStatusVerifyCodeBlocked:
			fmt.Fprint(out, "\n⛔ 多次输入错误，请稍后再试。\n")
			session.pendingVerifyCode = ""
			qrRefreshCount++
			if qrRefreshCount > maxQRRefreshCount {
				return giveUp("多次输入错误，连接流程已停止。请稍后再试。")
			}
			if err := refreshQRCode(ctx, session, cfg, qrRefreshCount, &scannedPrinted); err != nil {
				return giveUp(fmt.Sprintf("刷新二维码失败: %v", err))
			}

		case QRStatusExpired:
			qrRefreshCount++
			if qrRefreshCount > maxQRRefreshCount {
				return giveUp("二维码多次失效，连接流程已停止。请稍后再试。")
			}
			fmt.Fprint(out, "\n⏳ 二维码已过期，正在刷新...\n")
			if err := refreshQRCode(ctx, session, cfg, qrRefreshCount, &scannedPrinted); err != nil {
				return giveUp(fmt.Sprintf("刷新二维码失败: %v", err))
			}

		case QRStatusScannedButRedirect:
			host := strings.TrimSpace(status.RedirectHost)
			if host == "" {
				fmt.Fprint(out, "\n⚠️ 服务端要求跳转但未返回 redirect_host，继续使用当前地址。\n")
				break
			}
			session.client = session.client.WithBaseURL("https://" + host)
			if cfg.Verbose {
				fmt.Fprintf(out, "\n已切换到 %s\n", host)
			}

		case QRStatusBindedRedirect:
			fmt.Fprint(out, "\n✅ 已连接过此客户端，无需重复连接。\n")
			return LoginResult{
				AlreadyConnected: true,
				Message:          "已连接过此客户端，无需重复连接。",
			}, nil

		case QRStatusConfirmed:
			if strings.TrimSpace(status.ILinkBotID) == "" {
				return LoginResult{}, fmt.Errorf("登录失败：服务器未返回 ilink_bot_id")
			}
			return persistLogin(status, cfg)

		default:
			if cfg.Verbose {
				fmt.Fprintf(out, "\n未知状态: %s\n", status.Status)
			}
		}

		if err := cfg.sleep(ctx, cfg.pollInterval()); err != nil {
			return LoginResult{}, err
		}
	}

	return giveUp("登录超时，请重试。")
}

// giveUp reports an abandoned login attempt: no credentials were written, and
// the user-facing reason is carried on both the result and the error.
func giveUp(message string) (LoginResult, error) {
	return LoginResult{Message: message}, errors.New(message)
}

// persistLogin stores the confirmed credentials and clears stale accounts that
// belong to the same Weixin user.
func persistLogin(status QRStatusResp, cfg *LoginConfig) (LoginResult, error) {
	accountID := NormalizeAccountID(status.ILinkBotID)
	baseURL := strings.TrimSpace(status.BaseURL)
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	result := LoginResult{
		Connected: true,
		AccountID: accountID,
		UserID:    strings.TrimSpace(status.ILinkUserID),
		BaseURL:   baseURL,
		Message:   "已将此客户端连接到微信。",
	}
	if cfg.Store == nil {
		return result, nil
	}

	account := Account{
		Token:      strings.TrimSpace(status.BotToken),
		BaseURL:    baseURL,
		CDNBaseURL: strings.TrimSpace(cfg.CDNBaseURL),
		UserID:     result.UserID,
	}
	if err := cfg.Store.Save(accountID, account); err != nil {
		return LoginResult{}, err
	}
	removed, err := cfg.Store.RemoveStaleForUserID(accountID, result.UserID)
	if err != nil {
		return LoginResult{}, err
	}
	for _, id := range removed {
		fmt.Fprintf(cfg.stdout(), "已移除同一微信用户的旧账号: %s\n", id)
	}
	return result, nil
}

func refreshQRCode(ctx context.Context, session *LoginSession, cfg *LoginConfig, attempt int, scannedPrinted *bool) error {
	out := cfg.stdout()
	fmt.Fprintf(out, "\n⏳ 正在刷新二维码...(%d/%d)\n", attempt, maxQRRefreshCount)

	var localTokens []string
	if cfg.Store != nil {
		localTokens = cfg.Store.Tokens(maxLocalTokenListSize)
	}
	// A refresh always goes back to the fixed host, not a redirected one.
	resp, err := cfg.Client.FetchQRCode(ctx, cfg.botType(), localTokens)
	if err != nil {
		return err
	}
	session.QRCode = resp.QRCode
	session.QRCodeURL = resp.QRCodeImgContent
	session.StartedAt = cfg.now()
	session.pendingVerifyCode = ""
	session.client = cfg.Client
	*scannedPrinted = false

	fmt.Fprint(out, "🔄 二维码已更新，请重新扫描。\n\n")
	cfg.renderQR(resp.QRCodeImgContent)
	return nil
}

// RunInteractiveLogin performs the whole terminal login flow: fetch a QR code,
// render it, and wait for the scan to be confirmed.
func RunInteractiveLogin(ctx context.Context, cfg *LoginConfig) (LoginResult, error) {
	out := cfg.stdout()
	fmt.Fprint(out, "正在启动...\n\n")

	session, err := StartQRLogin(ctx, cfg)
	if err != nil {
		return LoginResult{}, err
	}

	fmt.Fprint(out, "用手机微信扫描以下二维码，以继续连接：\n")
	cfg.renderQR(session.QRCodeURL)
	fmt.Fprint(out, "\n正在等待操作...\n")

	return WaitForLogin(ctx, session, cfg)
}
