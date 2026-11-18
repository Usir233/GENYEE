package handler

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	chatpipeline "github.com/Usir233/GENYEE/internal/application/service/chat_pipeline"
	"github.com/Usir233/GENYEE/internal/assets"
	"github.com/Usir233/GENYEE/internal/errors"
	"github.com/Usir233/GENYEE/internal/handler/dto"
	"github.com/Usir233/GENYEE/internal/logger"
	modelapi "github.com/Usir233/GENYEE/internal/models/api"
	"github.com/Usir233/GENYEE/internal/models/asr"
	"github.com/Usir233/GENYEE/internal/models/chat"
	"github.com/Usir233/GENYEE/internal/models/embedding"
	"github.com/Usir233/GENYEE/internal/models/providers"
	"github.com/Usir233/GENYEE/internal/models/rerank"
	"github.com/Usir233/GENYEE/internal/types"
	"github.com/Usir233/GENYEE/internal/utils"
	"github.com/gin-gonic/gin"
)

// ModelTestRequest 统一的"测试连接"请求体。
//
// 四种模型（chat/embedding/rerank/asr）的测试接口共享同一份结构，以便：
//   - 前端只需维护一份表单 → 后端映射。
//   - 后端可以直接把请求转成 *types.Model，再调用各包的 ConfigFromModel，
//     与生产路径（service.modelService.GetXxxModel）走完全相同的装配流程，
//     彻底消除过去每个测试端点手工拼 Config 的样板代码。
//
// 所有 provider/model 通用字段都在这里集中声明；若未来新增字段（比如现在的
// custom_headers），只需改一处，生产路径和测试路径会同时生效。
type ModelTestRequest struct {
	Spec                      *types.ModelSpecOverride `json:"spec,omitempty"`
	Source                    string                   `json:"source"` // 为空时按需默认为 "remote"
	ModelName                 string                   `json:"model_name" binding:"required"`
	BaseURL                   string                   `json:"base_url"`
	APIKey                    string                   `json:"api_key"`
	Provider                  string                   `json:"provider"`
	InterfaceType             string                   `json:"interface_type,omitempty"`
	Dimension                 int                      `json:"dimension,omitempty"`
	SupportsDimensionOverride bool                     `json:"supports_dimension_override,omitempty"`
	CustomHeaders             map[string]string        `json:"custom_headers,omitempty"`
	ExtraConfig               map[string]string        `json:"extra_config,omitempty"`
	// AppSecret 用于 LKEAP / Volcengine Rerank 等需要第二段密钥的场景（对应模型 Parameters.AppSecret）。
	AppSecret string `json:"app_secret,omitempty"`
	// ModelID, when set, instructs the handler to substitute any missing
	// secrets (APIKey, AppSecret via ExtraConfig) from the stored model
	// record before assembling the test client. This lets the "Test
	// connection" button work on existing models without making the
	// frontend reload — and ship — the plaintext API key. Other fields
	// (BaseURL, ModelName, etc.) on this request still override the
	// stored values, so a user can validate a new endpoint against the
	// existing credentials in one click.
	ModelID string `json:"model_id,omitempty"`
}

// fillSecretsFromStoredModel mutates req in place: if req.ModelID is set
// and a secret field on the request is empty, the corresponding value from
// the stored (and decrypted) model is copied in. The stored ExtraConfig is
// filled in as well when the request does not carry one — provider-specific
// settings (thinking_control, api_version, remote_model_name, ...) must
// apply to the connection test exactly as they apply to real traffic, and
// the frontend only sends extra_config when the user actively edits it.
// Non-empty request values are always preferred — they represent the user
// actively typing a new key they want to verify. Missing or inaccessible
// model is treated as a no-op (the connection test will fail downstream
// with a clearer "missing api_key" error than we could produce here).
func (h *InitializationHandler) fillSecretsFromStoredModel(ctx context.Context, req *ModelTestRequest) {
	if req == nil || req.ModelID == "" {
		return
	}
	// A request that already carries every secret needs no lookup — but a
	// secret stored in extra_config (LKEAP / Volcengine secret_key) is
	// redacted by GET, so "extra_config is present" does not mean it is
	// complete.
	if req.APIKey != "" && req.AppSecret != "" && req.ExtraConfig != nil && req.Spec != nil &&
		dto.HasAllSecretExtras(req.Provider, req.BaseURL, req.ExtraConfig) {
		return
	}
	stored, err := h.modelService.GetModelByID(ctx, req.ModelID)
	if err != nil || stored == nil {
		logger.Warnf(ctx, "test-connection: stored model %s not found, leaving secrets empty: %v",
			utils.SanitizeForLog(req.ModelID), err)
		return
	}
	if req.Spec == nil && req.Provider == stored.Parameters.Provider {
		req.Spec = stored.Parameters.Spec
	}
	if req.APIKey == "" {
		req.APIKey = stored.Parameters.APIKey
	}
	if req.AppSecret == "" {
		req.AppSecret = stored.Parameters.AppSecret
	}
	// Same contract as PUT /models/{id}: an absent or masked secret extra
	// falls back to the stored value, a real one the user just typed wins —
	// and a test against a different vendor gets no stored credential, which
	// belongs to the integration the row is being moved away from.
	req.ExtraConfig = dto.PreserveStoredSecretExtras(
		stored.Parameters.ExtraConfig, req.ExtraConfig,
		dto.VendorRef{Provider: stored.Parameters.Provider, BaseURL: stored.Parameters.BaseURL},
		dto.VendorRef{Provider: req.Provider, BaseURL: req.BaseURL},
	)
}

