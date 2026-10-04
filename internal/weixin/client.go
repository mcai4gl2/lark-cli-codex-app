package weixin

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// DefaultBaseURL is the fixed iLink host used before an account-specific
// baseurl is issued at login, and for every QR login call.
const DefaultBaseURL = "https://ilinkai.weixin.qq.com"

// DefaultCDNBaseURL is the CDN host used for media upload and download.
const DefaultCDNBaseURL = "https://novac2c.cdn.weixin.qq.com/c2c"

// DefaultAppID is the iLink-App-Id header value used by the official bot client.
const DefaultAppID = "bot"

// DefaultBotType is the bot_type query parameter used during QR login.
const DefaultBotType = "3"

// DefaultBotAgent identifies this client when no bot_agent is configured.
const DefaultBotAgent = "lark-cli"

// Client-side request timeouts, mirroring the reference plugin.
const (
	DefaultLongPollTimeout = 35 * time.Second
	defaultAPITimeout      = 15 * time.Second
	defaultConfigTimeout   = 10 * time.Second
)

// API endpoint paths.
const (
	endpointGetUpdates    = "ilink/bot/getupdates"
	endpointSendMessage   = "ilink/bot/sendmessage"
	endpointGetUploadURL  = "ilink/bot/getuploadurl"
	endpointGetConfig     = "ilink/bot/getconfig"
	endpointSendTyping    = "ilink/bot/sendtyping"
	endpointNotifyStart   = "ilink/bot/msg/notifystart"
	endpointNotifyStop    = "ilink/bot/msg/notifystop"
	endpointGetQRCode     = "ilink/bot/get_bot_qrcode"
	endpointQRCodeStatus  = "ilink/bot/get_qrcode_status"
	maxLocalTokenListSize = 10
)

// ClientConfig configures Weixin iLink API calls.
type ClientConfig struct {
	BaseURL string
	Token   string
	// AppID fills the iLink-App-Id header. Empty uses DefaultAppID.
	AppID string
	// ClientVersion fills the iLink-App-ClientVersion header. Zero derives it
	// from ChannelVersion.
	ClientVersion int
	// ChannelVersion is reported in base_info for server-side observability.
	ChannelVersion string
	// BotAgent is a UA-style Name/Version identity reported in base_info.
	BotAgent string
	// RouteTag fills the optional SKRouteTag header.
	RouteTag string
	Client   *http.Client
}

// Client is a Weixin iLink bot API client.
type Client struct {
	baseURL        string
	token          string
	appID          string
	clientVersion  int
	channelVersion string
	botAgent       string
	routeTag       string
	client         *http.Client
}

// NewClient returns a Weixin API client.
func NewClient(cfg ClientConfig) *Client {
	baseURL := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	appID := strings.TrimSpace(cfg.AppID)
	if appID == "" {
		appID = DefaultAppID
	}
	channelVersion := strings.TrimSpace(cfg.ChannelVersion)
	if channelVersion == "" {
		channelVersion = "0.0.0"
	}
	clientVersion := cfg.ClientVersion
	if clientVersion == 0 {
		clientVersion = BuildClientVersion(channelVersion)
	}
	httpClient := cfg.Client
	if httpClient == nil {
		// No client-level timeout: each call sets its own context deadline so a
		// 35s long-poll and a 10s config call can share one client.
		httpClient = &http.Client{}
	}
	return &Client{
		baseURL:        baseURL,
		token:          strings.TrimSpace(cfg.Token),
		appID:          appID,
		clientVersion:  clientVersion,
		channelVersion: channelVersion,
		botAgent:       SanitizeBotAgent(cfg.BotAgent),
		routeTag:       strings.TrimSpace(cfg.RouteTag),
		client:         httpClient,
	}
}

// BaseURL returns the host this client talks to.
func (c *Client) BaseURL() string { return c.baseURL }

