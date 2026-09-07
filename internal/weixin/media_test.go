package weixin

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// encryptedFixture returns AES-128-ECB ciphertext plus the wire-format key.
func encryptedFixture(t *testing.T, plaintext string, hexEncodedKey bool) ([]byte, string) {
	t.Helper()
	key := []byte("fedcba9876543210")
	ciphertext, err := EncryptAESECB([]byte(plaintext), key)
	if err != nil {
		t.Fatalf("EncryptAESECB() error = %v", err)
	}
	if hexEncodedKey {
		return ciphertext, base64.StdEncoding.EncodeToString([]byte(hex.EncodeToString(key)))
	}
	return ciphertext, base64.StdEncoding.EncodeToString(key)
}

func TestMediaDownloaderDecryptsBothKeyEncodings(t *testing.T) {
	imageCipher, imageKey := encryptedFixture(t, "image-bytes", false)
	fileCipher, fileKey := encryptedFixture(t, "file-bytes", true)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("encrypted_query_param") {
		case "image-param":
			_, _ = w.Write(imageCipher)
		case "file-param":
			_, _ = w.Write(fileCipher)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	dir := t.TempDir()
	downloader := NewMediaDownloader(MediaDownloaderConfig{
		CDNBaseURL: server.URL,
		Dir:        dir,
		Client:     server.Client(),
	})

	paths := downloader.Download(context.Background(), []MessageItem{
		{Type: ItemTypeImage, ImageItem: &ImageItem{
			Media: &CDNMedia{EncryptQueryParam: "image-param", AESKey: imageKey},
		}},
		{Type: ItemTypeFile, FileItem: &FileItem{
			FileName: "report.pdf",
			Media:    &CDNMedia{EncryptQueryParam: "file-param", AESKey: fileKey},
		}},
	})

	if len(paths) != 2 {
		t.Fatalf("saved %d files, want 2", len(paths))
	}
	assertFileContent(t, paths[0], "image-bytes")
	assertFileContent(t, paths[1], "file-bytes")
	if !strings.HasSuffix(paths[1], ".pdf") {
		t.Fatalf("file attachment lost its extension: %q", paths[1])
	}
	for _, path := range paths {
		if filepath.Dir(path) != dir {
			t.Fatalf("file %q escaped the media directory %q", path, dir)
		}
	}
}

func TestMediaDownloaderPrefersTheImageItemHexKey(t *testing.T) {
	ciphertext, _ := encryptedFixture(t, "image-bytes", false)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(ciphertext)
	}))
	defer server.Close()

	downloader := NewMediaDownloader(MediaDownloaderConfig{
		CDNBaseURL: server.URL,
		Dir:        t.TempDir(),
		Client:     server.Client(),
	})
	paths := downloader.Download(context.Background(), []MessageItem{
		{Type: ItemTypeImage, ImageItem: &ImageItem{
			// The raw hex key on the item wins over a wrong key on the media ref.
			AESKey: hex.EncodeToString([]byte("fedcba9876543210")),
			Media: &CDNMedia{
				EncryptQueryParam: "p",
				AESKey:            base64.StdEncoding.EncodeToString([]byte("0000000000000000")),
			},
		}},
	})
	if len(paths) != 1 {
		t.Fatalf("saved %d files, want 1", len(paths))
	}
	assertFileContent(t, paths[0], "image-bytes")
}

func TestMediaDownloaderUsesFullURLWhenPresent(t *testing.T) {
	ciphertext, key := encryptedFixture(t, "video-bytes", false)
	var requestedPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestedPath = r.URL.Path
		_, _ = w.Write(ciphertext)
	}))
	defer server.Close()

	downloader := NewMediaDownloader(MediaDownloaderConfig{
		CDNBaseURL: "https://unused.example.test",
		Dir:        t.TempDir(),
		Client:     server.Client(),
	})
	paths := downloader.Download(context.Background(), []MessageItem{
		{Type: ItemTypeVideo, VideoItem: &VideoItem{
			Media: &CDNMedia{FullURL: server.URL + "/direct", AESKey: key},
		}},
	})
	if len(paths) != 1 {
		t.Fatalf("saved %d files, want 1", len(paths))
	}
	if requestedPath != "/direct" {
		t.Fatalf("requested %q; full_url should take precedence", requestedPath)
	}
	assertFileContent(t, paths[0], "video-bytes")
}