// RemoteModelCheckRequest 兼容旧 swagger 定义。
//
// Deprecated: 保留是为了不破坏已生成的 API 文档，新代码请直接使用 ModelTestRequest。
type RemoteModelCheckRequest = ModelTestRequest

// decryptModelAppSecret 解密模型 Parameters 中的 AppSecret（与 modelService 行为一致）。
func decryptModelAppSecret(encrypted string) string {
	if encrypted == "" {
		return encrypted
	}
	if key := utils.GetAESKey(); key != nil {
		if plain, err := utils.DecryptAESGCM(encrypted, key); err == nil {
			return plain
		}
	}
	return encrypted
}

// buildTestModel 把测试连接请求转成一个临时的 *types.Model（不落库），
// 供 ConfigFromModel 使用。source 为空时按 defaultSource 兜底（chat/rerank/asr
// 默认 remote，embedding 会根据前端传入的 source 决定）。
func (h *InitializationHandler) buildTestModel(
	req *ModelTestRequest, modelType types.ModelType, defaultSource types.ModelSource,
) *types.Model {
	source := types.ModelSource(strings.ToLower(req.Source))
	if source == "" {
		source = defaultSource
	}
	return &types.Model{
		Name:   req.ModelName,
		Type:   modelType,
		Source: source,
		Parameters: types.ModelParameters{
			BaseURL:       req.BaseURL,
			APIKey:        req.APIKey,
			AppSecret:     req.AppSecret,
			Provider:      req.Provider,
			InterfaceType: req.InterfaceType,
			ExtraConfig:   req.ExtraConfig,
			Spec:          req.Spec,
			CustomHeaders: req.CustomHeaders,
			EmbeddingParameters: types.EmbeddingParameters{
				Dimension:                 req.Dimension,
				TruncatePromptTokens:      256,
				SupportsDimensionOverride: req.SupportsDimensionOverride,
			},
		},
	}
}

// CheckRemoteModel godoc
// @Summary      检查远程模型
// @Description  检查远程API模型连接是否正常
// @Tags         初始化
// @Accept       json
// @Produce      json
// @Param        request  body      RemoteModelCheckRequest  true  "模型检查请求"
// @Success      200      {object}  map[string]interface{}   "检查结果"
// @Failure      400      {object}  errors.AppError          "请求参数错误"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /initialization/remote/check [post]
func (h *InitializationHandler) CheckRemoteModel(c *gin.Context) {
	ctx := c.Request.Context()

	logger.Info(ctx, "Checking remote model connection")

	var req ModelTestRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Error(ctx, "Failed to parse remote model check request", err)
		_ = c.Error(errors.NewBadRequestError(err.Error()))
		return
	}
	h.fillSecretsFromStoredModel(ctx, &req)

	if req.ModelName == "" || req.BaseURL == "" {
		logger.Error(ctx, "Model name and base URL are required")
		_ = c.Error(errors.NewBadRequestError("模型名称和Base URL不能为空"))
		return
	}

	if err := utils.ValidateURLForSSRF(req.BaseURL); err != nil {
		logger.Warnf(ctx, "SSRF validation failed for remote model BaseURL: %v", err)
		_ = c.Error(errors.NewBadRequestError(utils.FormatSSRFError("Base URL", req.BaseURL, err)))
		return
	}
	model := h.buildTestModel(&req, types.ModelTypeKnowledgeQA, types.ModelSourceRemote)
	available, message := h.checkChatModelConnection(ctx, model)

	logger.Infof(ctx, "Remote model check completed, available: %v, message: %s", available, message)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"available": available,
			"message":   message,
		},
	})
}

