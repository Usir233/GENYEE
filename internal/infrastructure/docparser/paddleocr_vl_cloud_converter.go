package docparser

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime"
	"mime/multipart"
	"net/http"
	neturl "net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/Usir233/GENYEE/internal/logger"
	"github.com/Usir233/GENYEE/internal/sourceloc"
	"github.com/Usir233/GENYEE/internal/types"
	"github.com/Usir233/GENYEE/internal/utils"
)

const (
	paddleOCRVLCloudDefaultBaseURL = "https://paddleocr.aistudio-app.com/api/v2/ocr/jobs"
	paddleOCRVLCloudDefaultModel   = "PaddleOCR-VL-1.6"
	paddleOCRVLCloudPollInterval   = 5 * time.Second
	// paddleOCRVLCloudTimeout bounds how long one job is polled;
	// GENYEE_PADDLEOCR_VL_CLOUD_TIMEOUT overrides it for documents that take
	// longer, mirroring GENYEE_MINERU_CLOUD_TIMEOUT on the MinerU cloud reader.
	paddleOCRVLCloudTimeout = 600 * time.Second
)

// PaddleOCRVLCloudReader calls the PaddleOCR-VL AI Studio cloud API.
// Flow: POST /jobs (multipart) → poll GET /jobs/{id} → download result JSONL,
// then fetch each referenced image URL.
type PaddleOCRVLCloudReader struct {
	token        string
	baseURL      string
	model        string
	useSeal      bool
	useChart     bool
	timeout      time.Duration
	pollInterval time.Duration
}

// NewPaddleOCRVLCloudReader creates a reader from ParserEngineOverrides.
func NewPaddleOCRVLCloudReader(overrides map[string]string) *PaddleOCRVLCloudReader {
	return &PaddleOCRVLCloudReader{
		token: strings.TrimSpace(overrides["paddleocr_vl_cloud_token"]),
		baseURL: strings.TrimRight(stringOr(
			overrides["paddleocr_vl_cloud_base_url"], paddleOCRVLCloudDefaultBaseURL,
		), "/"),
		model:        stringOr(overrides["paddleocr_vl_cloud_model"], paddleOCRVLCloudDefaultModel),
		useSeal:      parseBoolOr(overrides["paddleocr_vl_cloud_use_seal_recognition"], true),
		useChart:     parseBoolOr(overrides["paddleocr_vl_cloud_use_chart_recognition"], false),
		timeout:      requestTimeoutFromEnv("GENYEE_PADDLEOCR_VL_CLOUD_TIMEOUT", paddleOCRVLCloudTimeout),
		pollInterval: paddleOCRVLCloudPollInterval,
	}
}

// Read parses a document via the PaddleOCR-VL Cloud API.
func (c *PaddleOCRVLCloudReader) Read(ctx context.Context, req *types.ReadRequest) (*types.ReadResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if c.token == "" {
		return &types.ReadResult{Error: "PaddleOCR-VL Cloud token is not configured"}, nil
	}
	if err := utils.ValidateURLForSSRF(c.baseURL); err != nil {
		return &types.ReadResult{Error: fmt.Sprintf("PaddleOCR-VL Cloud base URL blocked by SSRF policy: %v", err)}, nil
	}

	content := req.FileContent
	if len(content) == 0 {
		return &types.ReadResult{Error: "no file content provided"}, nil
	}

	logger.Infof(ctx, "[PaddleOCR-VL Cloud] Parsing file=%s size=%d model=%s",
		req.FileName, len(content), c.model)

	jobID, err := c.submitJob(ctx, req, content)
	if err != nil {
		return nil, fmt.Errorf("PaddleOCR-VL Cloud submit: %w", err)
	}

	jsonlURL, err := c.pollJob(ctx, jobID)
	if err != nil {
		return nil, fmt.Errorf("PaddleOCR-VL Cloud poll: %w", err)
	}

	mdContent, imagesURL, layoutUnits, err := c.fetchResults(ctx, jsonlURL)
	if err != nil {
		return nil, fmt.Errorf("PaddleOCR-VL Cloud fetch results: %w", err)
	}

	mdContent = NormalizeHTMLTables(mdContent)

	imageRefs, err := c.downloadImages(ctx, mdContent, imagesURL)
	if err != nil {
		return nil, fmt.Errorf("PaddleOCR-VL Cloud fetch images: %w", err)
	}
	mdContent, imageRefs = ensureOriginalImageRef(req, mdContent, imageRefs)

	logger.Infof(context.Background(), "[PaddleOCR-VL Cloud] Parsed successfully, markdown=%d chars, images=%d",
		len(mdContent), len(imageRefs))

	return &types.ReadResult{
		MarkdownContent: mdContent,
		ImageRefs:       imageRefs,
		SourceBlocks:    paddleOCRVLSourceBlocks(mdContent, layoutUnits, fileTypeOf(req)),
	}, nil
}