func TestMediaDownloaderSkipsFailuresAndVoice(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	downloader := NewMediaDownloader(MediaDownloaderConfig{
		CDNBaseURL: server.URL,
		Dir:        t.TempDir(),
		Client:     server.Client(),
	})
	paths := downloader.Download(context.Background(), []MessageItem{
		{Type: ItemTypeImage, ImageItem: &ImageItem{Media: &CDNMedia{EncryptQueryParam: "p", AESKey: "bad"}}},
		// Voice is never downloaded: SILK has no Go decoder, so the
		// server-side transcription is used instead.
		{Type: ItemTypeVoice, VoiceItem: &VoiceItem{Media: &CDNMedia{EncryptQueryParam: "p"}}},
		{Type: ItemTypeText, TextItem: &TextItem{Text: "hi"}},
	})
	if len(paths) != 0 {
		t.Fatalf("paths = %v, want none", paths)
	}
}

func TestMediaDownloaderEnforcesTheSizeCap(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(make([]byte, 4096))
	}))
	defer server.Close()

	downloader := NewMediaDownloader(MediaDownloaderConfig{
		CDNBaseURL: server.URL,
		Dir:        t.TempDir(),
		MaxBytes:   1024,
		Client:     server.Client(),
	})
	paths := downloader.Download(context.Background(), []MessageItem{
		{Type: ItemTypeVideo, VideoItem: &VideoItem{Media: &CDNMedia{EncryptQueryParam: "p"}}},
	})
	if len(paths) != 0 {
		t.Fatalf("an oversized object should be rejected, got %v", paths)
	}
}

func TestSanitizeFileStem(t *testing.T) {
	cases := map[string]string{
		"report":         "report",
		"../../etc/pass": "etc-pass",
		"报告":             "weixin-file",
		"":               "weixin-file",
		"a b c":          "a-b-c",
	}
	for input, want := range cases {
		if got := sanitizeFileStem(input); got != want {
			t.Fatalf("sanitizeFileStem(%q) = %q, want %q", input, got, want)
		}
	}
}

// cdnRecorder captures what an upload sent.
type cdnRecorder struct {
	mu         sync.Mutex
	bodies     [][]byte
	attempts   int
	failuntil  int
	clientErr  bool
	uploadPath string
}