// TestEmbeddingModel godoc
// @Summary      测试Embedding模型
// @Description  测试Embedding接口是否可用并返回向量维度
// @Tags         初始化
// @Accept       json
// @Produce      json
// @Param        request  body      handler.ModelTestRequest  true  "Embedding测试请求"
// @Success      200      {object}  map[string]interface{}    "测试结果"
// @Failure      400      {object}  errors.AppError           "请求参数错误"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /initialization/embedding/test [post]
func (h *InitializationHandler) TestEmbeddingModel(c *gin.Context) {
	ctx := c.Request.Context()

	logger.Info(ctx, "Testing embedding model connectivity and functionality")

	var req ModelTestRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Error(ctx, "Failed to parse embedding test request", err)
		_ = c.Error(errors.NewBadRequestError(err.Error()))
		return
	}
	h.fillSecretsFromStoredModel(ctx, &req)
	if req.Source == "" {
		req.Source = string(types.ModelSourceRemote)
	}

	if req.BaseURL != "" {
		if err := utils.ValidateURLForSSRF(req.BaseURL); err != nil {
			logger.Warnf(ctx, "SSRF validation failed for embedding BaseURL: %v", err)
			_ = c.Error(errors.NewBadRequestError(utils.FormatSSRFError("Base URL", req.BaseURL, err)))
			return
		}
	}

	// 阿里云多模态 Embedding 模型暂不支持
	if strings.ToLower(req.Provider) == "aliyun" {
		modelNameLower := strings.ToLower(req.ModelName)
		if strings.Contains(modelNameLower, "vision") || strings.Contains(modelNameLower, "multimodal") {
			logger.Infof(ctx, "Aliyun multimodal embedding model not supported: %s", req.ModelName)
			c.JSON(http.StatusOK, gin.H{
				"success": true,
				"data": gin.H{
					"available": false,
					"message":   "阿里云多模态 Embedding 模型暂不支持，请使用纯文本 Embedding 模型（如 text-embedding-v4）",
					"dimension": 0,
				},
			})
			return
		}
	}

	model := h.buildTestModel(&req, types.ModelTypeEmbedding, types.ModelSourceRemote)
	emb, err := embedding.NewEmbedder(embedding.ConfigFromModel(model), h.pooler, h.ollamaService)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{"model": utils.SanitizeForLog(req.ModelName)})
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"data": gin.H{
				"available": false,
				"message":   fmt.Sprintf("创建Embedder失败: %v", err),
				"dimension": 0,
			},
		})
		return
	}

	vec, err := emb.Embed(ctx, "hello")
	if err != nil {
		logger.Error(ctx, "Failed to call embedder", err)
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"data": gin.H{
				"available": false,
				"message":   fmt.Sprintf("调用Embedding失败: %v", err),
				"dimension": 0,
			},
		})
		return
	}

	logger.Infof(ctx, "Embedding test succeeded, dimension: %d", len(vec))
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"available": true,
			"message":   fmt.Sprintf("测试成功，向量维度=%d", len(vec)),
			"dimension": len(vec),
		},
	})
}

// authFailureMarkers are lower-cased fragments that mark an upstream error
// body as a credential problem, whatever status code carried it.
var authFailureMarkers = []string{
	"api_key_invalid", "api key not valid", "invalid api key", "invalid_api_key",
	"unauthorized", "authentication",
}

// isAuthFailureBody reports whether an upstream error body describes a bad
// or missing credential. Gemini's native API answers an invalid key with
// 400 INVALID_ARGUMENT / API_KEY_INVALID, so a 400 alone does not prove the
// key was accepted.
func isAuthFailureBody(body string) bool {
	b := strings.ToLower(body)
	for _, m := range authFailureMarkers {
		if strings.Contains(b, m) {
			return true
		}
	}
	return false
}

// classifyConnectionError maps an upstream error string to a short
// human-readable hint in Chinese. Callers should always combine the hint
// with the raw error message (e.g. fmt.Sprintf("%s：%v", hint, err)) so
// the operator can still see what URL / response body the SDK actually
// got — the hint is for "where to start looking", the raw error is for
// "what actually happened".
func classifyConnectionError(errMsg string) string {
	switch {
	case strings.Contains(errMsg, "401") || isAuthFailureBody(errMsg):
		return "认证失败，请检查API Key"
	case strings.Contains(errMsg, "403") || strings.Contains(errMsg, "forbidden"):
		return "权限不足，请检查API Key权限"
	case strings.Contains(errMsg, "404") || strings.Contains(errMsg, "not found"):
		return "API端点不存在，请检查Base URL"
	case strings.Contains(errMsg, "timeout") || strings.Contains(errMsg, "context deadline exceeded"):
		return "连接超时，请检查网络连接"
	case strings.Contains(errMsg, "connection refused") ||
		strings.Contains(errMsg, "no such host") ||
		strings.Contains(errMsg, "dial tcp"):
		return "无法连接到服务器，请检查Base URL"
	default:
		return "连接失败"
	}
}

