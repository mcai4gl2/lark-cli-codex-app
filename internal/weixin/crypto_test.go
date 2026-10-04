package weixin

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"strings"
	"testing"
)

func TestAESECBRoundTrip(t *testing.T) {
	key := []byte("0123456789abcdef")
	cases := []string{
		"",
		"short",
		"exactly-16-bytes",
		strings.Repeat("payload ", 100),
		"多字节内容测试",
	}
	for _, plaintext := range cases {
		ciphertext, err := EncryptAESECB([]byte(plaintext), key)
		if err != nil {
			t.Fatalf("EncryptAESECB(%q) error = %v", plaintext, err)
		}
		if len(ciphertext)%16 != 0 {
			t.Fatalf("ciphertext length %d is not block aligned", len(ciphertext))
		}
		if int64(len(ciphertext)) != AESECBPaddedSize(int64(len(plaintext))) {
			t.Fatalf("ciphertext length %d disagrees with AESECBPaddedSize(%d) = %d",
				len(ciphertext), len(plaintext), AESECBPaddedSize(int64(len(plaintext))))
		}
		decrypted, err := DecryptAESECB(ciphertext, key)
		if err != nil {
			t.Fatalf("DecryptAESECB() error = %v", err)
		}
		if string(decrypted) != plaintext {
			t.Fatalf("round trip returned %q, want %q", decrypted, plaintext)
		}
	}
}

func TestAESECBIsBlockDeterministic(t *testing.T) {
	// ECB encrypts identical blocks identically; the property is what makes the
	// mode weak, and confirming it here proves the block loop is really ECB.
	key := []byte("0123456789abcdef")
	block := []byte("AAAAAAAAAAAAAAAA")
	ciphertext, err := EncryptAESECB(append(append([]byte{}, block...), block...), key)
	if err != nil {
		t.Fatalf("EncryptAESECB() error = %v", err)
	}
	if !bytes.Equal(ciphertext[0:16], ciphertext[16:32]) {
		t.Fatalf("identical plaintext blocks produced different ciphertext blocks")
	}
}

func TestAESECBPaddedSize(t *testing.T) {
	cases := map[int64]int64{
		0:  16,
		1:  16,
		15: 16,
		// PKCS7 always appends padding, so a full block grows by another block.
		16:   32,
		17:   32,
		31:   32,
		32:   48,
		1000: 1008,
	}
	for input, want := range cases {
		if got := AESECBPaddedSize(input); got != want {
			t.Fatalf("AESECBPaddedSize(%d) = %d, want %d", input, got, want)
		}
	}
	if got := AESECBPaddedSize(-5); got != 0 {
		t.Fatalf("AESECBPaddedSize(-5) = %d", got)
	}
}

func TestParseAESKeyAcceptsBothEncodings(t *testing.T) {
	raw := []byte("0123456789abcdef")

	// Images: base64 of the 16 raw key bytes.
	rawEncoded := base64.StdEncoding.EncodeToString(raw)
	key, err := ParseAESKey(rawEncoded)
	if err != nil {
		t.Fatalf("ParseAESKey(raw) error = %v", err)
	}
	if !bytes.Equal(key, raw) {
		t.Fatalf("ParseAESKey(raw) = %x, want %x", key, raw)
	}

	// Files, voice, video: base64 of a 32-character hex string.
	hexEncoded := base64.StdEncoding.EncodeToString([]byte(hex.EncodeToString(raw)))
	key, err = ParseAESKey(hexEncoded)
	if err != nil {
		t.Fatalf("ParseAESKey(hex) error = %v", err)
	}
	if !bytes.Equal(key, raw) {
		t.Fatalf("ParseAESKey(hex) = %x, want %x", key, raw)
	}
}

func TestParseAESKeyRejectsBadInput(t *testing.T) {
	cases := map[string]string{
		"not base64":       "!!!not base64!!!",
		"wrong length":     base64.StdEncoding.EncodeToString([]byte("too short")),
		"32 bytes non-hex": base64.StdEncoding.EncodeToString([]byte("zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz")),
	}
	for name, input := range cases {
		if _, err := ParseAESKey(input); err == nil {
			t.Fatalf("ParseAESKey(%s) should fail", name)
		}
	}
}

func TestDecryptAESECBRejectsMalformedCiphertext(t *testing.T) {
	key := []byte("0123456789abcdef")
	if _, err := DecryptAESECB([]byte("not-block-aligned"), key); err == nil {
		t.Fatalf("misaligned ciphertext should fail")
	}
	if _, err := DecryptAESECB(nil, key); err == nil {
		t.Fatalf("empty ciphertext should fail")
	}
	if _, err := DecryptAESECB(bytes.Repeat([]byte{0}, 16), []byte("short-key")); err == nil {
		t.Fatalf("a bad key length should fail")
	}

	// Valid blocks whose trailing bytes are not valid PKCS7 padding.
	garbage, err := EncryptAESECB([]byte("hello"), key)
	if err != nil {
		t.Fatalf("EncryptAESECB() error = %v", err)
	}
	garbage[len(garbage)-1] ^= 0xff
	if _, err := DecryptAESECB(garbage, key); err == nil {
		t.Fatalf("corrupt padding should fail")
	}
}
