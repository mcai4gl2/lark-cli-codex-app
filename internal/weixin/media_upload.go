package weixin

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
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

// uploadMaxRetries bounds CDN upload attempts. Server errors are retried;
// client errors are not, because they will not succeed on a retry.
const uploadMaxRetries = 3

// UploadedFile describes a file that now lives on the Weixin CDN.
type UploadedFile struct {
	FileKey string
	// DownloadEncryptedQueryParam goes into the outbound item's
	// media.encrypt_query_param.
	DownloadEncryptedQueryParam string
	AESKey                      []byte
	// FileSize is the plaintext size; FileSizeCiphertext is the padded size.
	FileSize           int64
	FileSizeCiphertext int64
	FileName           string
	MediaType          int
}

// AESKeyBase64 renders the key the way CDNMedia.aes_key expects it.
func (u UploadedFile) AESKeyBase64() string {
	return base64.StdEncoding.EncodeToString(u.AESKey)
}

// UploaderConfig configures outbound CDN uploads.
type UploaderConfig struct {
	Client     *Client
	CDNBaseURL string
	HTTPClient *http.Client
	Logger     *log.Logger
}

// Uploader encrypts local files and pushes them to the Weixin CDN.
type Uploader struct {
	cfg UploaderConfig
}

// NewUploader returns an outbound media uploader.
func NewUploader(cfg UploaderConfig) *Uploader {
	if strings.TrimSpace(cfg.CDNBaseURL) == "" {
		cfg.CDNBaseURL = DefaultCDNBaseURL
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 5 * time.Minute}
	}
	return &Uploader{cfg: cfg}
}

// Upload reads a local file, encrypts it with a fresh AES-128 key, asks the
// server for upload parameters, and pushes the ciphertext to the CDN.
func (u *Uploader) Upload(ctx context.Context, filePath, toUserID string) (UploadedFile, error) {
	if u == nil || u.cfg.Client == nil {
		return UploadedFile{}, fmt.Errorf("weixin uploader has no API client")
	}
	plaintext, err := os.ReadFile(filePath)
	if err != nil {
		return UploadedFile{}, fmt.Errorf("read media file: %w", err)
	}

	fileName := filepath.Base(filePath)
	mediaType := MediaTypeForFile(fileName)
	rawSize := int64(len(plaintext))
	rawMD5 := md5.Sum(plaintext)
	cipherSize := AESECBPaddedSize(rawSize)

	fileKey, err := randomHex(16)
	if err != nil {
		return UploadedFile{}, err
	}
	aesKey := make([]byte, 16)
	if _, err := rand.Read(aesKey); err != nil {
		return UploadedFile{}, fmt.Errorf("generate media key: %w", err)
	}

	resp, err := u.cfg.Client.GetUploadURL(ctx, GetUploadURLReq{
		FileKey:     fileKey,
		MediaType:   mediaType,
		ToUserID:    toUserID,
		RawSize:     rawSize,
		RawFileMD5:  hex.EncodeToString(rawMD5[:]),
		FileSize:    cipherSize,
		NoNeedThumb: true,
		// The server expects the key hex-encoded here, and base64-encoded on
		// the message item that references the upload.
		AESKey: hex.EncodeToString(aesKey),
	})
	if err != nil {
		return UploadedFile{}, err
	}

	uploadURL := strings.TrimSpace(resp.UploadFullURL)
	if uploadURL == "" {
		if strings.TrimSpace(resp.UploadParam) == "" {
			return UploadedFile{}, fmt.Errorf("weixin getuploadurl returned neither upload_full_url nor upload_param")
		}
		uploadURL = u.uploadURL(resp.UploadParam, fileKey)
	}

	ciphertext, err := EncryptAESECB(plaintext, aesKey)
	if err != nil {
		return UploadedFile{}, err
	}

	downloadParam, err := u.postToCDN(ctx, uploadURL, ciphertext)
	if err != nil {
		return UploadedFile{}, err
	}

	return UploadedFile{
		FileKey:                     fileKey,
		DownloadEncryptedQueryParam: downloadParam,
		AESKey:                      aesKey,
		FileSize:                    rawSize,
		FileSizeCiphertext:          cipherSize,
		FileName:                    fileName,
		MediaType:                   mediaType,
	}, nil
}

