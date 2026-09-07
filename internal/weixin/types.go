package weixin

// Weixin (WeChat) iLink bot protocol types. These mirror the wire shapes used
// by the official Tencent OpenClaw channel plugin (`src/api/types.ts`): JSON
// over HTTP, with protobuf `bytes` fields carried as base64 strings.

// Message item types (proto: MessageItemType).
const (
	ItemTypeNone           = 0
	ItemTypeText           = 1
	ItemTypeImage          = 2
	ItemTypeVoice          = 3
	ItemTypeFile           = 4
	ItemTypeVideo          = 5
	ItemTypeToolCallStart  = 11
	ItemTypeToolCallResult = 12
)

// Message direction (proto: MessageType).
const (
	MessageTypeNone = 0
	MessageTypeUser = 1
	MessageTypeBot  = 2
)

// Message lifecycle state (proto: MessageState).
const (
	MessageStateNew        = 0
	MessageStateGenerating = 1
	MessageStateFinish     = 2
)

// Typing indicator status for sendtyping.
const (
	TypingStatusTyping = 1
	TypingStatusCancel = 2
)

// Upload media types for getuploadurl (proto: UploadMediaType).
const (
	UploadMediaTypeImage = 1
	UploadMediaTypeVideo = 2
	UploadMediaTypeFile  = 3
	UploadMediaTypeVoice = 4
)

// StaleTokenErrCode is returned by the server when the bot token has expired.
const StaleTokenErrCode = -14

// BaseInfo is attached to every outgoing CGI request. Both fields are for
// server-side observability only; neither authenticates or routes.
type BaseInfo struct {
	ChannelVersion string `json:"channel_version,omitempty"`
	BotAgent       string `json:"bot_agent,omitempty"`
}

// TextItem carries plain message text.
type TextItem struct {
	Text string `json:"text,omitempty"`
}

// CDNMedia is a CDN media reference. AESKey is base64-encoded bytes in JSON.
type CDNMedia struct {
	EncryptQueryParam string `json:"encrypt_query_param,omitempty"`
	AESKey            string `json:"aes_key,omitempty"`
	// EncryptType 0 encrypts only the fileid, 1 packs thumbnail/mid-image info.
	EncryptType int    `json:"encrypt_type,omitempty"`
	FullURL     string `json:"full_url,omitempty"`
}

// ImageItem is an inbound or outbound image.
type ImageItem struct {
	Media      *CDNMedia `json:"media,omitempty"`
	ThumbMedia *CDNMedia `json:"thumb_media,omitempty"`
	// AESKey is a raw AES-128 key as a hex string; preferred over Media.AESKey
	// for inbound decryption when present.
	AESKey      string `json:"aeskey,omitempty"`
	URL         string `json:"url,omitempty"`
	MidSize     int64  `json:"mid_size,omitempty"`
	ThumbSize   int64  `json:"thumb_size,omitempty"`
	ThumbHeight int    `json:"thumb_height,omitempty"`
	ThumbWidth  int    `json:"thumb_width,omitempty"`
	HDSize      int64  `json:"hd_size,omitempty"`
}

// VoiceItem is an inbound voice clip. Text holds server-side ASR output.
type VoiceItem struct {
	Media *CDNMedia `json:"media,omitempty"`
	// EncodeType: 1=pcm 2=adpcm 3=feature 4=speex 5=amr 6=silk 7=mp3 8=ogg-speex
	EncodeType    int    `json:"encode_type,omitempty"`
	BitsPerSample int    `json:"bits_per_sample,omitempty"`
	SampleRate    int    `json:"sample_rate,omitempty"`
	PlayTime      int    `json:"playtime,omitempty"`
	Text          string `json:"text,omitempty"`
}

// FileItem is a file attachment. Len is the plaintext size as a string.
type FileItem struct {
	Media    *CDNMedia `json:"media,omitempty"`
	FileName string    `json:"file_name,omitempty"`
	MD5      string    `json:"md5,omitempty"`
	Len      string    `json:"len,omitempty"`
}

// VideoItem is a video attachment.
type VideoItem struct {
	Media       *CDNMedia `json:"media,omitempty"`
	VideoSize   int64     `json:"video_size,omitempty"`
	PlayLength  int       `json:"play_length,omitempty"`
	VideoMD5    string    `json:"video_md5,omitempty"`
	ThumbMedia  *CDNMedia `json:"thumb_media,omitempty"`
	ThumbSize   int64     `json:"thumb_size,omitempty"`
	ThumbHeight int       `json:"thumb_height,omitempty"`
	ThumbWidth  int       `json:"thumb_width,omitempty"`
}

