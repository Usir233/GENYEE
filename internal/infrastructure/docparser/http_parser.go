package docparser

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/Usir233/GENYEE/internal/logger"
	"github.com/Usir233/GENYEE/internal/types"
	secutils "github.com/Usir233/GENYEE/internal/utils"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	PathRead        = "/read"
	PathListEngines = "/list-engines"
)

// --- JSON DTOs ---

type httpReadConfig struct {
	ParserEngine          string            `json:"parser_engine,omitempty"`
	ParserEngineOverrides map[string]string `json:"parser_engine_overrides,omitempty"`
}

type httpReadRequest struct {
	FileContent string          `json:"file_content,omitempty"` // base64
	FileName    string          `json:"file_name,omitempty"`
	FileType    string          `json:"file_type,omitempty"`
	URL         string          `json:"url,omitempty"`
	Title       string          `json:"title,omitempty"`
	Config      *httpReadConfig `json:"config,omitempty"`
	RequestID   string          `json:"request_id,omitempty"`
}

type httpImageRef struct {
	Filename    string `json:"filename"`
	OriginalRef string `json:"original_ref"`
	MimeType    string `json:"mime_type"`
	StorageKey  string `json:"storage_key,omitempty"`
	ImageData   []byte `json:"image_data,omitempty"`
}

type httpReadResponse struct {
	MarkdownContent string            `json:"markdown_content"`
	ImageRefs       []httpImageRef    `json:"image_refs,omitempty"`
	ImageDirPath    string            `json:"image_dir_path,omitempty"`
	Metadata        map[string]string `json:"metadata,omitempty"`
	Error           string            `json:"error,omitempty"`
	SourceBlocks    []wireSourceBlock `json:"source_blocks,omitempty"`
}

// HTTPDocumentReader implements DocumentReader over HTTP/JSON.
type HTTPDocumentReader struct {
	mu      sync.RWMutex
	baseURL string
	trusted bool
	client  *http.Client
}

func normalizeHTTPDocReaderBaseURL(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", nil
	}
	if !strings.HasPrefix(strings.ToLower(trimmed), "http://") && !strings.HasPrefix(strings.ToLower(trimmed), "https://") {
		trimmed = "http://" + trimmed
	}
	u, err := url.Parse(trimmed)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("invalid HTTP docreader base URL %q", raw)
	}
	if !strings.EqualFold(u.Scheme, "http") && !strings.EqualFold(u.Scheme, "https") {
		return "", fmt.Errorf("unsupported HTTP docreader scheme %q", u.Scheme)
	}
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)
	u.Path = strings.TrimRight(u.Path, "/")
	return strings.TrimRight(u.String(), "/"), nil
}

func isTrustedHTTPDocReaderBaseURL(normalizedBase string) bool {
	if normalizedBase == "" {
		return false
	}
	configuredRaw := strings.TrimSpace(os.Getenv("DOCREADER_ADDR"))
	if configuredRaw == "" {
		return false
	}
	configuredNorm, err := normalizeHTTPDocReaderBaseURL(configuredRaw)
	if err != nil || configuredNorm == "" {
		return false
	}
	return configuredNorm == normalizedBase
}

func newHTTPDocReaderClient(trusted bool) *http.Client {
	if trusted {
		return &http.Client{
			Timeout: 5 * time.Minute,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 5 {
					return errors.New("stopped after 5 redirects")
				}
				if len(via) > 0 && (!strings.EqualFold(req.URL.Scheme, via[0].URL.Scheme) || !strings.EqualFold(req.URL.Host, via[0].URL.Host)) {
					return errors.New("cross-origin redirect blocked for internal docreader")
				}
				return nil
			},
		}
	}
	clientCfg := secutils.DefaultSSRFSafeHTTPClientConfig()
	clientCfg.Timeout = 5 * time.Minute
	return secutils.NewSSRFSafeHTTPClient(clientCfg)
}

func NewHTTPDocumentReader(baseURL string) (*HTTPDocumentReader, error) {
	normalized, err := normalizeHTTPDocReaderBaseURL(baseURL)
	if err != nil {
		return nil, err
	}
	trusted := isTrustedHTTPDocReaderBaseURL(normalized)
	if normalized != "" && !trusted {
		if err := secutils.ValidateURLForSSRF(normalized); err != nil {
			return nil, fmt.Errorf("docreader address failed SSRF validation: %w", err)
		}
	}
	p := &HTTPDocumentReader{
		baseURL: normalized,
		trusted: trusted,
		client:  newHTTPDocReaderClient(trusted),
	}
	if p.baseURL != "" {
		logger.Infof(context.Background(), "INFO: HTTP docreader base URL: %s", p.baseURL)
	}
	return p, nil
}

func (p *HTTPDocumentReader) endpoint() (string, bool, *http.Client) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.baseURL, p.trusted, p.client
}

