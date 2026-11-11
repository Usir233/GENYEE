package im_test

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Usir233/GENYEE/internal/im"
	"github.com/Usir233/GENYEE/internal/im/feishu"
	"github.com/Usir233/GENYEE/internal/im/wecom"
	"github.com/Usir233/GENYEE/internal/types"
	"github.com/gin-gonic/gin"
)

func encryptFeishuTestPayload(t *testing.T, encryptKey, plainJSON string) string {
	t.Helper()
	keyHash := sha256.Sum256([]byte(encryptKey))
	block, err := aes.NewCipher(keyHash[:])
	if err != nil {
		t.Fatalf("aes.NewCipher: %v", err)
	}
	padLen := aes.BlockSize - (len(plainJSON) % aes.BlockSize)
	padded := append([]byte(plainJSON), bytes.Repeat([]byte{byte(padLen)}, padLen)...)
	iv := []byte("0123456789abcdef")
	out := make([]byte, aes.BlockSize+len(padded))
	copy(out[:aes.BlockSize], iv)
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(out[aes.BlockSize:], padded)
	return base64.StdEncoding.EncodeToString(out)
}

func TestFeishuEncryptKeyBlocksPlaintextDowngradeAndEnforcesSignatureAndWindow(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const (
		verifyToken = "feishu-verify-token"
		encryptKey  = "feishu-encrypt-secret-key"
	)
	adapter, err := feishu.NewAdapter(feishu.RegionFeishu, "app-id", "app-secret", verifyToken, encryptKey, "")
	if err != nil {
		t.Fatalf("NewWebhookAdapter: %v", err)
	}

	t.Run("rejects plaintext payload when encryptKey is configured", func(t *testing.T) {
		body := `{"header":{"token":"` + verifyToken + `","event_type":"im.message.receive_v1"},"event":{}}`
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPost, "/im/callback", strings.NewReader(body))
		if err := adapter.VerifyCallback(c); err == nil {
			t.Fatal("expected plaintext callback to be rejected when encryptKey is configured")
		}
	})

	t.Run("rejects forged X-Lark-Signature and stale timestamps", func(t *testing.T) {
		plainEvent := `{"header":{"token":"` + verifyToken + `","event_type":"im.message.receive_v1"},"event":{}}`
		body := `{"encrypt":"` + encryptFeishuTestPayload(t, encryptKey, plainEvent) + `"}`
		staleTS := strconv.FormatInt(time.Now().Add(-10*time.Minute).Unix(), 10)
		nonce := "nonce-123"
		h := sha256.Sum256([]byte(staleTS + nonce + encryptKey + body))
		staleSig := hex.EncodeToString(h[:])

		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPost, "/im/callback", bytes.NewBufferString(body))
		c.Request.Header.Set("X-Lark-Request-Timestamp", staleTS)
		c.Request.Header.Set("X-Lark-Request-Nonce", nonce)
		c.Request.Header.Set("X-Lark-Signature", staleSig)
		if err := adapter.VerifyCallback(c); err == nil {
			t.Fatal("expected stale timestamp outside ±5m window to be rejected")
		}

		freshTS := strconv.FormatInt(time.Now().Unix(), 10)
		w2 := httptest.NewRecorder()
		c2, _ := gin.CreateTestContext(w2)
		c2.Request = httptest.NewRequest(http.MethodPost, "/im/callback", bytes.NewBufferString(body))
		c2.Request.Header.Set("X-Lark-Request-Timestamp", freshTS)
		c2.Request.Header.Set("X-Lark-Request-Nonce", nonce)
		c2.Request.Header.Set("X-Lark-Signature", strings.Repeat("0", 64))
		if err := adapter.VerifyCallback(c2); err == nil {
			t.Fatal("expected invalid X-Lark-Signature to be rejected")
		}

		validSum := sha256.Sum256([]byte(freshTS + nonce + encryptKey + body))
		w3 := httptest.NewRecorder()
		c3, _ := gin.CreateTestContext(w3)
		c3.Request = httptest.NewRequest(http.MethodPost, "/im/callback", bytes.NewBufferString(body))
		c3.Request.Header.Set("X-Lark-Request-Timestamp", freshTS)
		c3.Request.Header.Set("X-Lark-Request-Nonce", nonce)
		c3.Request.Header.Set("X-Lark-Signature", hex.EncodeToString(validSum[:]))
		if err := adapter.VerifyCallback(c3); err != nil {
			t.Fatalf("expected valid signed encrypted request to pass VerifyCallback, got %v", err)
		}
	})
}

func TestWeComWebhookRejectsStaleTimestampReplay(t *testing.T) {
	gin.SetMode(gin.TestMode)
	// 43-character base64 EncodingAESKey (decodes to 32 bytes)
	aesKey := "abcdefghijklmnopqrstuvwxyz0123456789ABCDEFG"
	adapter, err := wecom.NewWebhookAdapter("ww-corp-id", "agent-secret", "wecom-token", aesKey, 1000002, "")
	if err != nil {
		t.Fatalf("NewWebhookAdapter: %v", err)
	}

	staleTS := fmt.Sprintf("%d", time.Now().Add(-15*time.Minute).Unix())
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(
		http.MethodGet,
		"/im/callback?msg_signature=deadbeef&timestamp="+staleTS+"&nonce=123&echostr=AAAA",
		nil,
	)
	if err := adapter.VerifyCallback(c); err == nil {
		t.Fatal("expected WeCom VerifyCallback to reject stale timestamp")
	}
}

func TestIMCredentialsEncryptionAndLegacyPlaintextCompatibility(t *testing.T) {
	t.Setenv("SYSTEM_AES_KEY", "0123456789abcdef0123456789abcdef")

	input := types.JSON(`{"app_id":"cli_a1b2c3","app_secret":"raw-feishu-secret"}`)
	encrypted := im.EncryptCredentialsJSON(input)
	if !strings.Contains(string(encrypted), "enc:v1:") || strings.Contains(string(encrypted), "raw-feishu-secret") {
		t.Fatalf("expected enc:v1: ciphertext without plaintext secret, got %s", string(encrypted))
	}

	parsed, err := im.ParseCredentials(encrypted)
	if err != nil {
		t.Fatalf("ParseCredentials(encrypted): %v", err)
	}
	if got := im.GetString(parsed, "app_secret"); got != "raw-feishu-secret" {
		t.Fatalf("ParseCredentials(encrypted) = %q, want raw-feishu-secret", got)
	}

	decrypted := im.DecryptCredentialsJSON(encrypted)
	if !strings.Contains(string(decrypted), "raw-feishu-secret") {
		t.Fatalf("DecryptCredentialsJSON(encrypted) = %s, want raw-feishu-secret", string(decrypted))
	}

	legacy := types.JSON(`{"app_secret":"historical-plaintext-secret"}`)
	parsedLegacy, err := im.ParseCredentials(legacy)
	if err != nil || im.GetString(parsedLegacy, "app_secret") != "historical-plaintext-secret" {
		t.Fatalf("ParseCredentials(legacy) = (%v, %v), want historical-plaintext-secret", parsedLegacy, err)
	}
}
