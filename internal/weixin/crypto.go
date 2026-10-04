package weixin

import (
	"bytes"
	"crypto/aes"
	"encoding/base64"
	"encoding/hex"
	"fmt"
)

// Weixin CDN media is AES-128-ECB with PKCS7 padding. Go's standard library
// deliberately ships no ECB mode, so the block loop is written out here.

// EncryptAESECB encrypts plaintext with AES-128-ECB and PKCS7 padding.
func EncryptAESECB(plaintext, key []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("weixin aes key: %w", err)
	}
	padded := pkcs7Pad(plaintext, block.BlockSize())
	out := make([]byte, len(padded))
	for offset := 0; offset < len(padded); offset += block.BlockSize() {
		block.Encrypt(out[offset:offset+block.BlockSize()], padded[offset:offset+block.BlockSize()])
	}
	return out, nil
}

// DecryptAESECB decrypts AES-128-ECB ciphertext and strips PKCS7 padding.
func DecryptAESECB(ciphertext, key []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("weixin aes key: %w", err)
	}
	size := block.BlockSize()
	if len(ciphertext) == 0 || len(ciphertext)%size != 0 {
		return nil, fmt.Errorf("weixin ciphertext length %d is not a multiple of the %d-byte block size", len(ciphertext), size)
	}
	out := make([]byte, len(ciphertext))
	for offset := 0; offset < len(ciphertext); offset += size {
		block.Decrypt(out[offset:offset+size], ciphertext[offset:offset+size])
	}
	return pkcs7Unpad(out, size)
}

// AESECBPaddedSize returns the ciphertext size for a plaintext of n bytes.
// PKCS7 always adds padding, so a whole-block plaintext still grows by a block.
func AESECBPaddedSize(n int64) int64 {
	if n < 0 {
		return 0
	}
	return ((n + 1 + 15) / 16) * 16
}

// ParseAESKey decodes CDNMedia.aes_key into a raw 16-byte key.
//
// Two encodings appear in the wild: base64 of the 16 raw key bytes (images),
// and base64 of a 32-character hex string (files, voice, video).
func ParseAESKey(aesKeyBase64 string) ([]byte, error) {
	decoded, err := base64.StdEncoding.DecodeString(aesKeyBase64)
	if err != nil {
		return nil, fmt.Errorf("weixin aes_key is not valid base64: %w", err)
	}
	switch len(decoded) {
	case 16:
		return decoded, nil
	case 32:
		key, hexErr := hex.DecodeString(string(decoded))
		if hexErr != nil {
			return nil, fmt.Errorf("weixin aes_key decoded to 32 bytes that are not hex: %w", hexErr)
		}
		return key, nil
	default:
		return nil, fmt.Errorf("weixin aes_key must decode to 16 raw bytes or a 32-character hex string, got %d bytes", len(decoded))
	}
}

func pkcs7Pad(data []byte, blockSize int) []byte {
	padding := blockSize - len(data)%blockSize
	return append(append([]byte{}, data...), bytes.Repeat([]byte{byte(padding)}, padding)...)
}

func pkcs7Unpad(data []byte, blockSize int) ([]byte, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("weixin plaintext is empty")
	}
	padding := int(data[len(data)-1])
	if padding == 0 || padding > blockSize || padding > len(data) {
		return nil, fmt.Errorf("weixin PKCS7 padding byte %d is out of range", padding)
	}
	for _, b := range data[len(data)-padding:] {
		if int(b) != padding {
			return nil, fmt.Errorf("weixin PKCS7 padding is inconsistent")
		}
	}
	return data[:len(data)-padding], nil
}