// RefMessage is a quoted message attached to a text item.
type RefMessage struct {
	MessageItem *MessageItem `json:"message_item,omitempty"`
	Title       string       `json:"title,omitempty"`
}

// ToolCallStartItem announces a tool invocation.
type ToolCallStartItem struct {
	ToolName   string `json:"tool_name,omitempty"`
	ToolCallID string `json:"tool_call_id,omitempty"`
}

// ToolCallResultItem reports a tool invocation result.
type ToolCallResultItem struct {
	ToolName   string `json:"tool_name,omitempty"`
	ToolCallID string `json:"tool_call_id,omitempty"`
	Status     string `json:"status,omitempty"`
}

// MessageItem is one element of a message's item_list.
type MessageItem struct {
	Type               int                 `json:"type,omitempty"`
	CreateTimeMS       int64               `json:"create_time_ms,omitempty"`
	UpdateTimeMS       int64               `json:"update_time_ms,omitempty"`
	IsCompleted        bool                `json:"is_completed,omitempty"`
	MsgID              string              `json:"msg_id,omitempty"`
	RefMsg             *RefMessage         `json:"ref_msg,omitempty"`
	TextItem           *TextItem           `json:"text_item,omitempty"`
	ImageItem          *ImageItem          `json:"image_item,omitempty"`
	VoiceItem          *VoiceItem          `json:"voice_item,omitempty"`
	FileItem           *FileItem           `json:"file_item,omitempty"`
	VideoItem          *VideoItem          `json:"video_item,omitempty"`
	ToolCallStartItem  *ToolCallStartItem  `json:"tool_call_start_item,omitempty"`
	ToolCallResultItem *ToolCallResultItem `json:"tool_call_result_item,omitempty"`
}

// Message is the unified inbound/outbound message (proto: WeixinMessage).
type Message struct {
	Seq          int64         `json:"seq,omitempty"`
	MessageID    int64         `json:"message_id,omitempty"`
	FromUserID   string        `json:"from_user_id"`
	ToUserID     string        `json:"to_user_id,omitempty"`
	ClientID     string        `json:"client_id,omitempty"`
	CreateTimeMS int64         `json:"create_time_ms,omitempty"`
	UpdateTimeMS int64         `json:"update_time_ms,omitempty"`
	DeleteTimeMS int64         `json:"delete_time_ms,omitempty"`
	SessionID    string        `json:"session_id,omitempty"`
	GroupID      string        `json:"group_id,omitempty"`
	MessageType  int           `json:"message_type,omitempty"`
	MessageState int           `json:"message_state,omitempty"`
	ItemList     []MessageItem `json:"item_list,omitempty"`
	ContextToken string        `json:"context_token,omitempty"`
	RunID        string        `json:"run_id,omitempty"`
}

// GetUpdatesReq long-polls for new messages.
type GetUpdatesReq struct {
	GetUpdatesBuf string    `json:"get_updates_buf"`
	BaseInfo      *BaseInfo `json:"base_info,omitempty"`
}

// GetUpdatesResp carries new messages plus the next cursor.
type GetUpdatesResp struct {
	Ret     int       `json:"ret,omitempty"`
	ErrCode int       `json:"errcode,omitempty"`
	ErrMsg  string    `json:"errmsg,omitempty"`
	Msgs    []Message `json:"msgs,omitempty"`
	// GetUpdatesBuf is the cursor to cache locally and echo on the next poll.
	GetUpdatesBuf string `json:"get_updates_buf,omitempty"`
	// LongPollingTimeoutMS is the server-suggested timeout for the next poll.
	LongPollingTimeoutMS int `json:"longpolling_timeout_ms,omitempty"`
}

// SendMessageReq wraps a single outbound message.
type SendMessageReq struct {
	Msg      *Message  `json:"msg,omitempty"`
	BaseInfo *BaseInfo `json:"base_info,omitempty"`
}

// SendMessageResp is the sendmessage result.
type SendMessageResp struct {
	Ret    int    `json:"ret,omitempty"`
	ErrMsg string `json:"errmsg,omitempty"`
}

