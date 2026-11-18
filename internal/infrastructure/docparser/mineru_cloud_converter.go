package docparser

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Usir233/GENYEE/internal/logger"
	"github.com/Usir233/GENYEE/internal/types"
	"github.com/Usir233/GENYEE/internal/utils"
	"github.com/google/uuid"
)

const (
	defaultPollInterval = 3 * time.Second
	// defaultCloudTimeout bounds how long one batch is polled;
	// GENYEE_MINERU_CLOUD_TIMEOUT overrides it for documents that take longer.
	defaultCloudTimeout = 30 * time.Minute
	defaultBaseURL      = "https://mineru.net/api/v4"
)

// MinerUCloudReader calls the MinerU Cloud API (mineru.net) to read/convert documents.
// Flow: POST /file-urls/batch → PUT file → poll GET /extract-results/batch/{batch_id}.
type MinerUCloudReader struct {
	apiKey        string
	baseURL       string
	timeout       time.Duration
	pollInterval  time.Duration
	model         string
	formulaEnable bool
	tableEnable   bool
	ocrEnable     bool
	language      string
}

// NewMinerUCloudReader creates a reader from ParserEngineOverrides.
func NewMinerUCloudReader(overrides map[string]string) *MinerUCloudReader {
	return &MinerUCloudReader{
		apiKey:        strings.TrimSpace(overrides["mineru_api_key"]),
		baseURL:       defaultBaseURL,
		timeout:       requestTimeoutFromEnv("GENYEE_MINERU_CLOUD_TIMEOUT", defaultCloudTimeout),
		pollInterval:  defaultPollInterval,
		model:         stringOr(overrides["mineru_cloud_model"], "pipeline"),
		formulaEnable: parseBoolOr(overrides["mineru_cloud_enable_formula"], true),
		tableEnable:   parseBoolOr(overrides["mineru_cloud_enable_table"], true),
		ocrEnable:     parseBoolOr(overrides["mineru_cloud_enable_ocr"], true),
		language:      stringOr(overrides["mineru_cloud_language"], "ch"),
	}
}

// Read parses a document via the MinerU Cloud API.
func (c *MinerUCloudReader) Read(ctx context.Context, req *types.ReadRequest) (*types.ReadResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if c.apiKey == "" {
		return &types.ReadResult{Error: "MinerU Cloud API key is not configured"}, nil
	}
	if err := utils.ValidateURLForSSRF(c.baseURL); err != nil {
		return &types.ReadResult{Error: fmt.Sprintf("MinerU Cloud base URL blocked by SSRF check: %v", err)}, nil
	}

	content := req.FileContent
	if len(content) == 0 {
		return &types.ReadResult{Error: "no file content provided"}, nil
	}

	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	fileName := minerUUploadFileName(req.FileName, req.FileType)
	ext := strings.ToLower(filepath.Ext(fileName))
	if ext == "" {
		ext = ".pdf"
		fileName = "document.pdf"
	}

	logger.Infof(ctx, "[MinerUCloud] Parsing file=%s size=%d via %s", fileName, len(content), c.baseURL)

	copyReq := *req
	copyReq.FileName = fileName
	if strings.EqualFold(ext, ".pdf") {
		return c.readSplitPDF(ctx, &copyReq)
	}
	result, err := c.readCloudPart(ctx, &copyReq)
	if err != nil {
		return nil, err
	}
	result.MarkdownContent, result.ImageRefs = ensureOriginalImageRef(req, result.MarkdownContent, result.ImageRefs)
	return result, nil
}

// --- batch upload API ---

type batchApplyResponse struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data struct {
		BatchID  string   `json:"batch_id"`
		FileURLs []string `json:"file_urls"`
	} `json:"data"`
}