// checkChatModelConnection 使用 chat 模块做一次最小化调用来测试连通性与鉴权。
// 与生产路径走完全相同的 ConfigFromModel → NewChat 流程，因此 CustomHeaders、
// ExtraConfig、Provider 等字段都会被正确透传。
func (h *InitializationHandler) checkChatModelConnection(
	ctx context.Context, model *types.Model,
) (bool, string) {
	chatInstance, err := chat.NewChat(chat.ConfigFromModel(model), h.ollamaService)
	if err != nil {
		return false, fmt.Sprintf("创建聊天实例失败: %v", err)
	}

	testMessages := []chat.Message{{Role: "user", Content: "test"}}
	testOptions := &chat.ChatOptions{
		MaxTokens: 1,
		Thinking:  &[]bool{false}[0], // for dashscope.aliyuncs qwen3-32b
	}

	_, err = chatInstance.Chat(ctx, testMessages, testOptions)
	if err != nil {
		// 400 = endpoint reachable + auth ok, just a parameter mismatch
		// (e.g. max_tokens vs max_completion_tokens, or a reasoning model
		// that cannot finish within the 1-token probe). Treat as success.
		// Match the status code, not the message: the wording belongs to
		// whichever HTTP client built the error and has changed before.
		// Exception: some vendors (Gemini native) report an invalid key as
		// 400, which must still fail the check.
		var httpErr *modelapi.HTTPError
		if stderrors.As(err, &httpErr) && httpErr.StatusCode == http.StatusBadRequest &&
			!isAuthFailureBody(httpErr.Body) {
			return true, "连接正常，模型可用"
		}
		errMsg := err.Error()
		return false, fmt.Sprintf("%s：%v", classifyConnectionError(errMsg), err)
	}

	// 连接成功，模型可用
	return true, "连接正常，模型可用"
}

// checkRerankModelConnection 使用 rerank 模块做一次最小化调用来测试连通性与鉴权。
// 与生产路径共用 ConfigFromModel，所有字段（CustomHeaders 等）都透传。
func (h *InitializationHandler) checkRerankModelConnection(
	ctx context.Context, model *types.Model, appSecret string,
) (bool, string) {
	reranker, err := rerank.NewReranker(rerank.ConfigFromModel(model, appSecret))
	if err != nil {
		return false, fmt.Sprintf("创建Reranker失败: %v", err)
	}

	results, err := reranker.Rerank(ctx, "ping", []string{"pong"})
	if err != nil {
		return false, fmt.Sprintf("重排测试失败: %v", err)
	}
	if len(results) > 0 {
		return true, fmt.Sprintf("重排功能正常，返回%d个结果", len(results))
	}
	return false, "重排接口连接成功，但未返回重排结果"
}

// CheckRerankModel godoc
// @Summary      检查Rerank模型
// @Description  检查Rerank模型连接和功能是否正常
// @Tags         初始化
// @Accept       json
// @Produce      json
// @Param        request  body      handler.ModelTestRequest  true  "Rerank检查请求"
// @Success      200      {object}  map[string]interface{}    "检查结果"
// @Failure      400      {object}  errors.AppError           "请求参数错误"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /initialization/rerank/check [post]
func (h *InitializationHandler) CheckRerankModel(c *gin.Context) {
	ctx := c.Request.Context()

	logger.Info(ctx, "Checking rerank model connection and functionality")

	var req ModelTestRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Error(ctx, "Failed to parse rerank model check request", err)
		_ = c.Error(errors.NewBadRequestError(err.Error()))
		return
	}
	h.fillSecretsFromStoredModel(ctx, &req)

	if req.ModelName == "" || req.BaseURL == "" {
		logger.Error(ctx, "Model name and base URL are required")
		_ = c.Error(errors.NewBadRequestError("模型名称和Base URL不能为空"))
		return
	}

	if err := utils.ValidateURLForSSRF(req.BaseURL); err != nil {
		logger.Warnf(ctx, "SSRF validation failed for rerank BaseURL: %v", err)
		_ = c.Error(errors.NewBadRequestError(utils.FormatSSRFError("Base URL", req.BaseURL, err)))
		return
	}

	appSecret := decryptModelAppSecret(req.AppSecret)

	model := h.buildTestModel(&req, types.ModelTypeRerank, types.ModelSourceRemote)
	// LKEAP and Volcengine rerank sign with a key pair stored on the row
	// itself.
	if p := model.Parameters.Provider; p == providers.LkeapID || p == providers.VolcengineID {
		appSecret = decryptModelAppSecret(model.Parameters.AppSecret)
	}
	available, message := h.checkRerankModelConnection(ctx, model, appSecret)

	logger.Infof(ctx, "Rerank model check completed, available: %v, message: %s", available, message)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"available": available,
			"message":   message,
		},
	})
}