// WithBaseURL returns a copy of the client pointed at a different host. It is
// used for QR login (fixed host) and for following an IDC redirect.
func (c *Client) WithBaseURL(baseURL string) *Client {
	trimmed := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if trimmed == "" || trimmed == c.baseURL {
		return c
	}
	clone := *c
	clone.baseURL = trimmed
	return &clone
}

// WithToken returns a copy of the client authenticating with a different token.
func (c *Client) WithToken(token string) *Client {
	clone := *c
	clone.token = strings.TrimSpace(token)
	return &clone
}

// BuildClientVersion encodes a semantic version as major<<16|minor<<8|patch,
// matching the iLink-App-ClientVersion header format.
func BuildClientVersion(version string) int {
	parts := strings.SplitN(strings.TrimSpace(version), ".", 4)
	value := 0
	for i := 0; i < 3; i++ {
		component := 0
		if i < len(parts) {
			// Tolerate suffixes such as "1.2.3-dirty" or a leading "v".
			digits := strings.TrimLeft(parts[i], "v")
			end := 0
			for end < len(digits) && digits[end] >= '0' && digits[end] <= '9' {
				end++
			}
			if end > 0 {
				parsed, err := strconv.Atoi(digits[:end])
				if err == nil {
					component = parsed & 0xff
				}
			}
		}
		value |= component << (16 - 8*i)
	}
	return value
}

func (c *Client) baseInfo() *BaseInfo {
	return &BaseInfo{ChannelVersion: c.channelVersion, BotAgent: c.botAgent}
}

// randomWechatUin builds the X-WECHAT-UIN header: a random uint32 rendered as a
// decimal string, then base64-encoded.
func randomWechatUin() string {
	var buf [4]byte
	if _, err := rand.Read(buf[:]); err != nil {
		// A predictable value is acceptable: the header is observability-only.
		binary.BigEndian.PutUint32(buf[:], uint32(time.Now().UnixNano()))
	}
	value := binary.BigEndian.Uint32(buf[:])
	return base64.StdEncoding.EncodeToString([]byte(strconv.FormatUint(uint64(value), 10)))
}

// commonHeaders are sent on every request, GET and POST alike.
func (c *Client) commonHeaders(header http.Header) {
	header.Set("iLink-App-Id", c.appID)
	header.Set("iLink-App-ClientVersion", strconv.Itoa(c.clientVersion))
	if c.routeTag != "" {
		header.Set("SKRouteTag", c.routeTag)
	}
}

func (c *Client) endpointURL(endpoint string) string {
	return c.baseURL + "/" + strings.TrimLeft(endpoint, "/")
}