func (c *MinerUCloudReader) applyUploadURLs(ctx context.Context, fileName, ext string) (string, string, error) {
	modelVersion := c.model
	if strings.ToLower(ext) == ".html" {
		modelVersion = "MinerU-HTML"
	}

	payload := map[string]interface{}{
		"files":          []map[string]string{{"name": fileName, "data_id": uuid.New().String()}},
		"model_version":  modelVersion,
		"is_ocr":         c.ocrEnable,
		"enable_formula": c.formulaEnable,
		"enable_table":   c.tableEnable,
		"language":       c.language,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return "", "", fmt.Errorf("marshal payload: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(
		ctx, http.MethodPost, c.baseURL+"/file-urls/batch", bytes.NewReader(body),
	)
	if err != nil {
		return "", "", fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")

	client := utils.NewSSRFSafeHTTPClient(utils.SSRFSafeHTTPClientConfig{Timeout: 30 * time.Second, MaxRedirects: 5})
	resp, err := client.Do(httpReq)
	if err != nil {
		return "", "", fmt.Errorf("HTTP request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		preview := readErrorPreview(resp.Body)
		return "", "", fmt.Errorf("API status %d: %s", resp.StatusCode, preview)
	}

	respBody, err := readCapped(resp.Body, maxJSONResponseBytes)
	if err != nil {
		return "", "", fmt.Errorf("read response body: %w", err)
	}
	var result batchApplyResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", "", fmt.Errorf("decode response: %w", err)
	}
	if result.Code != 0 {
		return "", "", fmt.Errorf("API error: %s", result.Msg)
	}
	if len(result.Data.FileURLs) == 0 {
		return "", "", fmt.Errorf("API returned no file_urls")
	}

	logger.Infof(ctx, "[MinerUCloud] batch apply ok: batch_id=%s, urls=%d",
		result.Data.BatchID, len(result.Data.FileURLs))
	return result.Data.BatchID, result.Data.FileURLs[0], nil
}

func (c *MinerUCloudReader) uploadFile(ctx context.Context, uploadURL string, content []byte) error {
	if err := utils.ValidateURLForSSRF(uploadURL); err != nil {
		return fmt.Errorf("upload URL blocked by SSRF check: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPut, uploadURL, bytes.NewReader(content))
	if err != nil {
		return fmt.Errorf("create PUT request: %w", err)
	}

	client := utils.NewSSRFSafeHTTPClient(utils.SSRFSafeHTTPClientConfig{Timeout: 10 * time.Minute, MaxRedirects: 5})
	resp, err := client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("PUT upload: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 300 {
		preview := readErrorPreview(resp.Body)
		if preview != "" {
			return fmt.Errorf("PUT upload status %d: %s", resp.StatusCode, preview)
		}
		return fmt.Errorf("PUT upload status %d", resp.StatusCode)
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxErrorPreviewBytes))
	logger.Infof(ctx, "[MinerUCloud] file uploaded, status=%d", resp.StatusCode)
	return nil
}

// --- polling ---

type batchPollResponse struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data struct {
		ExtractResult json.RawMessage `json:"extract_result"` // can be object or array
	} `json:"data"`
}

type extractResultItem struct {
	State    string `json:"state"`
	FileName string `json:"file_name"`
	Markdown string `json:"markdown"`
	Content  string `json:"content"`
	Text     string `json:"text"`
	ErrMsg   string `json:"err_msg"`
	Progress struct {
		ExtractedPages int `json:"extracted_pages"`
		TotalPages     int `json:"total_pages"`
	} `json:"extract_progress"`
	FullZipURL string `json:"full_zip_url"`
}

func (c *MinerUCloudReader) pollBatchResult(
	ctx context.Context, batchID string,
) (string, []types.ImageRef, []byte, error) {
	deadline := time.Now().Add(c.timeout)
	pollCount := 0
	headers := map[string]string{
		"Authorization": "Bearer " + c.apiKey,
	}

	for time.Now().Before(deadline) {
		// Bail out promptly on caller cancellation instead of spinning:
		// fetchBatchStatus fails immediately and sleepCtx returns at once on a
		// cancelled ctx, so without this guard the loop busy-hammers the cloud
		// API and floods logs until the deadline.
		if err := ctx.Err(); err != nil {
			return "", nil, nil, err
		}
		pollCount++

		items, err := c.fetchBatchStatus(ctx, batchID, headers)
		if err != nil {
			logger.Errorf(ctx, "[MinerUCloud] poll #%d failed: %v", pollCount, err)
			sleepCtx(ctx, c.pollInterval)
			continue
		}

		if len(items) == 0 {
			if pollCount <= 3 || pollCount%10 == 0 {
				logger.Infof(ctx, "[MinerUCloud] poll #%d: extract_result empty, retrying", pollCount)
			}
			sleepCtx(ctx, c.pollInterval)
			continue
		}

		item := items[0]
		state := strings.ToLower(item.State)

		if pollCount == 1 || pollCount%10 == 0 || state == "done" || state == "failed" {
			logger.Infof(ctx, "[MinerUCloud] poll #%d: file=%s state=%s pages=%d/%d",
				pollCount, item.FileName, state, item.Progress.ExtractedPages, item.Progress.TotalPages)
		}

		if state == "failed" {
			return "", nil, nil, fmt.Errorf("MinerU Cloud task failed: %s", item.ErrMsg)
		}

		if state == "done" {
			return c.extractDoneResult(ctx, &item)
		}

		sleepCtx(ctx, c.pollInterval)
	}

	return "", nil, nil, fmt.Errorf("MinerU Cloud task timed out after %s (%d polls)", c.timeout, pollCount)
}

func urlPathUnescape(s string) (string, error) {
	return url.PathUnescape(s)
}

func (c *MinerUCloudReader) fetchBatchStatus(
	ctx context.Context, batchID string, headers map[string]string,
) ([]extractResultItem, error) {
	pollURL := fmt.Sprintf("%s/extract-results/batch/%s", c.baseURL, url.PathEscape(batchID))
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, pollURL, nil)
	if err != nil {
		return nil, err
	}
	for k, v := range headers {
		httpReq.Header.Set(k, v)
	}

	client := utils.NewSSRFSafeHTTPClient(utils.SSRFSafeHTTPClientConfig{Timeout: 30 * time.Second, MaxRedirects: 5})
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		preview := readErrorPreview(resp.Body)
		return nil, fmt.Errorf("poll status %d: %s", resp.StatusCode, preview)
	}

	respBody, err := readCapped(resp.Body, maxResultResponseBytes)
	if err != nil {
		return nil, fmt.Errorf("read poll response body: %w", err)
	}

	var pollResp batchPollResponse
	if err := json.Unmarshal(respBody, &pollResp); err != nil {
		return nil, fmt.Errorf("decode poll response: %w", err)
	}
	if pollResp.Code != 0 {
		return nil, fmt.Errorf("poll error code=%d msg=%s", pollResp.Code, pollResp.Msg)
	}

	if len(pollResp.Data.ExtractResult) == 0 {
		return nil, nil
	}

	// Dump the raw extract_result JSON for debugging
	rawExtract := string(pollResp.Data.ExtractResult)
	if len(rawExtract) > rawResponseLogBytes {
		logger.Infof(
			ctx,
			"[MinerUCloud] Raw extract_result (truncated to %d chars): %s",
			rawResponseLogBytes, rawResponsePreview(rawExtract),
		)
	} else {
		logger.Infof(ctx, "[MinerUCloud] Raw extract_result: %s", rawExtract)
	}

	// Pretty-print the structure to reveal all available fields
	var rawObj interface{}
	if err := json.Unmarshal(pollResp.Data.ExtractResult, &rawObj); err == nil {
		logResponseStructure("MinerUCloud", rawObj, "extract_result")
	}

	// The extract_result can be either a single object or an array
	var items []extractResultItem
	if pollResp.Data.ExtractResult[0] == '[' {
		if err := json.Unmarshal(pollResp.Data.ExtractResult, &items); err != nil {
			return nil, fmt.Errorf("decode extract_result array: %w", err)
		}
	} else {
		var single extractResultItem
		if err := json.Unmarshal(pollResp.Data.ExtractResult, &single); err != nil {
			return nil, fmt.Errorf("decode extract_result object: %w", err)
		}
		items = []extractResultItem{single}
	}

	return items, nil
}