// SendTypingReq drives the typing indicator for one user.
type SendTypingReq struct {
	ILinkUserID  string    `json:"ilink_user_id,omitempty"`
	TypingTicket string    `json:"typing_ticket,omitempty"`
	Status       int       `json:"status,omitempty"`
	BaseInfo     *BaseInfo `json:"base_info,omitempty"`
}

// SendTypingResp is the sendtyping result.
type SendTypingResp struct {
	Ret    int    `json:"ret,omitempty"`
	ErrMsg string `json:"errmsg,omitempty"`
}

// GetConfigReq requests per-user bot config.
type GetConfigReq struct {
	ILinkUserID  string    `json:"ilink_user_id,omitempty"`
	ContextToken string    `json:"context_token,omitempty"`
	BaseInfo     *BaseInfo `json:"base_info,omitempty"`
}

// GetConfigResp carries the per-user typing ticket.
type GetConfigResp struct {
	Ret          int    `json:"ret,omitempty"`
	ErrMsg       string `json:"errmsg,omitempty"`
	TypingTicket string `json:"typing_ticket,omitempty"`
}

// NotifyReq announces client startup or shutdown.
type NotifyReq struct {
	BaseInfo *BaseInfo `json:"base_info,omitempty"`
}

// NotifyResp is the notifystart/notifystop result.
type NotifyResp struct {
	Ret    int    `json:"ret,omitempty"`
	ErrMsg string `json:"errmsg,omitempty"`
}

// GetUploadURLReq requests pre-signed CDN upload parameters.
type GetUploadURLReq struct {
	FileKey    string `json:"filekey,omitempty"`
	MediaType  int    `json:"media_type,omitempty"`
	ToUserID   string `json:"to_user_id,omitempty"`
	RawSize    int64  `json:"rawsize,omitempty"`
	RawFileMD5 string `json:"rawfilemd5,omitempty"`
	// FileSize is the AES-128-ECB/PKCS7 padded ciphertext size.
	FileSize      int64     `json:"filesize,omitempty"`
	ThumbRawSize  int64     `json:"thumb_rawsize,omitempty"`
	ThumbRawFile  string    `json:"thumb_rawfilemd5,omitempty"`
	ThumbFileSize int64     `json:"thumb_filesize,omitempty"`
	NoNeedThumb   bool      `json:"no_need_thumb,omitempty"`
	AESKey        string    `json:"aeskey,omitempty"`
	BaseInfo      *BaseInfo `json:"base_info,omitempty"`
}

// GetUploadURLResp carries the CDN upload target.
type GetUploadURLResp struct {
	Ret              int    `json:"ret,omitempty"`
	ErrMsg           string `json:"errmsg,omitempty"`
	UploadParam      string `json:"upload_param,omitempty"`
	ThumbUploadParam string `json:"thumb_upload_param,omitempty"`
	UploadFullURL    string `json:"upload_full_url,omitempty"`
}

// QRCodeResp is the get_bot_qrcode result. QRCodeImgContent is the URL to
// encode into the terminal QR image.
type QRCodeResp struct {
	QRCode           string `json:"qrcode"`
	QRCodeImgContent string `json:"qrcode_img_content"`
}

// QRCodeReq asks the server for a login QR, offering already-bound tokens so
// an existing binding can be recognized (producing `binded_redirect`).
type QRCodeReq struct {
	LocalTokenList []string  `json:"local_token_list"`
	BaseInfo       *BaseInfo `json:"base_info,omitempty"`
}

// QR login status values returned by get_qrcode_status.
const (
	QRStatusWait               = "wait"
	QRStatusScanned            = "scaned"
	QRStatusConfirmed          = "confirmed"
	QRStatusExpired            = "expired"
	QRStatusScannedButRedirect = "scaned_but_redirect"
	QRStatusNeedVerifyCode     = "need_verifycode"
	QRStatusVerifyCodeBlocked  = "verify_code_blocked"
	QRStatusBindedRedirect     = "binded_redirect"
)

// QRStatusResp is one long-poll result from get_qrcode_status.
type QRStatusResp struct {
	Status       string `json:"status"`
	BotToken     string `json:"bot_token,omitempty"`
	ILinkBotID   string `json:"ilink_bot_id,omitempty"`
	BaseURL      string `json:"baseurl,omitempty"`
	ILinkUserID  string `json:"ilink_user_id,omitempty"`
	RedirectHost string `json:"redirect_host,omitempty"`
}