// CheckASRModel godoc
// @Summary      检查ASR模型
// @Description  检查ASR（语音识别）模型连接是否正常，通过发送一段静默音频测试 /v1/audio/transcriptions 端点
// @Tags         初始化
// @Accept       json
// @Produce      json
// @Param        request  body      handler.ModelTestRequest  true  "ASR检查请求"
// @Success      200      {object}  map[string]interface{}    "检查结果"
// @Failure      400      {object}  errors.AppError           "请求参数错误"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /initialization/asr/check [post]
func (h *InitializationHandler) CheckASRModel(c *gin.Context) {
	ctx := c.Request.Context()

	logger.Info(ctx, "Checking ASR model connection")

	var req ModelTestRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Error(ctx, "Failed to parse ASR model check request", err)
		_ = c.Error(errors.NewBadRequestError(err.Error()))
		return
	}
	h.fillSecretsFromStoredModel(ctx, &req)

	if req.ModelName == "" || req.BaseURL == "" {
		logger.Error(ctx, "Model name and base URL are required for ASR check")
		_ = c.Error(errors.NewBadRequestError("模型名称和Base URL不能为空"))
		return
	}

	if err := utils.ValidateURLForSSRF(req.BaseURL); err != nil {
		logger.Warnf(ctx, "SSRF validation failed for ASR BaseURL: %v", err)
		_ = c.Error(errors.NewBadRequestError(utils.FormatSSRFError("Base URL", req.BaseURL, err)))
		return
	}

	// 用统一构造器生成测试用 *types.Model（ASR 不涉及 模型附加凭证），
	// 发送一段极短的静默 WAV 音频验证 /v1/audio/transcriptions 端点可达。
	model := h.buildTestModel(&req, types.ModelTypeASR, types.ModelSourceRemote)
	asrInstance, err := asr.NewASR(asr.ConfigFromModel(model))
	if err != nil {
		logger.Errorf(ctx, "Failed to create ASR instance for check: %v", err)
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"data": gin.H{
				"available": false,
				"message":   fmt.Sprintf("创建ASR实例失败: %v", err),
			},
		})
		return
	}

	res, err := asrInstance.Transcribe(ctx, assets.ASRTestWAV, "asr_test.wav")
	var text string
	if res != nil {
		text = res.Text
	}
	available := true
	message := "ASR连接成功"

	if err != nil {
		errMsg := err.Error()
		switch {
		case strings.Contains(errMsg, "401") ||
			strings.Contains(errMsg, "Unauthorized") ||
			strings.Contains(errMsg, "authentication"):
			available = false
			message = fmt.Sprintf("认证失败，请检查API Key：%s", errMsg)
		case strings.Contains(errMsg, "403") || strings.Contains(errMsg, "Forbidden"):
			available = false
			message = fmt.Sprintf("访问被拒绝，请检查API Key权限或网络代理/安全软件拦截：%s", errMsg)
		case strings.Contains(errMsg, "timeout") || strings.Contains(errMsg, "context deadline exceeded"):
			available = false
			message = fmt.Sprintf("连接超时，请检查网络连接：%s", errMsg)
		case strings.Contains(errMsg, "404") || strings.Contains(errMsg, "Not Found"):
			available = false
			message = fmt.Sprintf("API端点不存在，请检查Base URL：%s", errMsg)
		case strings.Contains(errMsg, "connection refused") ||
			strings.Contains(errMsg, "no such host") ||
			strings.Contains(errMsg, "dial tcp"):
			available = false
			message = fmt.Sprintf("无法连接到服务器，请检查Base URL：%s", errMsg)
		case strings.Contains(errMsg, "model") && strings.Contains(errMsg, "not found"):
			available = false
			message = fmt.Sprintf("模型不存在，请检查模型名称：%s", errMsg)
		default:
			logger.Infof(ctx, "ASR check got non-fatal error (endpoint reachable): %v", err)
			available = true
			message = fmt.Sprintf("ASR端点可达（非致命错误: %s）", errMsg)
		}
	} else if text != "" {
		message = fmt.Sprintf("ASR连接成功，转写结果: %s", text)
	}

	logger.Infof(ctx, "ASR model check completed, available: %v, message: %s", available, message)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"available": available,
			"message":   message,
		},
	})
}

// 使用结构体解析表单数据
type testMultimodalForm struct {
	VLMModel         string `form:"vlm_model"`
	VLMBaseURL       string `form:"vlm_base_url"`
	VLMAPIKey        string `form:"vlm_api_key"`
	VLMInterfaceType string `form:"vlm_interface_type"`

	StorageType string `form:"storage_type"`

	// COS 配置
	COSSecretID   string `form:"cos_secret_id"`
	COSSecretKey  string `form:"cos_secret_key"`
	COSRegion     string `form:"cos_region"`
	COSBucketName string `form:"cos_bucket_name"`
	COSAppID      string `form:"cos_app_id"`
	COSPathPrefix string `form:"cos_path_prefix"`

	// MinIO 配置（当存储为 minio 时）
	MinioBucketName string `form:"minio_bucket_name"`
	MinioPathPrefix string `form:"minio_path_prefix"`

	// 文档切分配置（字符串后续自行解析，以避免类型绑定失败）
	ChunkSize     string `form:"chunk_size"`
	ChunkOverlap  string `form:"chunk_overlap"`
	SeparatorsRaw string `form:"separators"`
}