// extractDoneResult extracts markdown and images from a completed batch item.
// Prefers inline markdown/content fields; falls back to downloading full_zip_url.
func (c *MinerUCloudReader) extractDoneResult(
	ctx context.Context, item *extractResultItem,
) (string, []types.ImageRef, []byte, error) {
	text := firstNonEmpty(item.Markdown, item.Content, item.Text)
	if text != "" {
		logger.Infof(ctx, "[MinerUCloud] parsed (inline), length=%d", len(text))
		return text, nil, nil, nil
	}

	if item.FullZipURL == "" {
		return "", nil, nil, fmt.Errorf("MinerU Cloud state=done but no markdown/content or full_zip_url")
	}

	md, imageRefs, contentList, err := downloadAndExtractZip(ctx, item.FullZipURL)
	if err != nil {
		return "", nil, nil, fmt.Errorf("extract zip: %w", err)
	}

	logger.Infof(ctx, "[MinerUCloud] parsed (zip), markdown=%d chars, images=%d", len(md), len(imageRefs))
	return md, imageRefs, contentList, nil
}

// --- ZIP handling ---

// downloadAndExtractZip also returns the content_list of the package, if any.
func downloadAndExtractZip(ctx context.Context, zipURL string) (string, []types.ImageRef, []byte, error) {
	if err := utils.ValidateURLForSSRF(zipURL); err != nil {
		return "", nil, nil, fmt.Errorf("zip URL blocked by SSRF check: %w", err)
	}
	client := utils.NewSSRFSafeHTTPClient(utils.SSRFSafeHTTPClientConfig{Timeout: 10 * time.Minute, MaxRedirects: 5})
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, zipURL, nil)
	if err != nil {
		return "", nil, nil, err
	}
	resp, err := client.Do(request)
	if err != nil {
		return "", nil, nil, fmt.Errorf("download zip: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		preview := readErrorPreview(resp.Body)
		if preview != "" {
			return "", nil, nil, fmt.Errorf("download zip status %d: %s", resp.StatusCode, preview)
		}
		return "", nil, nil, fmt.Errorf("download zip status %d", resp.StatusCode)
	}

	zipData, err := readResultPackage(resp.Body, maxResultPackageBytes)
	if err != nil {
		return "", nil, nil, fmt.Errorf("read zip body: %w", err)
	}
	md, imageRefs, err := extractMarkdownZip(zipData, "MinerUCloud")
	if err != nil {
		return "", nil, nil, err
	}
	return md, imageRefs, minerUContentListFromZip(zipData), nil
}

