package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// InitializationConfig creates/configures models and splitting for a KB.
// LLM, Embedding and DocumentSplitting are required by InitializeByKB.
// To bind existing models, use SetKBModelConfig instead.
type InitializationConfig struct {
	LLM                *InitializationModelConfig     `json:"llm,omitempty"`
	Embedding          *InitializationModelConfig     `json:"embedding,omitempty"`
	DocumentSplitting  *InitializationSplittingConfig `json:"document_splitting,omitempty"`
	Rerank             json.RawMessage                `json:"rerank,omitempty"`
	Multimodal         json.RawMessage                `json:"multimodal,omitempty"`
	NodeExtract        json.RawMessage                `json:"node_extract,omitempty"`
	QuestionGeneration json.RawMessage                `json:"question_generation,omitempty"`

	// Deprecated: these fields are accepted only by UpdateKBConfig. The
	// initialization endpoint takes model connection settings, not IDs.
	ChatModelID      string `json:"-"`
	EmbeddingModelID string `json:"-"`
	RerankModelID    string `json:"-"`
	MultimodalID     string `json:"-"`
}

// InitializationModelConfig carries provider connection parameters for one model slot.
type InitializationModelConfig struct {
	Source    string `json:"source"`
	ModelName string `json:"model_name"`
	BaseURL   string `json:"base_url,omitempty"`
	APIKey    string `json:"api_key,omitempty"`
	Dimension int    `json:"dimension,omitempty"`
}

// InitializationSplittingConfig carries document chunking parameters for KB initialization.
type InitializationSplittingConfig struct {
	ChunkSize    int      `json:"chunk_size"`
	ChunkOverlap int      `json:"chunk_overlap"`
	Separators   []string `json:"separators"`
}

// KBModelConfigView is the secret-free, read-only model configuration of a
// knowledge base, returned by GetInitializationConfig. The server's read
// response nests config under embedding/llm/rerank/multimodal and INCLUDES
// provider apiKey/baseUrl (for the web config form); this view intentionally
// parses only the non-secret fields, so credentials can never leak through the
// CLI. Field tags are snake_case (the CLI envelope convention), remapped from
// the server's camelCase.
type KBModelConfigView struct {
	RetrievalReady bool               `json:"retrieval_ready"` // embedding model bound → KB can embed/retrieve
	Embedding      ModelSlotView      `json:"embedding"`
	LLM            ModelSlotView      `json:"llm"`
	Rerank         RerankSlotView     `json:"rerank"`
	Multimodal     MultimodalSlotView `json:"multimodal"`
}

// ModelSlotView is one non-secret model slot (embedding / llm).
type ModelSlotView struct {
	Configured bool   `json:"configured"`
	ModelName  string `json:"model_name,omitempty"`
	Source     string `json:"source,omitempty"`
	Dimension  int    `json:"dimension,omitempty"`
}

// RerankSlotView is the rerank slot (may be disabled).
type RerankSlotView struct {
	Enabled   bool   `json:"enabled"`
	ModelName string `json:"model_name,omitempty"`
}

// MultimodalSlotView reports whether multimodal processing is enabled.
type MultimodalSlotView struct {
	Enabled bool `json:"enabled"`
}

// OllamaModelInfo represents info about an Ollama model
type OllamaModelInfo struct {
	Name       string `json:"name"`
	Size       int64  `json:"size"`
	ModifiedAt string `json:"modified_at"`
}

// DownloadTask represents an Ollama model download task
type DownloadTask struct {
	ID        string     `json:"id"`
	ModelName string     `json:"model_name"`
	Status    string     `json:"status"`
	Progress  float64    `json:"progress"`
	Message   string     `json:"message"`
	StartTime time.Time  `json:"start_time"`
	EndTime   *time.Time `json:"end_time,omitempty"`
}

// ModelCheckResult represents the result of checking a remote model
type ModelCheckResult struct {
	Success        bool   `json:"success"` // Normalized result for model checks and multimodal tests
	Available      bool   `json:"available"`
	Message        string `json:"message,omitempty"`
	Dimension      int    `json:"dimension,omitempty"`
	Caption        string `json:"caption,omitempty"`
	OCR            string `json:"ocr,omitempty"`
	ProcessingTime int64  `json:"processing_time,omitempty"`
}

