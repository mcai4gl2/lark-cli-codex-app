package weixin

import (
	"path/filepath"
	"strings"
)

var extensionToMIME = map[string]string{
	".pdf":  "application/pdf",
	".doc":  "application/msword",
	".docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
	".xls":  "application/vnd.ms-excel",
	".xlsx": "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
	".ppt":  "application/vnd.ms-powerpoint",
	".pptx": "application/vnd.openxmlformats-officedocument.presentationml.presentation",
	".txt":  "text/plain",
	".md":   "text/markdown",
	".csv":  "text/csv",
	".json": "application/json",
	".zip":  "application/zip",
	".tar":  "application/x-tar",
	".gz":   "application/gzip",
	".mp3":  "audio/mpeg",
	".ogg":  "audio/ogg",
	".wav":  "audio/wav",
	".mp4":  "video/mp4",
	".mov":  "video/quicktime",
	".webm": "video/webm",
	".mkv":  "video/x-matroska",
	".avi":  "video/x-msvideo",
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".gif":  "image/gif",
	".webp": "image/webp",
	".bmp":  "image/bmp",
}

// MIMEFromFilename guesses a content type from a file name extension.
func MIMEFromFilename(name string) string {
	ext := strings.ToLower(filepath.Ext(strings.TrimSpace(name)))
	if mime, ok := extensionToMIME[ext]; ok {
		return mime
	}
	return "application/octet-stream"
}

// MediaTypeForFile picks the upload media type the server expects for a file,
// based on its extension.
func MediaTypeForFile(name string) int {
	mime := MIMEFromFilename(name)
	switch {
	case strings.HasPrefix(mime, "image/"):
		return UploadMediaTypeImage
	case strings.HasPrefix(mime, "video/"):
		return UploadMediaTypeVideo
	default:
		return UploadMediaTypeFile
	}
}