// TestMultimodalFunction godoc
// @Summary      测试多模态功能
// @Description  上传图片测试多模态处理功能
// @Tags         初始化
// @Accept       multipart/form-data
// @Produce      json
// @Param        image               formData  file    true   "测试图片"
// @Param        vlm_model           formData  string  true   "VLM模型名称"
// @Param        vlm_base_url        formData  string  true   "VLM Base URL"
// @Param        vlm_api_key         formData  string  false  "VLM API Key"
// @Param        vlm_interface_type  formData  string  false  "VLM接口类型"
// @Param        storage_type        formData  string  true   "存储类型(cos/minio)"
// @Success      200                 {object}  map[string]interface{}  "测试结果"
// @Failure      400                 {object}  errors.AppError         "请求参数错误"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /initialization/multimodal/test [post]
func (h *InitializationHandler) TestMultimodalFunction(c *gin.Context) {
	ctx := c.Request.Context()

	logger.Info(ctx, "Testing multimodal functionality")

	// 验证文件大小 — MAX_FILE_SIZE_MB env (50MB 默认)。
	// 见 utils/filesize.go 注释：故意保留为部署期 env，不做 runtime setting。
	maxSizeMB := utils.GetMaxFileSizeMB()
	maxSize := maxSizeMB * 1024 * 1024
	// 上限必须在 multipart 解析（ShouldBind / FormFile）之前生效，否则 Gin 会先把整个
	// 请求体缓冲到内存和临时文件，之后的限制无法阻止大文件放大。
	limitUploadBody(c, maxSize)

	var req testMultimodalForm
	if err := c.ShouldBind(&req); err != nil {
		if isRequestBodyTooLarge(err) {
			logger.Error(ctx, "File size too large")
			_ = c.Error(newRequestEntityTooLargeError(fmt.Sprintf("图片文件大小不能超过%dMB", maxSizeMB)))
			return
		}
		logger.Error(ctx, "Failed to parse form data", err)
		_ = c.Error(errors.NewBadRequestError("表单参数解析失败"))
		return
	}
	// ollama 场景自动拼接 base url
	if req.VLMInterfaceType == "ollama" {
		req.VLMBaseURL = os.Getenv("OLLAMA_BASE_URL") + "/v1"
	}

	req.StorageType = strings.ToLower(req.StorageType)

	if req.VLMModel == "" || req.VLMBaseURL == "" {
		logger.Error(ctx, "VLM model name and base URL are required")
		_ = c.Error(errors.NewBadRequestError("VLM模型名称和Base URL不能为空"))
		return
	}

	// SSRF validation for VLM BaseURL
	if err := utils.ValidateURLForSSRF(req.VLMBaseURL); err != nil {
		logger.Warnf(ctx, "SSRF validation failed for VLM BaseURL: %v", err)
		_ = c.Error(errors.NewBadRequestError(utils.FormatSSRFError("VLM Base URL", req.VLMBaseURL, err)))
		return
	}

	switch req.StorageType {
	case "cos":
		// 必填：SecretID/SecretKey/Region/BucketName/AppID；PathPrefix 可选
		if req.COSSecretID == "" || req.COSSecretKey == "" ||
			req.COSRegion == "" || req.COSBucketName == "" ||
			req.COSAppID == "" {
			logger.Error(ctx, "COS configuration is required")
			_ = c.Error(errors.NewBadRequestError("COS配置信息不能为空"))
			return
		}
	case "minio":
		if req.MinioBucketName == "" {
			logger.Error(ctx, "MinIO configuration is required")
			_ = c.Error(errors.NewBadRequestError("MinIO配置信息不能为空"))
			return
		}
	default:
		logger.Error(ctx, "Invalid storage type")
		_ = c.Error(errors.NewBadRequestError("无效的存储类型"))
		return
	}

	// 获取上传的图片文件
	file, header, err := c.Request.FormFile("image")
	if err != nil {
		if isRequestBodyTooLarge(err) {
			logger.Error(ctx, "File size too large")
			_ = c.Error(newRequestEntityTooLargeError(fmt.Sprintf("图片文件大小不能超过%dMB", maxSizeMB)))
			return
		}
		logger.Error(ctx, "Failed to get uploaded image", err)
		_ = c.Error(errors.NewBadRequestError("获取上传图片失败"))
		return
	}
	defer func() { _ = file.Close() }()

	// 验证文件类型
	if !strings.HasPrefix(header.Header.Get("Content-Type"), "image/") {
		logger.Error(ctx, "Invalid file type, only images are allowed")
		_ = c.Error(errors.NewBadRequestError("只允许上传图片文件"))
		return
	}

	if header.Size > maxSize {
		logger.Error(ctx, "File size too large")
		_ = c.Error(newRequestEntityTooLargeError(fmt.Sprintf("图片文件大小不能超过%dMB", maxSizeMB)))
		return
	}
	logger.Infof(ctx, "Processing image: %s", utils.SanitizeForLog(header.Filename))

	// 解析文档分割配置
	chunkSizeInt32, err := strconv.ParseInt(req.ChunkSize, 10, 32)
	if err != nil {
		logger.Error(ctx, "Failed to parse chunk size", err)
		_ = c.Error(errors.NewBadRequestError("Failed to parse chunk size"))
		return
	}
	chunkSize := int32(chunkSizeInt32)
	if chunkSize < 100 || chunkSize > 10000 {
		chunkSize = 1000
	}

	chunkOverlapInt32, err := strconv.ParseInt(req.ChunkOverlap, 10, 32)
	if err != nil {
		logger.Error(ctx, "Failed to parse chunk overlap", err)
		_ = c.Error(errors.NewBadRequestError("Failed to parse chunk overlap"))
		return
	}
	chunkOverlap := int32(chunkOverlapInt32)
	if chunkOverlap < 0 || chunkOverlap >= chunkSize {
		chunkOverlap = 200
	}

	var separators []string
	if req.SeparatorsRaw != "" {
		if err := json.Unmarshal([]byte(req.SeparatorsRaw), &separators); err != nil {
			separators = []string{"\n\n", "\n", "。", "！", "？", ";", "；"}
		}
	} else {
		separators = []string{"\n\n", "\n", "。", "！", "？", ";", "；"}
	}

	// 读取图片文件内容（受限于 maxSize+1，防止直接调用时无界读取）
	imageContent, err := io.ReadAll(io.LimitReader(file, maxSize+1))
	if err != nil {
		logger.Error(ctx, "Failed to read image file", err)
		_ = c.Error(errors.NewBadRequestError("读取图片文件失败"))
		return
	}
	if int64(len(imageContent)) > maxSize {
		_ = c.Error(newRequestEntityTooLargeError(fmt.Sprintf("图片文件大小不能超过%dMB", maxSizeMB)))
		return
	}

	// 调用多模态测试
	startTime := time.Now()
	result, err := h.testMultimodalWithDocReader(
		ctx,
		imageContent, header.Filename,
		chunkSize, chunkOverlap, separators, &req,
	)
	processingTime := time.Since(startTime).Milliseconds()

	if err != nil {
		logger.Error(ctx, "Failed to test multimodal", err)
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"data": gin.H{
				"success":         false,
				"message":         err.Error(),
				"processing_time": processingTime,
			},
		})
		return
	}

	logger.Infof(ctx, "Multimodal test completed successfully in %dms", processingTime)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"success":         true,
			"caption":         result["caption"],
			"ocr":             result["ocr"],
			"processing_time": processingTime,
		},
	})
}