func (c *PaddleOCRVLCloudReader) optionalPayload() map[string]interface{} {
	// Shared with the self-hosted engine so both produce identical output.
	return paddleOCRVLRecognitionParams(c.useSeal, c.useChart)
}

// --- job submit ---

type paddleOCRVLCloudSubmitResponse struct {
	Data struct {
		JobID string `json:"jobId"`
	} `json:"data"`
	ErrorCode int    `json:"errorCode"`
	ErrorMsg  string `json:"errorMsg"`
}

func (c *PaddleOCRVLCloudReader) submitJob(
	ctx context.Context, req *types.ReadRequest, content []byte,
) (string, error) {
	optional, err := json.Marshal(c.optionalPayload())
	if err != nil {
		return "", fmt.Errorf("marshal optionalPayload: %w", err)
	}

	fileName := minerUUploadFileName(req.FileName, req.FileType)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("model", c.model); err != nil {
		return "", fmt.Errorf("write model field: %w", err)
	}
	if err := writer.WriteField("optionalPayload", string(optional)); err != nil {
		return "", fmt.Errorf("write optionalPayload field: %w", err)
	}
	part, err := writer.CreateFormFile("file", fileName)
	if err != nil {
		return "", fmt.Errorf("create form file: %w", err)
	}
	if _, err := part.Write(content); err != nil {
		return "", fmt.Errorf("write file content: %w", err)
	}
	if err := writer.Close(); err != nil {
		return "", fmt.Errorf("close multipart writer: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL, &body)
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Authorization", "bearer "+c.token)
	httpReq.Header.Set("Content-Type", writer.FormDataContentType())

	client := utils.NewSSRFSafeHTTPClient(utils.SSRFSafeHTTPClientConfig{Timeout: 60 * time.Second, MaxRedirects: 5})
	resp, err := client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("HTTP request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		preview := readErrorPreview(resp.Body)
		return "", fmt.Errorf("API status %d: %s", resp.StatusCode, preview)
	}

	respBody, err := readCapped(resp.Body, maxJSONResponseBytes)
	if err != nil {
		return "", fmt.Errorf("read response body: %w", err)
	}

	var result paddleOCRVLCloudSubmitResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}
	if result.Data.JobID == "" {
		return "", fmt.Errorf("API returned no jobId: %s", rawResponsePreview(string(respBody)))
	}

	logger.Infof(context.Background(), "[PaddleOCR-VL Cloud] job submitted: jobId=%s", result.Data.JobID)
	return result.Data.JobID, nil
}

// --- polling ---

type paddleOCRVLCloudPollResponse struct {
	Data struct {
		State           string `json:"state"`
		ErrorMsg        string `json:"errorMsg"`
		ExtractProgress struct {
			TotalPages     int `json:"totalPages"`
			ExtractedPages int `json:"extractedPages"`
		} `json:"extractProgress"`
		ResultURL struct {
			JSONURL string `json:"jsonUrl"`
		} `json:"resultUrl"`
	} `json:"data"`
}

func (c *PaddleOCRVLCloudReader) pollJob(ctx context.Context, jobID string) (string, error) {
	deadline := time.Now().Add(c.timeout)
	pollCount := 0
	pollURL := c.baseURL + "/" + neturl.PathEscape(jobID)

	for time.Now().Before(deadline) {
		// Bail out promptly when the caller cancels (task cancelled / timed
		// out) instead of spinning: client.Do would fail immediately and
		// sleepCtx returns at once on a cancelled ctx, so without this guard
		// the loop busy-hammers the cloud API and floods logs until deadline.
		if err := ctx.Err(); err != nil {
			return "", err
		}
		pollCount++

		httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, pollURL, nil)
		if err != nil {
			return "", fmt.Errorf("create poll request: %w", err)
		}
		httpReq.Header.Set("Authorization", "bearer "+c.token)

		client := utils.NewSSRFSafeHTTPClient(
			utils.SSRFSafeHTTPClientConfig{Timeout: 30 * time.Second, MaxRedirects: 5},
		)
		resp, err := client.Do(httpReq)
		if err != nil {
			logger.Errorf(context.Background(), "[PaddleOCR-VL Cloud] poll #%d failed: %v", pollCount, err)
			sleepCtx(ctx, c.pollInterval)
			continue
		}
		if resp.StatusCode != http.StatusOK {
			preview := readErrorPreview(resp.Body)
			_ = resp.Body.Close()
			logger.Errorf(context.Background(), "[PaddleOCR-VL Cloud] poll #%d status %d: %s",
				pollCount, resp.StatusCode, preview)
			sleepCtx(ctx, c.pollInterval)
			continue
		}
		respBody, err := readCapped(resp.Body, maxJSONResponseBytes)
		_ = resp.Body.Close()
		if err != nil {
			logger.Errorf(context.Background(), "[PaddleOCR-VL Cloud] poll #%d read error: %v", pollCount, err)
			sleepCtx(ctx, c.pollInterval)
			continue
		}

		var pollResp paddleOCRVLCloudPollResponse
		if err := json.Unmarshal(respBody, &pollResp); err != nil {
			logger.Errorf(context.Background(), "[PaddleOCR-VL Cloud] poll #%d decode error: %v", pollCount, err)
			sleepCtx(ctx, c.pollInterval)
			continue
		}

		state := strings.ToLower(pollResp.Data.State)
		if pollCount == 1 || pollCount%6 == 0 || state == "done" || state == "failed" {
			prog := pollResp.Data.ExtractProgress
			logger.Infof(ctx, "[PaddleOCR-VL Cloud] poll #%d: state=%s pages=%d/%d",
				pollCount, state, prog.ExtractedPages, prog.TotalPages)
		}

		switch state {
		case "done":
			if pollResp.Data.ResultURL.JSONURL == "" {
				return "", fmt.Errorf("state=done but no jsonUrl")
			}
			return pollResp.Data.ResultURL.JSONURL, nil
		case "failed":
			return "", fmt.Errorf("task failed: %s", pollResp.Data.ErrorMsg)
		}

		sleepCtx(ctx, c.pollInterval)
	}

	return "", fmt.Errorf("PaddleOCR-VL Cloud task timed out after %s (%d polls)", c.timeout, pollCount)
}

