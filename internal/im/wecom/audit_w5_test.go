package wecom

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/binary"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Usir233/GENYEE/internal/im"
)

func TestWebhookAdapterDecrypt_RejectsMsgLenUint32OverflowWithoutPanic(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	corpID := "ww_secret_corp_id"
	adapter := &WebhookAdapter{aesKey: key, corpID: corpID}

	// Craft WeCom plaintext: random(16) + msgLen(4) + payload where msgLen = 0xFFFFFFEC
	// overflows uint32(20 + msgLen) == 0, which previously bypassed the length check
	// and panicked with slice bounds out of range [20:0].
	plaintext := make([]byte, 24)
	binary.BigEndian.PutUint32(plaintext[16:20], 0xFFFFFFEC)
	padLen := wecomPKCS7BlockSize - len(plaintext)%wecomPKCS7BlockSize
	plaintext = append(plaintext, bytes.Repeat([]byte{byte(padLen)}, padLen)...)

	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatalf("aes.NewCipher: %v", err)
	}
	ciphertext := make([]byte, len(plaintext))
	cipher.NewCBCEncrypter(block, key[:aes.BlockSize]).CryptBlocks(ciphertext, plaintext)
	encoded := base64.StdEncoding.EncodeToString(ciphertext)

	if _, err := adapter.decrypt(encoded); err == nil {
		t.Fatal("expected error for overflowed msgLen, got nil")
	}

	// Also verify corp_id mismatch does not leak expected corpID in error text.
	wrongCorpEnc, _ := encryptWeComTestPayload(t, key, "ww_attacker_corp", []byte("<xml/>"))
	_, err = adapter.decrypt(wrongCorpEnc)
	if err == nil {
		t.Fatal("expected corp_id mismatch error, got nil")
	}
	if strings.Contains(err.Error(), corpID) {
		t.Fatalf("decrypt error leaked expected corpID %q: %v", corpID, err)
	}

	// NewWebhookAdapter must reject wrong-length EncodingAESKey early.
	if _, err := NewWebhookAdapter(corpID, "tok", "dG9vc2hvcnQ", "sec", 1000001, ""); err == nil {
		t.Fatal("expected NewWebhookAdapter to reject short encoding_aes_key")
	}
}

type wecomRoundTripFunc func(*http.Request) (*http.Response, error)

func (f wecomRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestWSAdapterDownloadFile_CapsSizeSupports32BytePaddingAndHTTPSAllowlist(t *testing.T) {
	// 1. isAllowedIMAPIHost must reject cleartext http://, file://, and userinfo URLs.
	for _, badURL := range []string{
		"http://qyapi.weixin.qq.com/cgi-bin/media/get",
		"file://qyapi.weixin.qq.com/etc/passwd",
		"https://user:pass@qyapi.weixin.qq.com/cgi-bin/media/get",
	} {
		if isAllowedIMAPIHost(badURL, "") {
			t.Errorf("isAllowedIMAPIHost(%q) = true, want false", badURL)
		}
	}
	if !isAllowedIMAPIHost("https://qyapi.weixin.qq.com/cgi-bin/media/get", "") {
		t.Fatal("expected https://qyapi.weixin.qq.com to be allowed")
	}

	// 2. decryptAESCBC must strip valid 17..32-byte WeCom PKCS#7 padding (e.g. 20 bytes).
	aesKey := []byte("0123456789abcdef0123456789abcdef")
	aesKeyB64 := strings.TrimRight(base64.StdEncoding.EncodeToString(aesKey), "=")
	rawFile := []byte("hello wecom!") // 12 bytes -> 20-byte pad to 32-byte block
	pad := 32 - len(rawFile)%32       // 20
	padded := append(append([]byte(nil), rawFile...), bytes.Repeat([]byte{byte(pad)}, pad)...)
	block, err := aes.NewCipher(aesKey)
	if err != nil {
		t.Fatalf("aes.NewCipher: %v", err)
	}
	enc := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, aesKey[:aes.BlockSize]).CryptBlocks(enc, padded)

	gotPlain, err := decryptAESCBC(enc, aesKeyB64)
	if err != nil {
		t.Fatalf("decryptAESCBC error: %v", err)
	}
	if !bytes.Equal(gotPlain, rawFile) {
		t.Fatalf("decryptAESCBC = %q (len=%d), want %q (len=%d)", gotPlain, len(gotPlain), rawFile, len(rawFile))
	}

	// 3. WSAdapter.DownloadFile must enforce maxIMAttachmentBytes instead of unbounded io.ReadAll.
	origClient := httpClient
	httpClient = &http.Client{
		Transport: wecomRoundTripFunc(func(_ *http.Request) (*http.Response, error) {
			// Stream slightly over 100 MiB of zeros on demand without allocating a 100 MiB buffer.
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(io.LimitReader(zeroReader{}, (100<<20)+1024)),
				Header:     make(http.Header),
			}, nil
		}),
	}
	t.Cleanup(func() { httpClient = origClient })

	wsAdapter := NewWSAdapter(&LongConnClient{})
	rc, _, err := wsAdapter.DownloadFile(context.Background(), &im.IncomingMessage{
		FileKey:  "https://qyapi.weixin.qq.com/media/oversized.bin",
		FileName: "oversized.bin",
		Extra:    map[string]string{"aes_key": aesKeyB64},
	})
	if err == nil {
		_ = rc.Close()
		t.Fatal("expected WSAdapter.DownloadFile to reject >100MiB encrypted stream, got nil")
	}
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}