// extractMarkdownZip reads the shallowest .md file of a MinerU result package
// and the images it references, via markdown or inline HTML <img> syntax.
func extractMarkdownZip(zipData []byte, logLabel string) (string, []types.ImageRef, error) {
	return extractMarkdownZipBounded(zipData, logLabel, minerUMaxImageBytes())
}

// extractMarkdownZipBounded is extractMarkdownZip with the accumulated-image
// budget as a parameter, so a test can exercise the limit without materialising
// the production figure.
func extractMarkdownZipBounded(zipData []byte, logLabel string, maxImages int64) (string, []types.ImageRef, error) {
	zr, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		return "", nil, fmt.Errorf("open zip: %w", err)
	}

	// Find .md files
	var mdFiles []string
	entries := make(map[string]*zip.File)
	for _, f := range zr.File {
		entries[f.Name] = f
		if strings.HasSuffix(f.Name, ".md") {
			mdFiles = append(mdFiles, f.Name)
		}
	}
	if len(mdFiles) == 0 {
		return "", nil, fmt.Errorf("no .md file found in zip")
	}
	sort.Slice(mdFiles, func(i, j int) bool {
		di, dj := strings.Count(mdFiles[i], "/"), strings.Count(mdFiles[j], "/")
		if di != dj {
			return di < dj
		}
		return mdFiles[i] < mdFiles[j]
	})

	mdText, err := readZipEntry(entries[mdFiles[0]])
	if err != nil {
		return "", nil, fmt.Errorf("read md file: %w", err)
	}

	mdDir := filepath.Dir(mdFiles[0])

	// Extract referenced images
	var imageRefs []types.ImageRef
	// Every image below is kept in memory until the caller hands the slice on,
	// so the package needs a total budget as well as a per-entry cap: a package
	// of thousands of small entries expands without ever tripping the latter.
	var imageBytes int64
	seen := map[string]bool{}
	for _, imgPath := range extractImageRefsFromContent(mdText) {
		if strings.HasPrefix(imgPath, "http://") ||
			strings.HasPrefix(imgPath, "https://") ||
			strings.HasPrefix(imgPath, "data:") {
			continue
		}
		if seen[imgPath] {
			continue
		}
		seen[imgPath] = true

		resolved := resolveInZip(imgPath, mdDir, entries)
		if resolved == nil {
			logger.Errorf(context.Background(), "[%s] image not found in zip: %s", logLabel, imgPath)
			continue
		}

		imgData, err := readZipEntryBytes(resolved)
		if err != nil {
			logger.Errorf(context.Background(), "[%s] failed to read zip image %s: %v", logLabel, imgPath, err)
			continue
		}
		if imageBytes+int64(len(imgData)) > maxImages {
			return "", nil, fmt.Errorf("%w: images in %q reached %d bytes and exceed configured limit %d bytes (%s)",
				errResultPackageTooLarge, logLabel, imageBytes+int64(len(imgData)), maxImages, mineruMaxImageBytesEnv)
		}
		imageBytes += int64(len(imgData))

		ext := safeImageExtFromFilename(resolved.Name)
		if ext == "" {
			ext = ".png"
		}
		mimeType := mime.TypeByExtension(ext)
		if !strings.HasPrefix(strings.ToLower(mimeType), "image/") {
			mimeType = "image/png"
		}

		imageRefs = append(imageRefs, types.ImageRef{
			Filename:    filepath.Base(resolved.Name),
			OriginalRef: imgPath,
			MimeType:    mimeType,
			ImageData:   imgData,
		})
	}

	return mdText, imageRefs, nil
}

