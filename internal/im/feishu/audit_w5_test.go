package feishu

import (
	"bytes"
	"crypto/aes"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestDecrypt_RejectsNonBlockAlignedCiphertextWithoutPanic(t *testing.T) {
	a := &Adapter{
		verificationToken: "v-tok",
		encryptKey:        "test-encrypt-key",
	}

	for _, rawLen := range []int{0, 8, aes.BlockSize, aes.BlockSize + 1, aes.BlockSize*2 - 1, aes.BlockSize*2 + 3} {
		encoded := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x42}, rawLen))
		if _, err := a.decrypt(encoded); err == nil {
			t.Fatalf("decrypt(len=%d) succeeded, want error", rawLen)
		}
	}
}

func TestVerifyCallback_ChecksSignatureBeforeDecrypt(t *testing.T) {
	a := &Adapter{
		verificationToken: "v-tok",
		encryptKey:        "test-encrypt-key",
	}

	// Non-block-aligned encrypted payload + forged X-Lark-Signature:
	// VerifyCallback must reject the forged signature BEFORE attempting AES-CBC decrypt.
	badCipher := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0xaa}, aes.BlockSize+5))
	body := fmt.Sprintf(`{"encrypt":%q}`, badCipher)
	ts := strconv.FormatInt(time.Now().Unix(), 10)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(http.MethodPost, "/callback", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Lark-Request-Timestamp", ts)
	req.Header.Set("X-Lark-Request-Nonce", "nonce-1")
	req.Header.Set("X-Lark-Signature", strings.Repeat("0", 64))
	c.Request = req

	err := a.VerifyCallback(c)
	if err == nil {
		t.Fatal("expected VerifyCallback error for invalid signature, got nil")
	}
	if !strings.Contains(err.Error(), "invalid X-Lark-Signature") {
		t.Fatalf("expected signature rejection before decrypt, got: %v", err)
	}
}