// UnmarshalJSON normalizes model-check ("available") and multimodal-test
// ("success") outcome flags onto both Success and Available.
func (r *ModelCheckResult) UnmarshalJSON(data []byte) error {
	type resultFields ModelCheckResult
	var result struct {
		*resultFields
		Available *bool `json:"available"`
	}
	*r = ModelCheckResult{}
	result.resultFields = (*resultFields)(r)
	if err := json.Unmarshal(data, &result); err != nil {
		return err
	}
	if result.Available != nil {
		r.Available = *result.Available
		r.Success = *result.Available
	} else {
		r.Available = r.Success
	}
	return nil
}

// GetInitializationConfig returns a knowledge base's model configuration as a
// secret-free KBModelConfigView. The server response nests config under
// embedding/llm/rerank/multimodal and includes provider apiKey/baseUrl; this
// parses ONLY the non-secret fields (apiKey/baseUrl are never read into the
// struct, so they cannot leak through the CLI) and remaps to snake_case.
func (c *Client) GetInitializationConfig(ctx context.Context, kbID string) (*KBModelConfigView, error) {
	path := fmt.Sprintf("/api/v1/initialization/config/%s", escapePathSegment(kbID))
	resp, err := c.doRequest(ctx, http.MethodGet, path, nil, nil)
	if err != nil {
		return nil, err
	}
	// Deliberately model only non-secret fields; api_key / base_url in the server
	// payload are ignored by omission.
	var result struct {
		Data struct {
			Embedding struct {
				Source    string `json:"source"`
				ModelName string `json:"model_name"`
				Dimension int    `json:"dimension"`
			} `json:"embedding"`
			LLM struct {
				Source    string `json:"source"`
				ModelName string `json:"model_name"`
			} `json:"llm"`
			Rerank struct {
				Enabled   bool   `json:"enabled"`
				ModelName string `json:"model_name"`
			} `json:"rerank"`
			Multimodal struct {
				Enabled bool `json:"enabled"`
			} `json:"multimodal"`
		} `json:"data"`
	}
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	d := result.Data
	view := &KBModelConfigView{
		RetrievalReady: d.Embedding.ModelName != "",
		Embedding: ModelSlotView{
			Configured: d.Embedding.ModelName != "",
			ModelName:  d.Embedding.ModelName,
			Source:     d.Embedding.Source,
			Dimension:  d.Embedding.Dimension,
		},
		LLM: ModelSlotView{
			Configured: d.LLM.ModelName != "",
			ModelName:  d.LLM.ModelName,
			Source:     d.LLM.Source,
		},
		Rerank:     RerankSlotView{Enabled: d.Rerank.Enabled, ModelName: d.Rerank.ModelName},
		Multimodal: MultimodalSlotView{Enabled: d.Multimodal.Enabled},
	}
	return view, nil
}

// InitializeByKB initializes a knowledge base with model configuration
func (c *Client) InitializeByKB(ctx context.Context, kbID string, config *InitializationConfig) error {
	if config == nil || config.LLM == nil || config.Embedding == nil || config.DocumentSplitting == nil {
		return fmt.Errorf(
			"initialization requires llm, embedding and document_splitting; use SetKBModelConfig to bind model IDs",
		)
	}
	path := fmt.Sprintf("/api/v1/initialization/initialize/%s", escapePathSegment(kbID))
	resp, err := c.doRequest(ctx, http.MethodPost, path, config, nil)
	if err != nil {
		return err
	}
	return parseResponse(resp, nil)
}

// UpdateKBConfig updates the model configuration for a knowledge base.
//
// Deprecated: use SetKBModelConfig. This wrapper maps the legacy flat model
// ID fields to the current server request; it cannot update rerank/VLM models.
func (c *Client) UpdateKBConfig(ctx context.Context, kbID string, config *InitializationConfig) error {
	if config == nil {
		return fmt.Errorf("model configuration is required")
	}
	if config.RerankModelID != "" || config.MultimodalID != "" {
		return fmt.Errorf("UpdateKBConfig supports chat and embedding model IDs only")
	}
	return c.SetKBModelConfig(ctx, kbID, &KBModelConfig{
		LLMModelID:       config.ChatModelID,
		EmbeddingModelID: config.EmbeddingModelID,
	})
}

// KBModelConfig points a knowledge base at already-registered models. Field
// names match the server's KBModelConfigRequest (PUT
// /initialization/config/:kb_id). LLMModelID is required server-side;
// EmbeddingModelID is optional (omitted when RAG indexing is disabled).
type KBModelConfig struct {
	LLMModelID       string `json:"llm_model_id"`
	EmbeddingModelID string `json:"embedding_model_id,omitempty"`
}