func resolveInZip(imgPath, mdDir string, entries map[string]*zip.File) *zip.File {
	candidates := []string{strings.ReplaceAll(imgPath, "\\", "/")}
	if decoded, err := url.PathUnescape(candidates[0]); err == nil && decoded != candidates[0] {
		candidates = append(candidates, decoded)
	}
	for _, c := range candidates {
		c = strings.TrimPrefix(c, "./")
		if f, ok := entries[c]; ok {
			return f
		}
		if mdDir != "" && mdDir != "." {
			if f, ok := entries[mdDir+"/"+c]; ok {
				return f
			}
		}
	}
	return nil
}

// readZipEntry reads a text entry (the package markdown) under the per-entry cap.
func readZipEntry(f *zip.File) (string, error) {
	data, err := readZipEntryCapped(f, maxZipEntryBytes)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// readZipEntryBytes reads a binary entry (an extracted image) under the
// per-entry cap.
func readZipEntryBytes(f *zip.File) ([]byte, error) {
	return readZipEntryCapped(f, maxZipEntryBytes)
}

// PingMinerUCloud checks whether the MinerU Cloud API is reachable with the given API key.
func PingMinerUCloud(apiKey string) (bool, string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return PingMinerUCloudContext(ctx, defaultBaseURL, apiKey)
}

// PingMinerUCloudContext checks whether a MinerU Cloud API endpoint is reachable and accepts apiKey.
func PingMinerUCloudContext(ctx context.Context, baseURL, apiKey string) (bool, string) {
	if ctx == nil {
		ctx = context.Background()
	}
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return false, "MinerU Cloud API key is not configured"
	}
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	if err := utils.ValidateURLForSSRF(baseURL); err != nil {
		return false, fmt.Sprintf("MinerU Cloud URL blocked by SSRF check: %v", err)
	}

	targetURL := baseURL + "/file-urls/batch"
	payload := []byte(`{"files":[],"model_version":"pipeline"}`)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, bytes.NewReader(payload))
	if err != nil {
		return false, fmt.Sprintf("failed to build MinerU Cloud probe request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	client := utils.NewSSRFSafeHTTPClient(utils.SSRFSafeHTTPClientConfig{
		Timeout:      10 * time.Second,
		MaxRedirects: 5,
	})
	resp, err := client.Do(req)
	if err != nil {
		return false, fmt.Sprintf("MinerU Cloud unreachable: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxErrorPreviewBytes))

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return false, "invalid MinerU Cloud API key"
	}
	if resp.StatusCode >= http.StatusInternalServerError {
		return false, fmt.Sprintf("MinerU Cloud returned status %d", resp.StatusCode)
	}
	return true, ""
}