func (p *HTTPDocumentReader) Reconnect(addr string) error {
	normalized, err := normalizeHTTPDocReaderBaseURL(addr)
	if err != nil {
		return err
	}
	trusted := isTrustedHTTPDocReaderBaseURL(normalized)
	if normalized != "" && !trusted {
		if err := secutils.ValidateURLForSSRF(normalized); err != nil {
			return fmt.Errorf("docreader address failed SSRF validation: %w", err)
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.baseURL = normalized
	p.trusted = trusted
	p.client = newHTTPDocReaderClient(trusted)
	logger.Infof(context.Background(), "INFO: HTTP docreader base URL set to %s", p.baseURL)
	return nil
}

func (p *HTTPDocumentReader) IsConnected() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.baseURL != ""
}

func (p *HTTPDocumentReader) Close() error { return nil }

type httpListEnginesRequest struct {
	ConfigOverrides map[string]string `json:"config_overrides,omitempty"`
}

type httpParserEngineInfo struct {
	Name              string   `json:"name"`
	Description       string   `json:"description"`
	FileTypes         []string `json:"file_types"`
	Available         bool     `json:"available"`
	UnavailableReason string   `json:"unavailable_reason,omitempty"`
}

type httpListEnginesResponse struct {
	Engines []httpParserEngineInfo `json:"engines"`
}

func (p *HTTPDocumentReader) ListEngines(ctx context.Context, overrides map[string]string) ([]types.ParserEngineInfo, error) {
	base, trusted, client := p.endpoint()
	if base == "" {
		return nil, errNotConnected
	}
	if !trusted {
		if err := secutils.ValidateURLForSSRF(base); err != nil {
			return nil, fmt.Errorf("docreader address failed SSRF validation: %w", err)
		}
	}

	body := httpListEnginesRequest{ConfigOverrides: overrides}
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("http marshal list-engines request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, base+PathListEngines, bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("http new request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("http list-engines failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		preview := readErrorPreview(resp.Body)
		return nil, fmt.Errorf("http list-engines status %d: %s", resp.StatusCode, preview)
	}

	bodyBytes, err := readCapped(resp.Body, maxJSONResponseBytes)
	if err != nil {
		return nil, fmt.Errorf("http list-engines response: %w", err)
	}
	var out httpListEnginesResponse
	if err := json.Unmarshal(bodyBytes, &out); err != nil {
		return nil, fmt.Errorf("http decode list-engines response: %w", err)
	}

	result := make([]types.ParserEngineInfo, 0, len(out.Engines))
	for _, e := range out.Engines {
		result = append(result, types.ParserEngineInfo{
			Name:              e.Name,
			Description:       e.Description,
			FileTypes:         e.FileTypes,
			Available:         e.Available,
			UnavailableReason: e.UnavailableReason,
		})
	}
	return result, nil
}

func fromHTTPReadResponse(resp *httpReadResponse) *types.ReadResult {
	result := &types.ReadResult{
		MarkdownContent: resp.MarkdownContent,
		ImageDirPath:    resp.ImageDirPath,
		Metadata:        resp.Metadata,
		Error:           resp.Error,
		SourceBlocks:    sourceBlocksFromWire(resp.SourceBlocks),
	}
	for _, ref := range resp.ImageRefs {
		result.ImageRefs = append(result.ImageRefs, types.ImageRef{
			Filename:    ref.Filename,
			OriginalRef: ref.OriginalRef,
			MimeType:    ref.MimeType,
			StorageKey:  ref.StorageKey,
			ImageData:   ref.ImageData,
		})
	}
	return result
}

func (p *HTTPDocumentReader) Read(ctx context.Context, req *types.ReadRequest) (*types.ReadResult, error) {
	base, trusted, client := p.endpoint()
	if base == "" {
		return nil, errNotConnected
	}
	maxFileSize := getMaxFileSize()
	if len(req.FileContent) > maxFileSize {
		return nil, status.Errorf(codes.ResourceExhausted,
			"docreader file content size %d exceeds maximum %d", len(req.FileContent), maxFileSize)
	}
	if !trusted {
		if err := secutils.ValidateURLForSSRF(base); err != nil {
			return nil, fmt.Errorf("docreader address failed SSRF validation: %w", err)
		}
	}

	body := httpReadRequest{
		FileName:  req.FileName,
		FileType:  req.FileType,
		URL:       req.URL,
		Title:     req.Title,
		RequestID: req.RequestID,
		Config: &httpReadConfig{
			ParserEngine:          req.ParserEngine,
			ParserEngineOverrides: req.ParserEngineOverrides,
		},
	}
	if len(req.FileContent) > 0 {
		body.FileContent = base64.StdEncoding.EncodeToString(req.FileContent)
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("http marshal read request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, base+PathRead, bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("http new request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.ContentLength = int64(len(jsonBody))

	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("http read failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		preview := readErrorPreview(resp.Body)
		return nil, fmt.Errorf("http read status %d: %s", resp.StatusCode, preview)
	}
	bodyBytes, err := readCapped(resp.Body, maxResultResponseBytes)
	if err != nil {
		return nil, fmt.Errorf("http read response: %w", err)
	}
	var out httpReadResponse
	if err := json.Unmarshal(bodyBytes, &out); err != nil {
		return nil, fmt.Errorf("http decode read response: %w", err)
	}
	return fromHTTPReadResponse(&out), nil
}

// PreprocessImage is not offered over the HTTP transport: page straightening
// lives in the docreader service, and this reader speaks to a different
// surface. It reports that as a verdict — fallback=true, no error — so the
// caller's single fallback (OCR the original bytes) is the only outcome, with
// nothing logged that looks like a failure.
func (p *HTTPDocumentReader) PreprocessImage(_ context.Context, _ []byte) ([]byte, bool, error) {
	return nil, true, nil
}