// SetKBModelConfig binds a knowledge base to already-registered models via PUT
// /initialization/config/:kb_id. Register models first with CreateModel; the
// server rejects unknown model ids and refuses to change the embedding model of
// a KB that already has documents.
func (c *Client) SetKBModelConfig(ctx context.Context, kbID string, cfg *KBModelConfig) error {
	path := fmt.Sprintf("/api/v1/initialization/config/%s", escapePathSegment(kbID))
	resp, err := c.doRequest(ctx, http.MethodPut, path, cfg, nil)
	if err != nil {
		return err
	}
	return parseResponse(resp, nil)
}

// CheckOllamaStatus checks if Ollama is running and accessible
func (c *Client) CheckOllamaStatus(ctx context.Context) (bool, error) {
	resp, err := c.doRequest(ctx, http.MethodGet, "/api/v1/initialization/ollama/status", nil, nil)
	if err != nil {
		return false, err
	}
	var result struct {
		Success bool `json:"success"`
		Data    struct {
			Available bool `json:"available"`
		} `json:"data"`
	}
	if err := parseResponse(resp, &result); err != nil {
		return false, err
	}
	return result.Data.Available, nil
}

// ListOllamaModels lists all locally available Ollama models
func (c *Client) ListOllamaModels(ctx context.Context) ([]OllamaModelInfo, error) {
	resp, err := c.doRequest(ctx, http.MethodGet, "/api/v1/initialization/ollama/models", nil, nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		Success bool `json:"success"`
		Data    struct {
			Models []OllamaModelInfo `json:"models"`
		} `json:"data"`
	}
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return result.Data.Models, nil
}

// CheckOllamaModels checks if specific Ollama models are available
func (c *Client) CheckOllamaModels(ctx context.Context, models []string) (map[string]bool, error) {
	req := map[string][]string{"models": models}
	resp, err := c.doRequest(ctx, http.MethodPost, "/api/v1/initialization/ollama/models/check", req, nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		Success bool `json:"success"`
		Data    struct {
			Models map[string]bool `json:"models"`
		} `json:"data"`
	}
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return result.Data.Models, nil
}

// DownloadOllamaModel starts downloading an Ollama model
func (c *Client) DownloadOllamaModel(ctx context.Context, modelName string) (*DownloadTask, error) {
	req := map[string]string{"model_name": modelName}
	resp, err := c.doRequest(ctx, http.MethodPost, "/api/v1/initialization/ollama/models/download", req, nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		Success bool `json:"success"`
		Data    struct {
			DownloadTask
			TaskID string `json:"task_id"`
		} `json:"data"`
	}
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	if result.Data.TaskID != "" {
		result.Data.ID = result.Data.TaskID
	}
	return &result.Data.DownloadTask, nil
}

// GetOllamaDownloadProgress gets the download progress of an Ollama model
func (c *Client) GetOllamaDownloadProgress(ctx context.Context, taskID string) (*DownloadTask, error) {
	path := fmt.Sprintf("/api/v1/initialization/ollama/download/progress/%s", escapePathSegment(taskID))
	resp, err := c.doRequest(ctx, http.MethodGet, path, nil, nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		Success bool          `json:"success"`
		Data    *DownloadTask `json:"data"`
	}
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return result.Data, nil
}

// ListOllamaDownloadTasks lists all Ollama download tasks
func (c *Client) ListOllamaDownloadTasks(ctx context.Context) ([]*DownloadTask, error) {
	resp, err := c.doRequest(ctx, http.MethodGet, "/api/v1/initialization/ollama/download/tasks", nil, nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		Success bool            `json:"success"`
		Data    []*DownloadTask `json:"data"`
	}
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return result.Data, nil
}

// CheckRemoteModel checks if a remote model API is accessible
func (c *Client) CheckRemoteModel(ctx context.Context, params map[string]string) (*ModelCheckResult, error) {
	resp, err := c.doRequest(ctx, http.MethodPost, "/api/v1/initialization/remote/check", params, nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		Success bool              `json:"success"`
		Data    *ModelCheckResult `json:"data"`
	}
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return result.Data, nil
}