// postJSON posts a JSON body and decodes the JSON response.
func (c *Client) postJSON(ctx context.Context, endpoint string, body interface{}, out interface{}, timeout time.Duration) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal weixin request: %w", err)
	}

	callCtx, cancel := withTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(callCtx, http.MethodPost, c.endpointURL(endpoint), bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("build weixin request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("AuthorizationType", "ilink_bot_token")
	req.Header.Set("X-WECHAT-UIN", randomWechatUin())
	c.commonHeaders(req.Header)
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return c.wrapCallError(ctx, endpoint, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read weixin %s response: %w", endpoint, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("weixin %s returned HTTP %d: %s", endpoint, resp.StatusCode, TruncateForLog(string(raw), 200))
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("decode weixin %s response: %w", endpoint, err)
	}
	return nil
}

// getJSON issues a GET with only the common headers and decodes the response.
func (c *Client) getJSON(ctx context.Context, endpoint string, params url.Values, out interface{}, timeout time.Duration) error {
	target := c.endpointURL(endpoint)
	if len(params) > 0 {
		target += "?" + params.Encode()
	}

	callCtx, cancel := withTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(callCtx, http.MethodGet, target, nil)
	if err != nil {
		return fmt.Errorf("build weixin request: %w", err)
	}
	c.commonHeaders(req.Header)

	resp, err := c.client.Do(req)
	if err != nil {
		return c.wrapCallError(ctx, endpoint, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read weixin %s response: %w", endpoint, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("weixin %s returned HTTP %d: %s", endpoint, resp.StatusCode, TruncateForLog(string(raw), 200))
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("decode weixin %s response: %w", endpoint, err)
	}
	return nil
}

func withTimeout(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout <= 0 {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, timeout)
}

// errRequestTimeout marks a client-side timeout that the caller may treat as
// normal control flow (long-poll expiry) rather than a failure.
var errRequestTimeout = errors.New("weixin request timeout")

func (c *Client) wrapCallError(parent context.Context, endpoint string, err error) error {
	if parent.Err() != nil {
		return parent.Err()
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("call weixin %s: %w", endpoint, errRequestTimeout)
	}
	kind, description := ClassifyNetworkError(err)
	return fmt.Errorf("call weixin %s: %s (%s): %w", endpoint, description, kind, err)
}

// ClassifyNetworkError categorizes a transport failure for logging.
func ClassifyNetworkError(err error) (kind string, description string) {
	if err == nil {
		return "", ""
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, errRequestTimeout) {
		return "timeout", "request timeout"
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return "dns", "DNS resolution failed, check DNS configuration"
	}
	var recordErr tls.RecordHeaderError
	if errors.As(err, &recordErr) {
		return "tls", "TLS handshake error"
	}
	var certErr *tls.CertificateVerificationError
	if errors.As(err, &certErr) {
		return "tls", "TLS certificate verification failed"
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return "timeout", "request timeout"
	}
	message := err.Error()
	switch {
	case strings.Contains(message, "connection refused"):
		return "tcp", "TCP connection refused"
	case strings.Contains(message, "no route to host"), strings.Contains(message, "network is unreachable"):
		return "tcp", "TCP connection unreachable"
	case strings.Contains(message, "tls"), strings.Contains(message, "certificate"):
		return "tls", "TLS handshake error"
	default:
		return "unknown", "network request failed"
	}
}

// GetUpdates long-polls for new messages. A client-side timeout is normal
// control flow for a long-poll, so it returns an empty successful response and
// the caller simply re-polls.
func (c *Client) GetUpdates(ctx context.Context, getUpdatesBuf string, timeout time.Duration) (GetUpdatesResp, error) {
	if timeout <= 0 {
		timeout = DefaultLongPollTimeout
	}
	req := GetUpdatesReq{GetUpdatesBuf: getUpdatesBuf, BaseInfo: c.baseInfo()}
	var resp GetUpdatesResp
	if err := c.postJSON(ctx, endpointGetUpdates, req, &resp, timeout); err != nil {
		if errors.Is(err, errRequestTimeout) {
			return GetUpdatesResp{Ret: 0, GetUpdatesBuf: getUpdatesBuf}, nil
		}
		return GetUpdatesResp{}, err
	}
	return resp, nil
}

// SendMessage sends one message downstream.
func (c *Client) SendMessage(ctx context.Context, msg *Message) error {
	req := SendMessageReq{Msg: msg, BaseInfo: c.baseInfo()}
	var resp SendMessageResp
	if err := c.postJSON(ctx, endpointSendMessage, req, &resp, defaultAPITimeout); err != nil {
		return err
	}
	if resp.Ret != 0 {
		return fmt.Errorf("weixin sendmessage failed: ret=%d errmsg=%s", resp.Ret, resp.ErrMsg)
	}
	return nil
}

// GetConfig fetches per-user bot config, which carries the typing ticket.
func (c *Client) GetConfig(ctx context.Context, userID, contextToken string) (GetConfigResp, error) {
	req := GetConfigReq{ILinkUserID: userID, ContextToken: contextToken, BaseInfo: c.baseInfo()}
	var resp GetConfigResp
	if err := c.postJSON(ctx, endpointGetConfig, req, &resp, defaultConfigTimeout); err != nil {
		return GetConfigResp{}, err
	}
	return resp, nil
}

// SendTyping shows or cancels the typing indicator for one user.
func (c *Client) SendTyping(ctx context.Context, userID, typingTicket string, status int) error {
	req := SendTypingReq{
		ILinkUserID:  userID,
		TypingTicket: typingTicket,
		Status:       status,
		BaseInfo:     c.baseInfo(),
	}
	var resp SendTypingResp
	if err := c.postJSON(ctx, endpointSendTyping, req, &resp, defaultConfigTimeout); err != nil {
		return err
	}
	if resp.Ret != 0 {
		return fmt.Errorf("weixin sendtyping failed: ret=%d errmsg=%s", resp.Ret, resp.ErrMsg)
	}
	return nil
}

// NotifyStart announces that this client is starting.
func (c *Client) NotifyStart(ctx context.Context) error {
	return c.notify(ctx, endpointNotifyStart)
}

// NotifyStop announces that this client is stopping.
func (c *Client) NotifyStop(ctx context.Context) error {
	return c.notify(ctx, endpointNotifyStop)
}

func (c *Client) notify(ctx context.Context, endpoint string) error {
	var resp NotifyResp
	if err := c.postJSON(ctx, endpoint, NotifyReq{BaseInfo: c.baseInfo()}, &resp, defaultConfigTimeout); err != nil {
		return err
	}
	if resp.Ret != 0 {
		return fmt.Errorf("weixin %s failed: ret=%d errmsg=%s", endpoint, resp.Ret, resp.ErrMsg)
	}
	return nil
}

// GetUploadURL requests pre-signed CDN upload parameters for outbound media.
func (c *Client) GetUploadURL(ctx context.Context, req GetUploadURLReq) (GetUploadURLResp, error) {
	req.BaseInfo = c.baseInfo()
	var resp GetUploadURLResp
	if err := c.postJSON(ctx, endpointGetUploadURL, req, &resp, defaultAPITimeout); err != nil {
		return GetUploadURLResp{}, err
	}
	if resp.Ret != 0 {
		return GetUploadURLResp{}, fmt.Errorf("weixin getuploadurl failed: ret=%d errmsg=%s", resp.Ret, resp.ErrMsg)
	}
	return resp, nil
}

// FetchQRCode requests a login QR code. localTokens lets the server recognize a
// client that is already bound (which produces `binded_redirect`); the newest
// tokens are sent first and the list is capped at ten.
func (c *Client) FetchQRCode(ctx context.Context, botType string, localTokens []string) (QRCodeResp, error) {
	if strings.TrimSpace(botType) == "" {
		botType = DefaultBotType
	}
	if len(localTokens) > maxLocalTokenListSize {
		localTokens = localTokens[:maxLocalTokenListSize]
	}
	if localTokens == nil {
		localTokens = []string{}
	}
	endpoint := endpointGetQRCode + "?bot_type=" + url.QueryEscape(botType)
	// The QR endpoints are unauthenticated: this client must carry no token.
	var resp QRCodeResp
	if err := c.WithToken("").postJSON(ctx, endpoint, QRCodeReq{LocalTokenList: localTokens}, &resp, defaultAPITimeout); err != nil {
		return QRCodeResp{}, err
	}
	if strings.TrimSpace(resp.QRCode) == "" {
		return QRCodeResp{}, fmt.Errorf("weixin get_bot_qrcode returned an empty qrcode")
	}
	return resp, nil
}

// PollQRStatus long-polls the QR scan state. Gateway and transport errors are
// reported to the caller, which treats them as "keep waiting".
func (c *Client) PollQRStatus(ctx context.Context, qrcode, verifyCode string) (QRStatusResp, error) {
	params := url.Values{"qrcode": []string{qrcode}}
	if strings.TrimSpace(verifyCode) != "" {
		params.Set("verify_code", strings.TrimSpace(verifyCode))
	}
	var resp QRStatusResp
	if err := c.getJSON(ctx, endpointQRCodeStatus, params, &resp, DefaultLongPollTimeout); err != nil {
		return QRStatusResp{}, err
	}
	return resp, nil
}