// testMultimodalWithDocReader uses DocumentReader.Read for document reading,
// then returns basic information about the result.
func (h *InitializationHandler) testMultimodalWithDocReader(
	ctx context.Context,
	imageContent []byte, filename string,
	_, _ int32, _ []string,
	_ *testMultimodalForm,
) (map[string]string, error) {
	fileExt := ""
	if idx := strings.LastIndex(filename, "."); idx != -1 {
		fileExt = strings.ToLower(filename[idx+1:])
	}

	if h.documentReader == nil {
		return nil, fmt.Errorf("DocReader service not configured")
	}

	requestID, _ := types.RequestIDFromContext(ctx)

	readResult, err := h.documentReader.Read(ctx, &types.ReadRequest{
		FileContent: imageContent,
		FileName:    filename,
		FileType:    fileExt,
		RequestID:   requestID,
	})
	if err != nil {
		return nil, fmt.Errorf("调用DocReader服务失败: %w", err)
	}
	if readResult.Error != "" {
		return nil, fmt.Errorf("DocReader服务返回错误: %s", readResult.Error)
	}

	result := map[string]string{
		"markdown": readResult.MarkdownContent,
		"caption":  "",
		"ocr":      "",
	}
	return result, nil
}

// TextRelationExtractionRequest 文本关系提取请求结构
type TextRelationExtractionRequest struct {
	Text    string   `json:"text"     binding:"required"`
	Tags    []string `json:"tags"     binding:"required"`
	ModelID string   `json:"model_id" binding:"required"`
}

// TextRelationExtractionResponse 文本关系提取响应结构
type TextRelationExtractionResponse struct {
	Nodes     []*types.GraphNode     `json:"nodes"`
	Relations []*types.GraphRelation `json:"relations"`
}

// ExtractTextRelations godoc
// @Summary      提取文本关系
// @Description  从文本中提取实体和关系
// @Tags         初始化
// @Accept       json
// @Produce      json
// @Param        request  body      TextRelationExtractionRequest                                true  "提取请求"
// @Success      200      {object}  handler.DataResponse{data=handler.TextRelationExtractionResponse}  "提取结果"
// @Failure      400      {object}  errors.AppError                                              "请求参数错误"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /initialization/extract/text-relation [post]
func (h *InitializationHandler) ExtractTextRelations(c *gin.Context) {
	ctx := c.Request.Context()

	var req TextRelationExtractionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Error(ctx, "文本关系提取请求参数错误")
		_ = c.Error(errors.NewBadRequestError("文本关系提取请求参数错误"))
		return
	}

	// 验证文本内容
	if len(req.Text) == 0 {
		_ = c.Error(errors.NewBadRequestError("文本内容不能为空"))
		return
	}

	if len(req.Text) > 5000 {
		_ = c.Error(errors.NewBadRequestError("文本内容长度不能超过5000字符"))
		return
	}

	// 验证标签
	if len(req.Tags) == 0 {
		_ = c.Error(errors.NewBadRequestError("至少需要选择一个关系标签"))
		return
	}

	// 根据模型ID获取chat模型
	chatModel, err := h.modelService.GetChatModel(ctx, req.ModelID)
	if err != nil {
		logger.Error(ctx, "获取模型失败", err)
		_ = c.Error(errors.NewBadRequestError("获取模型失败: " + err.Error()))
		return
	}

	// 调用模型服务进行文本关系提取
	result, err := h.extractRelationsFromText(ctx, req.Text, req.Tags, chatModel)
	if err != nil {
		logger.Error(ctx, "文本关系提取失败", err)
		_ = c.Error(errors.NewInternalServerError("文本关系提取失败: " + err.Error()))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    result,
	})
}

