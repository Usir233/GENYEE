package wechat

import (
	"bytes"
	"context"
	"crypto/aes"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/Usir233/GENYEE/internal/im"
	secutils "github.com/Usir233/GENYEE/internal/utils"
)

// A WeChat attachment may be up to maxIMAttachmentBytes, which is larger than
// the callback-body cap. The encrypted download has to be bounded separately;
// otherwise a valid attachment between the two limits is rejected.
func TestDownloadFileAcceptsEncryptedAttachmentAboveCallbackCap(t *testing.T) {
	secutils.SetSSRFWhitelistFromRaw("127.0.0.1")
	t.Cleanup(func() { secutils.SetSSRFWhitelistFromRaw("") })

	const plaintextSize = 5 << 20
	plaintext := bytes.Repeat([]byte("a"), plaintextSize)
	key := []byte("0123456789abcdef")
	ciphertext := encryptAES128ECB(t, plaintext, key)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if _, err := w.Write(ciphertext); err != nil {
			t.Errorf("write ciphertext: %v", err)
		}
	}))
	t.Cleanup(server.Close)

	adapter := &Adapter{}
	reader, fileName, err := adapter.DownloadFile(context.Background(), &im.IncomingMessage{
		FileKey:  server.URL,
		FileName: "large.pdf",
		FileSize: plaintextSize,
		Extra:    map[string]string{"aes_key": base64.StdEncoding.EncodeToString(key)},
	})
	if err != nil {
		t.Fatalf("DownloadFile() = %v, want nil for a %d byte attachment", err, plaintextSize)
	}
	defer func() { _ = reader.Close() }()

	if fileName != "large.pdf" {
		t.Errorf("fileName = %q, want %q", fileName, "large.pdf")
	}
	got, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("ReadAll() = %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Errorf("decrypted attachment differs from the %d byte plaintext", plaintextSize)
	}
}

func encryptAES128ECB(t *testing.T, plaintext, key []byte) []byte {
	t.Helper()

	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatalf("aes.NewCipher() = %v", err)
	}
	paddedSize := (len(plaintext)/block.BlockSize() + 1) * block.BlockSize()
	padded := make([]byte, paddedSize)
	copy(padded, plaintext)
	for i := len(plaintext); i < paddedSize; i++ {
		padded[i] = byte(paddedSize - len(plaintext))
	}

	ciphertext := make([]byte, paddedSize)
	for i := 0; i < paddedSize; i += block.BlockSize() {
		block.Encrypt(ciphertext[i:i+block.BlockSize()], padded[i:i+block.BlockSize()])
	}
	return ciphertext
}

type failingRandReader struct{}

func (failingRandReader) Read([]byte) (int, error) {
	return 0, errors.New("entropy unavailable")
}

func TestGenerateWeChatUINFallbackOnRandFailure(t *testing.T) {
	origReader := wechatRandReader
	wechatRandReader = failingRandReader{}
	t.Cleanup(func() { wechatRandReader = origReader })

	uin := generateWeChatUIN()
	decoded, err := base64.StdEncoding.DecodeString(uin)
	if err != nil {
		t.Fatalf("DecodeString(%q): %v", uin, err)
	}
	val, err := strconv.ParseUint(string(decoded), 10, 32)
	if err != nil || val == 0 {
		t.Fatalf("expected non-zero fallback UIN when crypto/rand fails, got %q (%v)", string(decoded), err)
	}
}

type endlessWeChatReader struct {
	remaining int64
}

func (r *endlessWeChatReader) Read(p []byte) (int, error) {
	if r.remaining <= 0 {
		return 0, io.EOF
	}
	if int64(len(p)) > r.remaining {
		p = p[:r.remaining]
	}
	for i := range p {
		p[i] = 'w'
	}
	r.remaining -= int64(len(p))
	return len(p), nil
}

func TestDownloadFileRejectsOversizedUnencryptedStream(t *testing.T) {
	secutils.SetSSRFWhitelistFromRaw("127.0.0.1")
	t.Cleanup(func() { secutils.SetSSRFWhitelistFromRaw("") })

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.Copy(w, &endlessWeChatReader{remaining: 33 << 20})
	}))
	t.Cleanup(server.Close)

	adapter := &Adapter{}
	rc, _, err := adapter.DownloadFile(context.Background(), &im.IncomingMessage{
		FileKey:  server.URL,
		FileName: "raw.bin",
	})
	if err != nil {
		t.Fatalf("DownloadFile: %v", err)
	}
	defer func() { _ = rc.Close() }()

	if _, err := io.ReadAll(rc); err == nil {
		t.Fatal("expected oversized unencrypted WeChat attachment download to fail, got nil")
	}
}