// --- result parsing ---

type paddleOCRVLCloudResultLine struct {
	Result struct {
		LayoutParsingResults []paddleOCRVLPage `json:"layoutParsingResults"`
	} `json:"result"`
}

func (c *PaddleOCRVLCloudReader) fetchResults(
	ctx context.Context, jsonlURL string,
) (string, map[string]string, []sourceloc.Unit, error) {
	if err := utils.ValidateURLForSSRF(jsonlURL); err != nil {
		return "", nil, nil, fmt.Errorf("jsonl URL blocked by SSRF check: %w", err)
	}
	client := utils.NewSSRFSafeHTTPClient(utils.SSRFSafeHTTPClientConfig{Timeout: 120 * time.Second, MaxRedirects: 5})
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, jsonlURL, nil)
	if err != nil {
		return "", nil, nil, fmt.Errorf("create jsonl request: %w", err)
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		return "", nil, nil, fmt.Errorf("download jsonl: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", nil, nil, fmt.Errorf("download jsonl status %d", resp.StatusCode)
	}
	data, err := readCapped(resp.Body, maxResultResponseBytes)
	if err != nil {
		return "", nil, nil, fmt.Errorf("read jsonl body: %w", err)
	}

	texts := make([]string, 0)
	images := make(map[string]string)
	var units []sourceloc.Unit
	pageNumber := 0
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if err := ctx.Err(); err != nil {
			return "", nil, nil, err
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var parsed paddleOCRVLCloudResultLine
		if err := json.Unmarshal([]byte(line), &parsed); err != nil {
			logger.Errorf(context.Background(), "[PaddleOCR-VL Cloud] skip malformed jsonl line: %v", err)
			continue
		}
		for _, p := range parsed.Result.LayoutParsingResults {
			pageNumber++
			units = append(units, paddleOCRVLPageUnits(pageNumber, p)...)
			if t := strings.TrimSpace(p.Markdown.Text); t != "" {
				texts = append(texts, p.Markdown.Text)
			}
			for path, u := range p.Markdown.Images {
				if _, ok := images[path]; !ok {
					images[path] = u
				}
			}
		}
	}

	logger.Infof(context.Background(), "[PaddleOCR-VL Cloud] fetched %d page(s), images=%d", len(texts), len(images))
	return strings.Join(texts, "\n\n"), images, units, nil
}