// postToCDN uploads the ciphertext and returns the download parameter the CDN
// reports in the x-encrypted-param response header.
func (u *Uploader) postToCDN(ctx context.Context, uploadURL string, ciphertext []byte) (string, error) {
	var lastErr error
	for attempt := 1; attempt <= uploadMaxRetries; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, uploadURL, bytes.NewReader(ciphertext))
		if err != nil {
			return "", fmt.Errorf("build CDN upload request: %w", err)
		}
		req.Header.Set("Content-Type", "application/octet-stream")
		req.ContentLength = int64(len(ciphertext))

		resp, err := u.cfg.HTTPClient.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("CDN upload %s: %w", RedactURL(uploadURL), err)
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			u.logRetry(attempt, lastErr)
			continue
		}

		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		downloadParam := resp.Header.Get("x-encrypted-param")
		errMessage := resp.Header.Get("x-error-message")
		resp.Body.Close()

		if resp.StatusCode >= 400 && resp.StatusCode < 500 {
			// A client error will not improve on a retry.
			if errMessage == "" {
				errMessage = TruncateForLog(string(body), 200)
			}
			return "", fmt.Errorf("CDN upload client error %d: %s", resp.StatusCode, errMessage)
		}
		if resp.StatusCode != http.StatusOK {
			if errMessage == "" {
				errMessage = fmt.Sprintf("status %d", resp.StatusCode)
			}
			lastErr = fmt.Errorf("CDN upload server error: %s", errMessage)
			u.logRetry(attempt, lastErr)
			continue
		}
		if downloadParam == "" {
			lastErr = fmt.Errorf("CDN upload response is missing the x-encrypted-param header")
			u.logRetry(attempt, lastErr)
			continue
		}
		return downloadParam, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("CDN upload failed after %d attempts", uploadMaxRetries)
	}
	return "", lastErr
}

func (u *Uploader) logRetry(attempt int, err error) {
	if u.cfg.Logger == nil {
		return
	}
	if attempt < uploadMaxRetries {
		u.cfg.Logger.Printf("CDN upload attempt %d/%d failed, retrying: %v", attempt, uploadMaxRetries, err)
		return
	}
	u.cfg.Logger.Printf("CDN upload failed after %d attempts: %v", uploadMaxRetries, err)
}

func (u *Uploader) uploadURL(uploadParam, fileKey string) string {
	base := strings.TrimRight(u.cfg.CDNBaseURL, "/")
	return base + "/upload?encrypted_query_param=" + url.QueryEscape(uploadParam) +
		"&filekey=" + url.QueryEscape(fileKey)
}

// MessageItemForUpload builds the outbound item that references an upload.
func MessageItemForUpload(uploaded UploadedFile) MessageItem {
	media := &CDNMedia{
		EncryptQueryParam: uploaded.DownloadEncryptedQueryParam,
		AESKey:            uploaded.AESKeyBase64(),
		EncryptType:       1,
	}
	switch uploaded.MediaType {
	case UploadMediaTypeImage:
		return MessageItem{
			Type:      ItemTypeImage,
			ImageItem: &ImageItem{Media: media, MidSize: uploaded.FileSizeCiphertext},
		}
	case UploadMediaTypeVideo:
		return MessageItem{
			Type:      ItemTypeVideo,
			VideoItem: &VideoItem{Media: media, VideoSize: uploaded.FileSizeCiphertext},
		}
	default:
		return MessageItem{
			Type: ItemTypeFile,
			FileItem: &FileItem{
				Media:    media,
				FileName: uploaded.FileName,
				Len:      fmt.Sprintf("%d", uploaded.FileSize),
			},
		}
	}
}

func randomHex(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate random bytes: %w", err)
	}
	return hex.EncodeToString(buf), nil
}