// TestEmbeddingModel tests an embedding model
func (c *Client) TestEmbeddingModel(ctx context.Context, params map[string]string) (*ModelCheckResult, error) {
	resp, err := c.doRequest(ctx, http.MethodPost, "/api/v1/initialization/embedding/test", params, nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		Success bool              `json:"success"`
		Data    *ModelCheckResult `json:"data"`
	}
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return result.Data, nil
}

// CheckRerankModel checks if a rerank model is accessible
func (c *Client) CheckRerankModel(ctx context.Context, params map[string]string) (*ModelCheckResult, error) {
	resp, err := c.doRequest(ctx, http.MethodPost, "/api/v1/initialization/rerank/check", params, nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		Success bool              `json:"success"`
		Data    *ModelCheckResult `json:"data"`
	}
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return result.Data, nil
}

var multipartQuoteEscaper = strings.NewReplacer("\\", "\\\\", `"`, "\\\"", "\r", "%0D", "\n", "%0A")

func detectMultimodalImageContentType(filename string, head []byte) string {
	if len(head) > 0 {
		sniffed := http.DetectContentType(head)
		if mediaType, _, err := mime.ParseMediaType(sniffed); err == nil && strings.HasPrefix(mediaType, "image/") {
			return mediaType
		}
	}
	switch strings.ToLower(filepath.Ext(filename)) {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".bmp":
		return "image/bmp"
	case ".svg":
		return "image/svg+xml"
	case ".tif", ".tiff":
		return "image/tiff"
	case ".ico":
		return "image/x-icon"
	case ".avif":
		return "image/avif"
	case ".heic":
		return "image/heic"
	case ".heif":
		return "image/heif"
	}
	if extType := mime.TypeByExtension(filepath.Ext(filename)); extType != "" {
		if mediaType, _, err := mime.ParseMediaType(extType); err == nil && strings.HasPrefix(mediaType, "image/") {
			return mediaType
		}
	}
	return "image/png"
}

// TestMultimodalFunction uploads one local image and the model/storage form
// fields to the multimodal test endpoint. Exactly one image path is required.
// The variadic argument preserves source compatibility with old callers,
// which previously could not supply the server's required image at all.
func (c *Client) TestMultimodalFunction(
	ctx context.Context,
	params map[string]string,
	imagePaths ...string,
) (*ModelCheckResult, error) {
	if len(imagePaths) != 1 || imagePaths[0] == "" {
		return nil, fmt.Errorf("multimodal test requires one image path")
	}
	file, err := os.Open(imagePaths[0])
	if err != nil {
		return nil, fmt.Errorf("open multimodal test image: %w", err)
	}
	defer func() { _ = file.Close() }()

	var sniffBuf [512]byte
	n, sniffErr := io.ReadFull(file, sniffBuf[:])
	if sniffErr != nil && sniffErr != io.EOF && sniffErr != io.ErrUnexpectedEOF {
		return nil, fmt.Errorf("read multimodal test image: %w", sniffErr)
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	partHeader := make(textproto.MIMEHeader)
	safeFilename := multipartQuoteEscaper.Replace(filepath.Base(imagePaths[0]))
	partHeader.Set("Content-Disposition", fmt.Sprintf(`form-data; name="image"; filename="%s"`, safeFilename))
	partHeader.Set("Content-Type", detectMultimodalImageContentType(imagePaths[0], sniffBuf[:n]))
	part, err := writer.CreatePart(partHeader)
	if err != nil {
		return nil, err
	}
	if _, err := io.Copy(part, io.MultiReader(bytes.NewReader(sniffBuf[:n]), file)); err != nil {
		return nil, err
	}
	for name, value := range params {
		if err := writer.WriteField(name, value); err != nil {
			return nil, err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	const path = "/api/v1/initialization/multimodal/test"
	if err := validateAPIPath(path); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, &body)
	if err != nil {
		return nil, err
	}
	if err := c.validateBaseURL(req); err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	c.applyAuthHeaders(ctx, req)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	var result struct {
		Success bool              `json:"success"`
		Data    *ModelCheckResult `json:"data"`
	}
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return result.Data, nil
}

// ExtractTextRelations extracts text relations for knowledge graph
func (c *Client) ExtractTextRelations(ctx context.Context, params any) (json.RawMessage, error) {
	resp, err := c.doRequest(ctx, http.MethodPost, "/api/v1/initialization/extract/text-relation", params, nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		Success bool            `json:"success"`
		Data    json.RawMessage `json:"data"`
	}
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return result.Data, nil
}
