package weixin

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/yjwong/lark-cli/internal/platform"
)

// MessengerConfig configures outbound Weixin sends.
type MessengerConfig struct {
	Client        *Client
	ContextTokens *ContextTokenStore
	Guard         *SessionGuard
	// MaxChunkRunes caps one outbound text message. Zero uses MaxChunkRunes.
	MaxChunkRunes int
	// ChunkPause spaces out consecutive chunks. The server's rate limits are
	// undocumented, so long replies are paced rather than blasted.
	ChunkPause time.Duration
	// CDNBaseURL is used to upload media referenced by MEDIA: directives.
	CDNBaseURL string
	// UploadClient carries CDN uploads, which need a longer timeout than the
	// API client's per-call deadlines.
	UploadClient *http.Client
	Logger       *log.Logger
}

// Messenger sends Weixin replies through the provider-neutral interface.
type Messenger struct {
	cfg      MessengerConfig
	uploader *Uploader
}

// NewMessenger returns a platform.Messenger backed by the Weixin API.
func NewMessenger(cfg MessengerConfig) *Messenger {
	if cfg.MaxChunkRunes <= 0 {
		cfg.MaxChunkRunes = MaxChunkRunes
	}
	m := &Messenger{cfg: cfg}
	if cfg.Client != nil {
		m.uploader = NewUploader(UploaderConfig{
			Client:     cfg.Client,
			CDNBaseURL: cfg.CDNBaseURL,
			HTTPClient: cfg.UploadClient,
			Logger:     cfg.Logger,
		})
	}
	return m
}

var _ platform.Messenger = (*Messenger)(nil)

// Reply answers the sender of an inbound event.
func (m *Messenger) Reply(ctx context.Context, event platform.MessageEvent, text string) error {
	target := strings.TrimSpace(event.ChannelID)
	if target == "" {
		target = strings.TrimSpace(event.UserID)
	}
	return m.send(ctx, target, text)
}

// Send delivers a message to an explicit target user.
func (m *Messenger) Send(ctx context.Context, target platform.MessageTarget, text string) error {
	userID := strings.TrimSpace(target.ChannelID)
	if userID == "" {
		userID = strings.TrimSpace(target.UserID)
	}
	return m.send(ctx, userID, text)
}

// send routes agent output: any MEDIA: directives become attachments, and what
// remains is sent as text.
func (m *Messenger) send(ctx context.Context, userID, text string) error {
	body, mediaPaths := ExtractMediaDirectives(text)
	if len(mediaPaths) == 0 {
		return m.SendText(ctx, userID, body)
	}
	for i, path := range mediaPaths {
		caption := ""
		if i == 0 {
			caption = body
		}
		if err := m.SendMedia(ctx, userID, caption, path); err != nil {
			return err
		}
	}
	return nil
}

// SendText strips markdown, splits the result into WeChat-sized chunks, and
// sends each chunk as its own message.
func (m *Messenger) SendText(ctx context.Context, userID, text string) error {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return fmt.Errorf("weixin recipient user id is required")
	}
	if m.cfg.Client == nil {
		return fmt.Errorf("weixin messenger has no API client")
	}
	if err := m.cfg.Guard.AssertActive(); err != nil {
		return err
	}

	plain := StripMarkdown(text)
	chunks := SplitChunks(plain, m.cfg.MaxChunkRunes)
	if len(chunks) == 0 {
		return fmt.Errorf("weixin message text is required")
	}

	contextToken, ok := m.cfg.ContextTokens.Get(userID)
	if !ok && m.cfg.Logger != nil {
		// The send is still attempted: the server tolerates a missing token on
		// some paths, and dropping the reply outright would be worse.
		m.cfg.Logger.Printf("context token missing for user_id=%s, sending without conversation context", userID)
	}

	for i, chunk := range chunks {
		if i > 0 && m.cfg.ChunkPause > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(m.cfg.ChunkPause):
			}
		}
		if err := m.sendItem(ctx, userID, contextToken, MessageItem{
			Type:     ItemTypeText,
			TextItem: &TextItem{Text: chunk},
		}); err != nil {
			return err
		}
	}
	return nil
}

// sendItem posts exactly one message item, as the protocol requires.
func (m *Messenger) sendItem(ctx context.Context, userID, contextToken string, item MessageItem) error {
	return m.cfg.Client.SendMessage(ctx, &Message{
		FromUserID:   "",
		ToUserID:     userID,
		ClientID:     NewClientID(),
		MessageType:  MessageTypeBot,
		MessageState: MessageStateFinish,
		ItemList:     []MessageItem{item},
		ContextToken: contextToken,
	})
}

// SendMedia uploads a local file and sends it as an attachment. Because the
// protocol allows exactly one item per request, a caption is sent as its own
// text message immediately before the attachment.
func (m *Messenger) SendMedia(ctx context.Context, userID, caption, filePath string) error {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return fmt.Errorf("weixin recipient user id is required")
	}
	if m.uploader == nil {
		return fmt.Errorf("weixin messenger has no API client")
	}
	if err := m.cfg.Guard.AssertActive(); err != nil {
		return err
	}

	if strings.TrimSpace(caption) != "" {
		if err := m.SendText(ctx, userID, caption); err != nil {
			return err
		}
	}

	uploaded, err := m.uploader.Upload(ctx, filePath, userID)
	if err != nil {
		return err
	}
	contextToken, _ := m.cfg.ContextTokens.Get(userID)
	return m.sendItem(ctx, userID, contextToken, MessageItemForUpload(uploaded))
}

// ExtractMediaDirectives pulls "MEDIA:<path>" lines out of agent output and
// returns the remaining text plus the referenced paths. Only absolute paths are
// honored, so a stray mention in prose cannot make the bot read a relative file.
func ExtractMediaDirectives(text string) (string, []string) {
	if !strings.Contains(text, mediaDirectivePrefix) {
		return text, nil
	}
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	kept := make([]string, 0, len(lines))
	paths := make([]string, 0, 2)
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, mediaDirectivePrefix) {
			kept = append(kept, line)
			continue
		}
		path := strings.TrimSpace(strings.TrimPrefix(trimmed, mediaDirectivePrefix))
		if path == "" || !filepath.IsAbs(path) {
			kept = append(kept, line)
			continue
		}
		paths = append(paths, path)
	}
	return strings.TrimSpace(strings.Join(kept, "\n")), paths
}

// mediaDirectivePrefix marks an outbound attachment in agent output.
const mediaDirectivePrefix = "MEDIA:"

// ContextToken returns the cached conversation token for a user.
func (m *Messenger) ContextToken(userID string) (string, bool) {
	return m.cfg.ContextTokens.Get(userID)
}

// NewClientID generates the locally chosen id echoed back as a message id.
func NewClientID() string {
	var buf [4]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return fmt.Sprintf("lark-cli:%d", time.Now().UnixMilli())
	}
	return fmt.Sprintf("lark-cli:%d-%s", time.Now().UnixMilli(), hex.EncodeToString(buf[:]))
}