func TestUploaderEncryptsAndReportsTheDownloadParam(t *testing.T) {
	recorder := &cdnRecorder{}
	var uploadReq GetUploadURLReq

	mux := http.NewServeMux()
	mux.HandleFunc("/"+endpointGetUploadURL, func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&uploadReq); err != nil {
			t.Errorf("decode getuploadurl body: %v", err)
		}
		writeJSON(t, w, GetUploadURLResp{Ret: 0, UploadFullURL: "http://" + r.Host + "/cdn/upload"})
	})
	mux.HandleFunc("/cdn/upload", func(w http.ResponseWriter, r *http.Request) {
		body := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(body)
		recorder.mu.Lock()
		recorder.attempts++
		recorder.bodies = append(recorder.bodies, body)
		recorder.mu.Unlock()
		if got := r.Header.Get("Content-Type"); got != "application/octet-stream" {
			t.Errorf("Content-Type = %q", got)
		}
		w.Header().Set("x-encrypted-param", "download-param-1")
		w.WriteHeader(http.StatusOK)
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	path := filepath.Join(t.TempDir(), "note.txt")
	if err := os.WriteFile(path, []byte("hello world"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	uploader := NewUploader(UploaderConfig{
		Client:     NewClient(ClientConfig{BaseURL: server.URL, Token: "tok", Client: server.Client()}),
		CDNBaseURL: server.URL + "/cdn",
		HTTPClient: server.Client(),
	})
	uploaded, err := uploader.Upload(context.Background(), path, "u1")
	if err != nil {
		t.Fatalf("Upload() error = %v", err)
	}

	if uploaded.DownloadEncryptedQueryParam != "download-param-1" {
		t.Fatalf("download param = %q", uploaded.DownloadEncryptedQueryParam)
	}
	if uploaded.FileSize != 11 {
		t.Fatalf("plaintext size = %d, want 11", uploaded.FileSize)
	}
	if uploaded.FileSizeCiphertext != AESECBPaddedSize(11) {
		t.Fatalf("ciphertext size = %d", uploaded.FileSizeCiphertext)
	}
	if uploadReq.ToUserID != "u1" || uploadReq.FileSize != AESECBPaddedSize(11) || !uploadReq.NoNeedThumb {
		t.Fatalf("getuploadurl request = %+v", uploadReq)
	}
	if len(uploadReq.AESKey) != 32 {
		t.Fatalf("aeskey should be hex-encoded on getuploadurl, got %q", uploadReq.AESKey)
	}
	if uploadReq.MediaType != UploadMediaTypeFile {
		t.Fatalf("media_type = %d, want file", uploadReq.MediaType)
	}

	// The CDN must receive ciphertext, not the plaintext file.
	recorder.mu.Lock()
	body := recorder.bodies[0]
	recorder.mu.Unlock()
	if strings.Contains(string(body), "hello world") {
		t.Fatalf("plaintext reached the CDN")
	}
	decrypted, err := DecryptAESECB(body, uploaded.AESKey)
	if err != nil {
		t.Fatalf("DecryptAESECB() error = %v", err)
	}
	if string(decrypted) != "hello world" {
		t.Fatalf("round trip returned %q", decrypted)
	}
}

func TestUploaderRetriesServerErrorsAndAbortsOnClientErrors(t *testing.T) {
	for _, tc := range []struct {
		name       string
		status     int
		wantTries  int
		wantErrHas string
	}{
		{"server error is retried", http.StatusBadGateway, uploadMaxRetries, "server error"},
		{"client error aborts immediately", http.StatusForbidden, 1, "client error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			attempts := 0
			mux := http.NewServeMux()
			mux.HandleFunc("/"+endpointGetUploadURL, func(w http.ResponseWriter, r *http.Request) {
				writeJSON(t, w, GetUploadURLResp{Ret: 0, UploadFullURL: "http://" + r.Host + "/cdn/upload"})
			})
			mux.HandleFunc("/cdn/upload", func(w http.ResponseWriter, r *http.Request) {
				attempts++
				w.Header().Set("x-error-message", "nope")
				w.WriteHeader(tc.status)
			})
			server := httptest.NewServer(mux)
			defer server.Close()

			path := filepath.Join(t.TempDir(), "a.bin")
			if err := os.WriteFile(path, []byte("data"), 0o600); err != nil {
				t.Fatalf("write fixture: %v", err)
			}

			uploader := NewUploader(UploaderConfig{
				Client:     NewClient(ClientConfig{BaseURL: server.URL, Token: "tok", Client: server.Client()}),
				HTTPClient: server.Client(),
			})
			_, err := uploader.Upload(context.Background(), path, "u1")
			if err == nil || !strings.Contains(err.Error(), tc.wantErrHas) {
				t.Fatalf("Upload() error = %v, want one containing %q", err, tc.wantErrHas)
			}
			if attempts != tc.wantTries {
				t.Fatalf("attempts = %d, want %d", attempts, tc.wantTries)
			}
		})
	}
}

func TestUploaderRequiresAnUploadTarget(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/"+endpointGetUploadURL, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, GetUploadURLResp{Ret: 0})
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	path := filepath.Join(t.TempDir(), "a.bin")
	if err := os.WriteFile(path, []byte("data"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	uploader := NewUploader(UploaderConfig{
		Client:     NewClient(ClientConfig{BaseURL: server.URL, Token: "tok", Client: server.Client()}),
		HTTPClient: server.Client(),
	})
	if _, err := uploader.Upload(context.Background(), path, "u1"); err == nil {
		t.Fatalf("Upload() should fail when the server returns no upload target")
	}
}

func TestMessageItemForUpload(t *testing.T) {
	uploaded := UploadedFile{
		DownloadEncryptedQueryParam: "dp",
		AESKey:                      []byte("0123456789abcdef"),
		FileSize:                    11,
		FileSizeCiphertext:          16,
		FileName:                    "a.png",
		MediaType:                   UploadMediaTypeImage,
	}
	item := MessageItemForUpload(uploaded)
	if item.Type != ItemTypeImage || item.ImageItem == nil {
		t.Fatalf("item = %+v", item)
	}
	if item.ImageItem.Media.EncryptType != 1 || item.ImageItem.Media.EncryptQueryParam != "dp" {
		t.Fatalf("media = %+v", item.ImageItem.Media)
	}
	if item.ImageItem.Media.AESKey != base64.StdEncoding.EncodeToString(uploaded.AESKey) {
		t.Fatalf("aes_key must be base64 on the message item, got %q", item.ImageItem.Media.AESKey)
	}
	if item.ImageItem.MidSize != 16 {
		t.Fatalf("mid_size = %d", item.ImageItem.MidSize)
	}

	uploaded.MediaType = UploadMediaTypeVideo
	if item := MessageItemForUpload(uploaded); item.Type != ItemTypeVideo || item.VideoItem.VideoSize != 16 {
		t.Fatalf("video item = %+v", item)
	}

	uploaded.MediaType = UploadMediaTypeFile
	item = MessageItemForUpload(uploaded)
	if item.Type != ItemTypeFile || item.FileItem.FileName != "a.png" {
		t.Fatalf("file item = %+v", item)
	}
	if item.FileItem.Len != "11" {
		t.Fatalf("len should be the plaintext size, got %q", item.FileItem.Len)
	}
}

func TestMediaTypeForFile(t *testing.T) {
	cases := map[string]int{
		"a.png":  UploadMediaTypeImage,
		"a.jpeg": UploadMediaTypeImage,
		"a.mp4":  UploadMediaTypeVideo,
		"a.pdf":  UploadMediaTypeFile,
		"a":      UploadMediaTypeFile,
	}
	for name, want := range cases {
		if got := MediaTypeForFile(name); got != want {
			t.Fatalf("MediaTypeForFile(%q) = %d, want %d", name, got, want)
		}
	}
	if got := MIMEFromFilename("a.unknown"); got != "application/octet-stream" {
		t.Fatalf("MIMEFromFilename() = %q", got)
	}
}

func TestMessengerSendMediaSendsCaptionThenAttachment(t *testing.T) {
	sent := &sentMessages{}
	mux := http.NewServeMux()
	mux.HandleFunc("/"+endpointSendMessage, func(w http.ResponseWriter, r *http.Request) {
		var req SendMessageReq
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.Msg != nil {
			sent.add(*req.Msg)
		}
		writeJSON(t, w, SendMessageResp{Ret: 0})
	})
	mux.HandleFunc("/"+endpointGetUploadURL, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, GetUploadURLResp{Ret: 0, UploadFullURL: "http://" + r.Host + "/cdn/upload"})
	})
	mux.HandleFunc("/cdn/upload", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("x-encrypted-param", "dp")
		w.WriteHeader(http.StatusOK)
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	path := filepath.Join(t.TempDir(), "chart.png")
	if err := os.WriteFile(path, []byte("png-bytes"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	tokens := NewContextTokenStore(filepath.Join(t.TempDir(), "tokens.json"))
	if err := tokens.Set("u1", "ctx-1"); err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	messenger := NewMessenger(MessengerConfig{
		Client:        NewClient(ClientConfig{BaseURL: server.URL, Token: "tok", Client: server.Client()}),
		ContextTokens: tokens,
		Guard:         NewSessionGuard(),
		UploadClient:  server.Client(),
	})

	if err := messenger.SendMedia(context.Background(), "u1", "here is the chart", path); err != nil {
		t.Fatalf("SendMedia() error = %v", err)
	}

	msgs := sent.all()
	if len(msgs) != 2 {
		t.Fatalf("sent %d messages, want a caption plus the attachment", len(msgs))
	}
	if msgs[0].ItemList[0].Type != ItemTypeText || msgs[0].ItemList[0].TextItem.Text != "here is the chart" {
		t.Fatalf("first message = %+v", msgs[0].ItemList[0])
	}
	if msgs[1].ItemList[0].Type != ItemTypeImage {
		t.Fatalf("second message = %+v", msgs[1].ItemList[0])
	}
	for _, msg := range msgs {
		if msg.ContextToken != "ctx-1" {
			t.Fatalf("context token = %q", msg.ContextToken)
		}
		if len(msg.ItemList) != 1 {
			t.Fatalf("each request must carry exactly one item, got %d", len(msg.ItemList))
		}
	}
}

func assertFileContent(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if string(data) != want {
		t.Fatalf("file %s contains %q, want %q", path, data, want)
	}
}