// extractRelationsFromText 从文本中提取关系
func (h *InitializationHandler) extractRelationsFromText(
	ctx context.Context,
	text string,
	tags []string,
	chatModel chat.Chat,
) (*TextRelationExtractionResponse, error) {
	template := &types.PromptTemplateStructured{
		Description: h.config.ExtractManager.ExtractGraph.Description,
		Tags:        tags,
		Examples:    h.config.ExtractManager.ExtractGraph.Examples,
	}

	extractor := chatpipeline.NewExtractor(chatModel, template)
	graph, err := extractor.Extract(ctx, text)
	if err != nil {
		logger.Error(ctx, "文本关系提取失败", err)
		return nil, err
	}
	extractor.RemoveUnknownRelation(ctx, graph)

	result := &TextRelationExtractionResponse{
		Nodes:     graph.Node,
		Relations: graph.Relation,
	}

	return result, nil
}

// FabriTextRequest is a request for generating example text
type FabriTextRequest struct {
	Tags    []string `json:"tags"`
	ModelID string   `json:"model_id" binding:"required"`
}

// FabriTextResponse is a response for generating example text
type FabriTextResponse struct {
	Text string `json:"text"`
}

// FabriText godoc
// @Summary      生成示例文本
// @Description  根据标签生成示例文本
// @Tags         初始化
// @Accept       json
// @Produce      json
// @Param        request  body      FabriTextRequest                                    true  "生成请求"
// @Success      200      {object}  handler.DataResponse{data=handler.FabriTextResponse}  "生成的文本"
// @Failure      400      {object}  errors.AppError                                     "请求参数错误"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /initialization/extract/fabri-text [post]
func (h *InitializationHandler) FabriText(c *gin.Context) {
	ctx := c.Request.Context()

	var req FabriTextRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Error(ctx, "failed to parse fabri text request")
		_ = c.Error(errors.NewBadRequestError("invalid fabri text request parameters"))
		return
	}

	chatModel, err := h.modelService.GetChatModel(ctx, req.ModelID)
	if err != nil {
		logger.Error(ctx, "获取模型失败", err)
		_ = c.Error(errors.NewBadRequestError("获取模型失败: " + err.Error()))
		return
	}

	result, err := h.fabriText(ctx, req.Tags, chatModel)
	if err != nil {
		logger.Error(ctx, "failed to generate fabri text", err)
		_ = c.Error(errors.NewInternalServerError("failed to generate fabri text: " + err.Error()))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    FabriTextResponse{Text: result},
	})
}

// fabriText generates example text
func (h *InitializationHandler) fabriText(ctx context.Context, tags []string, chatModel chat.Chat) (string, error) {
	content := h.config.ExtractManager.FabriText.WithNoTag
	if len(tags) > 0 {
		tagStr, _ := json.Marshal(tags)
		content = fmt.Sprintf(h.config.ExtractManager.FabriText.WithTag, string(tagStr))
	}

	think := false
	result, err := chatModel.Chat(ctx, []chat.Message{
		{Role: "user", Content: content},
	}, &chat.ChatOptions{
		Temperature: 0.3,
		MaxTokens:   4096,
		Thinking:    &think,
	})
	if err != nil {
		logger.Error(ctx, "生成示例文本失败", err)
		return "", err
	}
	return result.Content, nil
}

// FabriTagRequest is a request for generating tags
type FabriTagRequest struct{}

// FabriTagResponse is a response for generating tags
type FabriTagResponse struct {
	Tags []string `json:"tags"`
}

var tagOptions = []string{
	"Content", "Culture", "Person", "Event", "Time", "Location",
	"Work", "Author", "Relation", "Attribute",
}

// FabriTag godoc
// @Summary      生成随机标签
// @Description  随机生成一组标签
// @Tags         初始化
// @Accept       json
// @Produce      json
// @Success      200  {object}  handler.DataResponse{data=handler.FabriTagResponse}  "生成的标签"
// @Router       /initialization/extract/fabri-tag [post]
func (h *InitializationHandler) FabriTag(c *gin.Context) {
	tagRandom := RandomSelect(tagOptions, rand.Intn(len(tagOptions)-1)+1)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    FabriTagResponse{Tags: tagRandom},
	})
}

// RandomSelect selects random strings
func RandomSelect(strs []string, n int) []string {
	if n <= 0 {
		return []string{}
	}
	result := make([]string, len(strs))
	copy(result, strs)
	rand.Shuffle(len(result), func(i, j int) {
		result[i], result[j] = result[j], result[i]
	})

	if n > len(strs) {
		n = len(strs)
	}
	return result[:n]
}
