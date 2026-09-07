package weixin

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// MaxInboundMediaBytes caps a single downloaded attachment.
const MaxInboundMediaBytes int64 = 100 * 1024 * 1024

// MediaDownloaderConfig configures inbound CDN media retrieval.
type MediaDownloaderConfig struct {
	CDNBaseURL string
	// Dir is where decrypted attachments are written.
	Dir      string
	MaxBytes int64
	Client   *http.Client
	Logger   *log.Logger
}

// MediaDownloader fetches, decrypts, and stores inbound Weixin attachments.
type MediaDownloader struct {
	cfg MediaDownloaderConfig
}

// NewMediaDownloader returns an inbound media downloader.
func NewMediaDownloader(cfg MediaDownloaderConfig) *MediaDownloader {
	if cfg.MaxBytes <= 0 {
		cfg.MaxBytes = MaxInboundMediaBytes
	}
	if strings.TrimSpace(cfg.CDNBaseURL) == "" {
		cfg.CDNBaseURL = DefaultCDNBaseURL
	}
	if cfg.Client == nil {
		cfg.Client = &http.Client{Timeout: 2 * time.Minute}
	}
	return &MediaDownloader{cfg: cfg}
}

// Download saves every attachment in a message and returns the local paths.
// A failure on one attachment is logged and skipped: the text half of the
// message is still worth delivering.
func (d *MediaDownloader) Download(ctx context.Context, items []MessageItem) []string {
	if d == nil {
		return nil
	}
	paths := make([]string, 0, len(items))
	for _, item := range items {
		path, err := d.downloadItem(ctx, item)
		if err != nil {
			if d.cfg.Logger != nil {
				d.cfg.Logger.Printf("inbound media download failed: %v", err)
			}
			continue
		}
		if path != "" {
			paths = append(paths, path)
		}
	}
	return paths
}

func (d *MediaDownloader) downloadItem(ctx context.Context, item MessageItem) (string, error) {
	switch item.Type {
	case ItemTypeImage:
		if item.ImageItem == nil {
			return "", nil
		}
		// image_item.aeskey is a raw hex key and takes precedence over the
		// base64 key on the media reference.
		aesKey := ""
		if hexKey := strings.TrimSpace(item.ImageItem.AESKey); hexKey != "" {
			aesKey = hexKeyAsBase64(hexKey)
		} else if item.ImageItem.Media != nil {
			aesKey = item.ImageItem.Media.AESKey
		}
		return d.fetch(ctx, item.ImageItem.Media, aesKey, "weixin-image", ".jpg")

	case ItemTypeVideo:
		if item.VideoItem == nil || item.VideoItem.Media == nil {
			return "", nil
		}
		return d.fetch(ctx, item.VideoItem.Media, item.VideoItem.Media.AESKey, "weixin-video", ".mp4")

	case ItemTypeFile:
		if item.FileItem == nil || item.FileItem.Media == nil {
			return "", nil
		}
		name := strings.TrimSpace(item.FileItem.FileName)
		ext := filepath.Ext(name)
		if ext == "" {
			ext = ".bin"
		}
		base := "weixin-file"
		if stem := strings.TrimSuffix(filepath.Base(name), ext); stem != "" && stem != "." {
			base = sanitizeFileStem(stem)
		}
		return d.fetch(ctx, item.FileItem.Media, item.FileItem.Media.AESKey, base, ext)

	case ItemTypeVoice:
		// Voice arrives as SILK, which has no comparable Go decoder. The
		// server-side transcription in voice_item.text is used instead, so
		// there is nothing to download here.
		return "", nil

	default:
		return "", nil
	}
}

// fetch downloads one CDN object, decrypts it when a key is present, and writes
// it to the media directory.
func (d *MediaDownloader) fetch(ctx context.Context, media *CDNMedia, aesKeyBase64, namePrefix, ext string) (string, error) {
	if media == nil {
		return "", nil
	}
	target := strings.TrimSpace(media.FullURL)
	if target == "" {
		if strings.TrimSpace(media.EncryptQueryParam) == "" {
			return "", nil
		}
		target = d.downloadURL(media.EncryptQueryParam)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return "", fmt.Errorf("build CDN request: %w", err)
	}
	resp, err := d.cfg.Client.Do(req)
	if err != nil {
		return "", fmt.Errorf("CDN download %s: %w", RedactURL(target), err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("CDN download %s returned HTTP %d", RedactURL(target), resp.StatusCode)
	}

	// Read one byte past the cap so an oversized object is detected rather than
	// silently truncated.
	data, err := io.ReadAll(io.LimitReader(resp.Body, d.cfg.MaxBytes+1))
	if err != nil {
		return "", fmt.Errorf("read CDN response: %w", err)
	}
	if int64(len(data)) > d.cfg.MaxBytes {
		return "", fmt.Errorf("CDN object exceeds the %d byte limit", d.cfg.MaxBytes)
	}

	if strings.TrimSpace(aesKeyBase64) != "" {
		key, keyErr := ParseAESKey(aesKeyBase64)
		if keyErr != nil {
			return "", keyErr
		}
		decrypted, decErr := DecryptAESECB(data, key)
		if decErr != nil {
			return "", decErr
		}
		data = decrypted
	}

	return d.save(data, namePrefix, ext)
}

func (d *MediaDownloader) downloadURL(encryptedQueryParam string) string {
	base := strings.TrimRight(d.cfg.CDNBaseURL, "/")
	return base + "/download?encrypted_query_param=" + url.QueryEscape(encryptedQueryParam)
}

func (d *MediaDownloader) save(data []byte, namePrefix, ext string) (string, error) {
	dir := strings.TrimSpace(d.cfg.Dir)
	if dir == "" {
		return "", fmt.Errorf("weixin media directory is not configured")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create media directory: %w", err)
	}
	name := fmt.Sprintf("%s-%d%s", namePrefix, time.Now().UnixNano(), ext)
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", fmt.Errorf("write media file: %w", err)
	}
	return path, nil
}

// hexKeyAsBase64 re-encodes a raw hex AES key so ParseAESKey can consume it
// through the same path as a wire-format key.
func hexKeyAsBase64(hexKey string) string {
	return base64.StdEncoding.EncodeToString([]byte(hexKey))
}

// sanitizeFileStem keeps a server-supplied filename from escaping the media
// directory or producing an unusable name.
func sanitizeFileStem(stem string) string {
	var b strings.Builder
	for _, r := range stem {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "weixin-file"
	}
	if len(out) > 64 {
		out = out[:64]
	}
	return out
}
