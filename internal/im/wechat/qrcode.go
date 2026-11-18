// Package wechat implements the Tencent iLink Bot API IM adapter, long-polling
// message receiver, and QR code login flow for WeChat personal accounts.
package wechat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/Usir233/GENYEE/internal/im"
	"github.com/Usir233/GENYEE/internal/types"
	secutils "github.com/Usir233/GENYEE/internal/utils"
)

// QRCodeResult holds the result of requesting a login QR code.
type QRCodeResult struct {
	// QRCodeURL is the URL to render as a QR code image (qrcode_img_content).
	QRCodeURL string `json:"qrcode_url"`
	// QRCode is the opaque token used to poll for scan status.
	QRCode string `json:"qrcode"`
}

// LoginResult holds the credentials returned after successful QR code scan.
type LoginResult struct {
	Status      string `json:"status"`        // "wait", "scaned", "confirmed", "expired"
	BotToken    string `json:"bot_token"`     // Bearer token for API calls
	ILinkBotID  string `json:"ilink_bot_id"`  // Bot identifier
	ILinkUserID string `json:"ilink_user_id"` // User identifier
	BaseURL     string `json:"baseurl"`       // API base URL (may override default)
}

const qrCodeOwnerTTL = 10 * time.Minute

type qrCodeOwner struct {
	tenantID  uint64
	expiresAt time.Time
}

// QRCodeService handles WeChat QR code login operations.
type QRCodeService struct {
	client *http.Client
	mu     sync.Mutex
	owners map[string]qrCodeOwner
}

// NewQRCodeService creates a new QR code service.
func NewQRCodeService() *QRCodeService {
	return &QRCodeService{
		client: secutils.NewSSRFSafeHTTPClient(secutils.SSRFSafeHTTPClientConfig{
			Timeout:      pollTimeout,
			MaxRedirects: 5,
		}),
		owners: make(map[string]qrCodeOwner),
	}
}

func (s *QRCodeService) recordOwner(qrcode string, tenantID uint64) {
	if qrcode == "" || tenantID == 0 {
		return
	}
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.owners == nil {
		s.owners = make(map[string]qrCodeOwner)
	}
	for k, v := range s.owners {
		if now.After(v.expiresAt) {
			delete(s.owners, k)
		}
	}
	s.owners[qrcode] = qrCodeOwner{
		tenantID:  tenantID,
		expiresAt: now.Add(qrCodeOwnerTTL),
	}
}

func (s *QRCodeService) verifyOwner(ctx context.Context, qrcode string) error {
	now := time.Now()
	s.mu.Lock()
	owner, tracked := s.owners[qrcode]
	if tracked && now.After(owner.expiresAt) {
		delete(s.owners, qrcode)
		tracked = false
	}
	s.mu.Unlock()
	if !tracked {
		return nil
	}
	callerTenant, ok := types.TenantIDFromContext(ctx)
	if !ok || callerTenant != owner.tenantID {
		return fmt.Errorf("qrcode does not belong to current tenant")
	}
	return nil
}

func (s *QRCodeService) clearOwner(qrcode string) {
	s.mu.Lock()
	delete(s.owners, qrcode)
	s.mu.Unlock()
}

// GetLoginQRCode requests a new login QR code from iLink API.
// GET /ilink/bot/get_bot_qrcode?bot_type=3
func (s *QRCodeService) GetLoginQRCode(ctx context.Context) (*QRCodeResult, error) {
	u := ilinkBaseURL + "/ilink/bot/get_bot_qrcode?bot_type=" + url.QueryEscape(defaultBotType)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request qrcode: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := im.ReadIMResponse(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("qrcode API returned status %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		QRCode           string `json:"qrcode"`
		QRCodeImgContent string `json:"qrcode_img_content"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	if result.QRCode == "" {
		return nil, fmt.Errorf("empty qrcode in response")
	}

	if tenantID, ok := types.TenantIDFromContext(ctx); ok && tenantID != 0 {
		s.recordOwner(result.QRCode, tenantID)
	}

	return &QRCodeResult{
		QRCodeURL: result.QRCodeImgContent,
		QRCode:    result.QRCode,
	}, nil
}

// pollTimeout is the client-side timeout for the long-poll get_qrcode_status
// request. The iLink server may hold the request up to 35s, so we set a
// slightly longer timeout. We use a DETACHED context (not the gin request
// context) to avoid "context canceled" when gin's own timeout fires first.
const pollTimeout = 38 * time.Second

// PollQRCodeStatus checks the scan status of a QR code.
// GET /ilink/bot/get_qrcode_status?qrcode=xxx
// This is a long-poll endpoint: the server holds the connection until there
// is a status change or ~35 seconds elapse.
// Status values: "wait", "scaned", "confirmed", "expired"
func (s *QRCodeService) PollQRCodeStatus(ctx context.Context, qrcode string) (*LoginResult, error) {
	qrcode = strings.TrimSpace(qrcode)
	if qrcode == "" || len(qrcode) > 512 {
		return nil, fmt.Errorf("invalid qrcode")
	}
	if err := s.verifyOwner(ctx, qrcode); err != nil {
		return nil, err
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		return nil, ctx.Err()
	}

	u := ilinkBaseURL + "/ilink/bot/get_qrcode_status?qrcode=" + url.QueryEscape(qrcode)

	// Use a DETACHED context with our own timeout so we are not bound by the
	// caller's (gin) request context deadline which may be shorter than the iLink
	// long-poll hold time, while still cancelling promptly on client disconnect.
	pollCtx, cancel := context.WithTimeout(context.Background(), pollTimeout)
	defer cancel()
	stopCancel := context.AfterFunc(ctx, func() {
		if errors.Is(ctx.Err(), context.Canceled) {
			cancel()
		}
	})
	defer stopCancel()

	req, err := http.NewRequestWithContext(pollCtx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("iLink-App-ClientVersion", "1")

	resp, err := s.client.Do(req)
	if err != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			return nil, ctx.Err()
		}
		// Client-side timeout is normal for long-poll; return "wait" status
		if pollCtx.Err() != nil {
			return &LoginResult{Status: "wait"}, nil
		}
		return nil, fmt.Errorf("request qrcode status: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := im.ReadIMResponse(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("qrcode status API returned status %d", resp.StatusCode)
	}

	var result struct {
		Status      string `json:"status"` // "wait", "scaned", "confirmed", "expired"
		BotToken    string `json:"bot_token"`
		ILinkBotID  string `json:"ilink_bot_id"`
		ILinkUserID string `json:"ilink_user_id"`
		BaseURL     string `json:"baseurl"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	if result.Status == "confirmed" || result.Status == "expired" {
		s.clearOwner(qrcode)
	}

	return &LoginResult{
		Status:      result.Status,
		BotToken:    result.BotToken,
		ILinkBotID:  result.ILinkBotID,
		ILinkUserID: result.ILinkUserID,
		BaseURL:     result.BaseURL,
	}, nil
}