// downloadImages fetches each referenced image URL and builds ImageRef entries.
func (c *PaddleOCRVLCloudReader) downloadImages(
	ctx context.Context, mdContent string, imagesURL map[string]string,
) ([]types.ImageRef, error) {
	var refs []types.ImageRef
	var totalBytes int64
	maxImages := minerUMaxImageBytes()
	extracted := extractImageRefsFromContent(mdContent)
	client := utils.NewSSRFSafeHTTPClient(utils.SSRFSafeHTTPClientConfig{Timeout: 60 * time.Second, MaxRedirects: 5})

	for ipath, u := range imagesURL {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		matchedRefs := matchExtractedRefsForPath(extracted, ipath)
		if len(matchedRefs) == 0 {
			continue
		}
		if err := utils.ValidateURLForSSRF(u); err != nil {
			logger.Errorf(ctx, "[PaddleOCR-VL Cloud] image URL blocked %s: %v", ipath, err)
			continue
		}
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			logger.Errorf(ctx, "[PaddleOCR-VL Cloud] create image request %s: %v", ipath, err)
			continue
		}
		resp, err := client.Do(httpReq)
		if err != nil {
			logger.Errorf(ctx, "[PaddleOCR-VL Cloud] download image %s: %v", ipath, err)
			continue
		}
		if resp.StatusCode != http.StatusOK {
			preview := readErrorPreview(resp.Body)
			_ = resp.Body.Close()
			logger.Errorf(ctx, "[PaddleOCR-VL Cloud] read image %s status=%d body=%s",
				ipath, resp.StatusCode, preview)
			continue
		}
		// A per-page crop, so the remote-image cap is the right ceiling rather
		// than the JSON one, and image_resolver.go already applies that same
		// cap to the images it fetches.
		imgBytes, err := readCapped(resp.Body, maxRemoteImageSize)
		_ = resp.Body.Close()
		if err != nil {
			logger.Errorf(ctx, "[PaddleOCR-VL Cloud] read image %s err=%v", ipath, err)
			continue
		}
		if totalBytes+int64(len(imgBytes)) > maxImages {
			return nil, fmt.Errorf("%w: PaddleOCR-VL Cloud images reached %d bytes and exceed limit %d bytes (%s)",
				errResultPackageTooLarge, totalBytes+int64(len(imgBytes)), maxImages, mineruMaxImageBytesEnv)
		}
		totalBytes += int64(len(imgBytes))

		ext := strings.TrimPrefix(filepath.Ext(ipath), ".")
		if ext == "" {
			ext = "png"
		}
		mimeType := mime.TypeByExtension("." + ext)
		if !strings.HasPrefix(strings.ToLower(mimeType), "image/") {
			mimeType = "image/png"
		}

		for _, originalRef := range matchedRefs {
			refs = append(refs, types.ImageRef{
				Filename:    ipath,
				OriginalRef: originalRef,
				MimeType:    mimeType,
				ImageData:   imgBytes,
			})
		}
	}

	// Individual unavailable images are optional, but cancellation must stop
	// the parse instead of turning a canceled final download into success.
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return refs, nil
}

// PingPaddleOCRVLCloud checks whether the cloud token is present (the API has
// no lightweight health endpoint, so we only validate configuration here).
func PingPaddleOCRVLCloud(token string) (bool, string) {
	if strings.TrimSpace(token) == "" {
		return false, "PaddleOCR-VL Cloud token is not configured"
	}
	return true, ""
}
