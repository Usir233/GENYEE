package handler

import (
	"context"
	stderrors "errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Usir233/GENYEE/internal/config"
	"github.com/Usir233/GENYEE/internal/errors"
	"github.com/Usir233/GENYEE/internal/handler/dto"
	"github.com/Usir233/GENYEE/internal/logger"
	"github.com/Usir233/GENYEE/internal/middleware"
	"github.com/Usir233/GENYEE/internal/models/embedding"
	"github.com/Usir233/GENYEE/internal/models/utils/ollama"
	"github.com/Usir233/GENYEE/internal/types"
	"github.com/Usir233/GENYEE/internal/types/interfaces"
	"github.com/Usir233/GENYEE/internal/utils"
	"github.com/gin-gonic/gin"
)

// InitializationHandler 初始化处理器
type InitializationHandler struct {
	config           *config.Config
	tenantService    interfaces.TenantService
	modelService     interfaces.ModelService
	kbService        interfaces.KnowledgeBaseService
	knowledgeService interfaces.KnowledgeService
	ollamaService    *ollama.OllamaService
	documentReader   interfaces.DocumentReader
	pooler           embedding.EmbedderPooler
	storageResolver  interfaces.StorageBackendResolver
}

// NewInitializationHandler 创建初始化处理器
func NewInitializationHandler(
	config *config.Config,
	tenantService interfaces.TenantService,
	modelService interfaces.ModelService,
	kbService interfaces.KnowledgeBaseService,
	knowledgeService interfaces.KnowledgeService,
	ollamaService *ollama.OllamaService,
	documentReader interfaces.DocumentReader,
	pooler embedding.EmbedderPooler,
	storageResolver interfaces.StorageBackendResolver,
) *InitializationHandler {
	return &InitializationHandler{
		config:           config,
		tenantService:    tenantService,
		modelService:     modelService,
		kbService:        kbService,
		knowledgeService: knowledgeService,
		ollamaService:    ollamaService,
		documentReader:   documentReader,
		pooler:           pooler,
		storageResolver:  storageResolver,
	}
}

// KBModelConfigRequest 知识库模型配置请求（简化版，只传模型ID）
type KBModelConfigRequest struct {
	LLMModelID       string `json:"llm_model_id"       binding:"required"`
	EmbeddingModelID string `json:"embedding_model_id"` // optional when RAG indexing is disabled
	VLMConfig        *struct {
		Enabled             *bool   `json:"enabled"`
		ModelID             *string `json:"model_id"`
		DescriptionLanguage *string `json:"description_language"`
		CustomInstructions  *string `json:"custom_instructions"`
		ModelName           *string `json:"model_name"`
		BaseURL             *string `json:"base_url"`
		APIKey              *string `json:"api_key"`
		InterfaceType       *string `json:"interface_type"`
	} `json:"vlm_config"`
	ASRConfig *struct {
		Enabled  *bool   `json:"enabled"`
		ModelID  *string `json:"model_id"`
		Language *string `json:"language"`
	} `json:"asr_config"`

	// 文档分块配置
	DocumentSplitting struct {
		ChunkSize         *int                      `json:"chunk_size"`
		ChunkOverlap      *int                      `json:"chunk_overlap"`
		Separators        *[]string                 `json:"separators"`
		ParserEngineRules *[]types.ParserEngineRule `json:"parser_engine_rules,omitempty"`
		EnableParentChild *bool                     `json:"enable_parent_child"`
		ParentChunkSize   *int                      `json:"parent_chunk_size,omitempty"`
		ChildChunkSize    *int                      `json:"child_chunk_size,omitempty"`
		// Strategy / TokenLimit / Languages use pointer types so the
		// handler can distinguish "field absent in payload" (no change)
		// from "field present with empty/zero value" (clear / disable).
		// Without that distinction, users could set strategy="auto" once
		// but never reset it back to legacy / unset.
		Strategy                  *string   `json:"strategy,omitempty"`
		TokenLimit                *int      `json:"token_limit,omitempty"`
		Languages                 *[]string `json:"languages,omitempty"`
		TableMetadataInstructions *string   `json:"table_metadata_instructions,omitempty"`
	} `json:"document_splitting"`

	// 多模态配置（仅模型相关；存储引擎在 storage_provider 中配置）
	Multimodal struct {
		Enabled *bool `json:"enabled"`
	} `json:"multimodal"`

	// 存储引擎选择（"local" | "minio" | "cos"），影响文档上传与文档内图片存储，参数从全局设置读取
	StorageProvider  string `json:"storage_provider"`
	StorageBackendID string `json:"storage_backend_id"`

	// 知识图谱配置
	NodeExtract struct {
		Enabled            *bool                  `json:"enabled"`
		Text               *string                `json:"text"`
		Tags               *[]string              `json:"tags"`
		Nodes              *[]types.GraphNode     `json:"nodes"`
		Relations          *[]types.GraphRelation `json:"relations"`
		CustomInstructions *string                `json:"custom_instructions"`
	} `json:"node_extract"`

	// 问题生成配置
	QuestionGeneration struct {
		ModelID            *string `json:"model_id,omitempty"`
		Enabled            *bool   `json:"enabled"`
		QuestionCount      *int    `json:"question_count"`
		CustomInstructions *string `json:"custom_instructions"`
	} `json:"question_generation"`
}

// InitializationRequest 初始化请求结构
type InitializationRequest struct {
	LLM struct {
		Source    string `json:"source" binding:"required"`
		ModelName string `json:"model_name" binding:"required"`
		BaseURL   string `json:"base_url"`
		APIKey    string `json:"api_key"`
	} `json:"llm" binding:"required"`

	Embedding struct {
		Source    string `json:"source" binding:"required"`
		ModelName string `json:"model_name" binding:"required"`
		BaseURL   string `json:"base_url"`
		APIKey    string `json:"api_key"`
		Dimension int    `json:"dimension"` // 添加embedding维度字段
	} `json:"embedding" binding:"required"`

	Rerank struct {
		Enabled   bool   `json:"enabled"`
		ModelName string `json:"model_name"`
		BaseURL   string `json:"base_url"`
		APIKey    string `json:"api_key"`
	} `json:"rerank"`

	Multimodal struct {
		Enabled bool `json:"enabled"`
		VLM     *struct {
			ModelName     string `json:"model_name"`
			BaseURL       string `json:"base_url"`
			APIKey        string `json:"api_key"`
			InterfaceType string `json:"interface_type"` // "ollama" or "openai"
		} `json:"vlm,omitempty"`
		StorageType string `json:"storage_type"`
		COS         *struct {
			SecretID   string `json:"secret_id"`
			SecretKey  string `json:"secret_key"`
			Region     string `json:"region"`
			BucketName string `json:"bucket_name"`
			AppID      string `json:"app_id"`
			PathPrefix string `json:"path_prefix"`
		} `json:"cos,omitempty"`
		Minio *struct {
			BucketName string `json:"bucket_name"`
			PathPrefix string `json:"path_prefix"`
		} `json:"minio,omitempty"`
	} `json:"multimodal"`

	DocumentSplitting struct {
		ChunkSize    int      `json:"chunk_size" binding:"required,min=100,max=10000"`
		ChunkOverlap int      `json:"chunk_overlap" binding:"min=0"`
		Separators   []string `json:"separators" binding:"required,min=1"`
	} `json:"document_splitting" binding:"required"`

	NodeExtract struct {
		Enabled bool     `json:"enabled"`
		Text    string   `json:"text"`
		Tags    []string `json:"tags"`
		Nodes   []struct {
			Name       string   `json:"name"`
			Attributes []string `json:"attributes"`
		} `json:"nodes"`
		Relations []struct {
			Node1 string `json:"node1"`
			Node2 string `json:"node2"`
			Type  string `json:"type"`
		} `json:"relations"`
	} `json:"node_extract"`

	QuestionGeneration struct {
		Enabled       bool `json:"enabled"`
		QuestionCount int  `json:"question_count"`
	} `json:"question_generation"`
}

// UpdateKBConfig godoc
// @Summary      更新知识库配置
// @Description  根据知识库ID更新模型和分块配置
// @Tags         初始化
// @Accept       json
// @Produce      json
// @Param        kb_id    path      string               true  "知识库ID"
// @Param        request  body      KBModelConfigRequest true  "配置请求"
// @Success      200      {object}  handler.MessageResponse  "更新成功"
// @Failure      400      {object}  errors.AppError         "请求参数错误"
// @Failure      404      {object}  errors.AppError         "知识库不存在"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /initialization/config/{kb_id} [put]
func (h *InitializationHandler) UpdateKBConfig(c *gin.Context) {
	ctx := c.Request.Context()
	kbIDStr := utils.SanitizeForLog(c.Param("kb_id"))

	var req KBModelConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Error(ctx, "Failed to parse KB config request", err)
		_ = c.Error(errors.NewBadRequestError(err.Error()))
		return
	}

	// 获取知识库信息
	kb, err := h.kbService.GetKnowledgeBaseByID(ctx, kbIDStr)
	if err != nil || kb == nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{"kb_id": kbIDStr})
		_ = c.Error(errors.NewNotFoundError("知识库不存在"))
		return
	}
	ownWorkspace, err := kbSettingsAccess(c, kb)
	if err != nil {
		_ = c.Error(err)
		return
	}

	questionModelID, err := h.resolveQuestionGenerationModel(
		ctx, req.QuestionGeneration.ModelID, kb.QuestionGenerationConfig,
	)
	if err != nil {
		_ = c.Error(err)
		return
	}

	// 检查Embedding模型是否可以修改
	if kb.EmbeddingModelID != "" && req.EmbeddingModelID != "" && kb.EmbeddingModelID != req.EmbeddingModelID {
		// 检查是否已有文件
		knowledgeList, err := h.knowledgeService.ListPagedKnowledgeByKnowledgeBaseID(ctx,
			kbIDStr, &types.Pagination{
				Page:     1,
				PageSize: 1,
			}, types.KnowledgeListFilter{})
		if err == nil && knowledgeList != nil && knowledgeList.Total > 0 {
			logger.Error(ctx, "Cannot change embedding model when files exist")
			_ = c.Error(errors.NewBadRequestError("知识库中已有文件，无法修改Embedding模型"))
			return
		}
	}

	// 从数据库获取模型详情并验证
	llmModel, err := h.modelService.GetModelByID(ctx, req.LLMModelID)
	if err != nil || llmModel == nil {
		logger.Error(ctx, "LLM model not found")
		_ = c.Error(errors.NewBadRequestError("LLM模型不存在"))
		return
	}

	// Embedding模型仅在需要时验证（RAG检索启用时）
	if req.EmbeddingModelID != "" {
		embeddingModel, err := h.modelService.GetModelByID(ctx, req.EmbeddingModelID)
		if err != nil || embeddingModel == nil {
			logger.Error(ctx, "Embedding model not found")
			_ = c.Error(errors.NewBadRequestError("Embedding模型不存在"))
			return
		}
	}

	// 更新知识库的模型ID
	kb.SummaryModelID = req.LLMModelID
	if req.EmbeddingModelID != "" {
		kb.EmbeddingModelID = req.EmbeddingModelID
	}

	// Merge only supplied fields: model-only requests must preserve KB settings.
	if req.Multimodal.Enabled != nil {
		kb.VLMConfig.Enabled = *req.Multimodal.Enabled
	}
	if req.VLMConfig != nil {
		if req.VLMConfig.BaseURL != nil && strings.TrimSpace(*req.VLMConfig.BaseURL) != "" &&
			strings.TrimSpace(*req.VLMConfig.BaseURL) != strings.TrimSpace(kb.VLMConfig.BaseURL) {
			_ = c.Error(errors.NewBadRequestError("vlm_config.base_url 不支持直接写入，请通过 model_id 绑定受管视觉模型"))
			return
		}
		if req.VLMConfig.Enabled != nil {
			kb.VLMConfig.Enabled = *req.VLMConfig.Enabled
		}
		if req.VLMConfig.ModelID != nil {
			kb.VLMConfig.ModelID = strings.TrimSpace(*req.VLMConfig.ModelID)
		}
		if req.VLMConfig.DescriptionLanguage != nil {
			kb.VLMConfig.DescriptionLanguage = strings.TrimSpace(*req.VLMConfig.DescriptionLanguage)
		}
		if req.VLMConfig.CustomInstructions != nil {
			kb.VLMConfig.CustomInstructions = strings.TrimSpace(*req.VLMConfig.CustomInstructions)
		}

		if req.VLMConfig.ModelID != nil && kb.VLMConfig.Enabled && kb.VLMConfig.ModelID != "" {
			model, err := h.modelService.GetModelByID(ctx, kb.VLMConfig.ModelID)
			if err != nil || model == nil {
				logger.Warn(ctx, "VLM model not found")
				_ = c.Error(errors.NewBadRequestError("vlm_config.model_id: 视觉模型不存在"))
				return
			}
			if !model.SupportsType(types.ModelTypeVLLM) {
				_ = c.Error(errors.NewBadRequestError(
					fmt.Sprintf("vlm_config.model_id: 模型类型为 %s，需要 %s", model.Type, types.ModelTypeVLLM)))
				return
			}
			kb.VLMConfig.ModelName = ""
			kb.VLMConfig.BaseURL = ""
			kb.VLMConfig.APIKey = ""
			kb.VLMConfig.InterfaceType = ""
		}
	}
	if (req.Multimodal.Enabled != nil && !*req.Multimodal.Enabled) ||
		(req.VLMConfig != nil && req.VLMConfig.Enabled != nil && !*req.VLMConfig.Enabled) {
		// Legacy bindings also enable VLM, independently of Enabled.
		kb.VLMConfig.Enabled = false
		kb.VLMConfig.ModelID = ""
		kb.VLMConfig.ModelName = ""
		kb.VLMConfig.BaseURL = ""
		kb.VLMConfig.APIKey = ""
		kb.VLMConfig.InterfaceType = ""
	}
	if kb.VLMConfig.Enabled && kb.VLMConfig.ModelID == "" &&
		(kb.VLMConfig.ModelName == "" || kb.VLMConfig.BaseURL == "") {
		_ = c.Error(errors.NewBadRequestError("启用视觉多模态时必须指定有效的 vlm_config.model_id"))
		return
	}
	if req.ASRConfig != nil {
		if req.ASRConfig.Enabled != nil {
			kb.ASRConfig.Enabled = *req.ASRConfig.Enabled
		}
		if req.ASRConfig.ModelID != nil {
			kb.ASRConfig.ModelID = *req.ASRConfig.ModelID
		}
		if req.ASRConfig.Language != nil {
			kb.ASRConfig.Language = *req.ASRConfig.Language
		}

		if req.ASRConfig.ModelID != nil && kb.ASRConfig.Enabled && kb.ASRConfig.ModelID != "" {
			model, err := h.modelService.GetModelByID(ctx, kb.ASRConfig.ModelID)
			if err != nil || model == nil {
				logger.Warn(ctx, "ASR model not found")
				kb.ASRConfig.Enabled = false
				kb.ASRConfig.ModelID = ""
			}
		}
		if req.ASRConfig.Enabled != nil && !*req.ASRConfig.Enabled {
			kb.ASRConfig.ModelID = ""
		}
	}
	if req.DocumentSplitting.ChunkSize != nil {
		kb.ChunkingConfig.ChunkSize = *req.DocumentSplitting.ChunkSize
	}
	if req.DocumentSplitting.ChunkOverlap != nil {
		kb.ChunkingConfig.ChunkOverlap = *req.DocumentSplitting.ChunkOverlap
	}
	if req.DocumentSplitting.Separators != nil {
		kb.ChunkingConfig.Separators = *req.DocumentSplitting.Separators
	}
	if req.DocumentSplitting.ParserEngineRules != nil {
		kb.ChunkingConfig.ParserEngineRules = *req.DocumentSplitting.ParserEngineRules
	}
	if req.DocumentSplitting.EnableParentChild != nil {
		kb.ChunkingConfig.EnableParentChild = *req.DocumentSplitting.EnableParentChild
	}
	if req.DocumentSplitting.ParentChunkSize != nil {
		kb.ChunkingConfig.ParentChunkSize = *req.DocumentSplitting.ParentChunkSize
	}
	if req.DocumentSplitting.ChildChunkSize != nil {
		kb.ChunkingConfig.ChildChunkSize = *req.DocumentSplitting.ChildChunkSize
	}
	if req.DocumentSplitting.Strategy != nil {
		kb.ChunkingConfig.Strategy = *req.DocumentSplitting.Strategy
	}
	if req.DocumentSplitting.TokenLimit != nil {
		kb.ChunkingConfig.TokenLimit = *req.DocumentSplitting.TokenLimit
	}
	if req.DocumentSplitting.Languages != nil {
		kb.ChunkingConfig.Languages = *req.DocumentSplitting.Languages
	}
	if req.DocumentSplitting.TableMetadataInstructions != nil {
		kb.ChunkingConfig.TableMetadataInstructions = strings.TrimSpace(
			*req.DocumentSplitting.TableMetadataInstructions,
		)
	}

	// Storage backends resolve per workspace and belong to the owner's
	// infrastructure: another workspace can neither see the owner's backends
	// nor bind the KB to one of its own, so it may only leave them unchanged.
	if ownWorkspace {
		if err := h.applyKBStorageBinding(ctx, kb, kbIDStr, &req); err != nil {
			_ = c.Error(err)
			return
		}
	} else if kbStorageBindingChanged(kb, req.StorageBackendID, req.StorageProvider) {
		_ = c.Error(errors.NewForbiddenError("只有知识库所属空间可以修改存储配置"))
		return
	}

	if req.NodeExtract.Enabled != nil ||
		req.NodeExtract.Text != nil ||
		req.NodeExtract.Tags != nil ||
		req.NodeExtract.Nodes != nil ||
		req.NodeExtract.Relations != nil ||
		req.NodeExtract.CustomInstructions != nil {
		if kb.ExtractConfig == nil {
			kb.ExtractConfig = &types.ExtractConfig{}
		}
		if req.NodeExtract.Enabled != nil {
			kb.ExtractConfig.Enabled = *req.NodeExtract.Enabled
		}
		if req.NodeExtract.Text != nil {
			kb.ExtractConfig.Text = *req.NodeExtract.Text
		}
		if req.NodeExtract.Tags != nil {
			kb.ExtractConfig.Tags = *req.NodeExtract.Tags
		}
		if req.NodeExtract.Nodes != nil {
			kb.ExtractConfig.Nodes = make([]*types.GraphNode, len(*req.NodeExtract.Nodes))
			for i := range *req.NodeExtract.Nodes {
				kb.ExtractConfig.Nodes[i] = &(*req.NodeExtract.Nodes)[i]
			}
		}
		if req.NodeExtract.Relations != nil {
			kb.ExtractConfig.Relations = make([]*types.GraphRelation, len(*req.NodeExtract.Relations))
			for i := range *req.NodeExtract.Relations {
				kb.ExtractConfig.Relations[i] = &(*req.NodeExtract.Relations)[i]
			}
		}
		if req.NodeExtract.CustomInstructions != nil {
			kb.ExtractConfig.CustomInstructions = strings.TrimSpace(*req.NodeExtract.CustomInstructions)
		}
		if err := validateExtractConfig(kb.ExtractConfig); err != nil {
			logger.Error(ctx, "Invalid extract configuration", err)
			_ = c.Error(err)
			return
		}
	}
	if req.QuestionGeneration.Enabled != nil ||
		req.QuestionGeneration.QuestionCount != nil ||
		req.QuestionGeneration.CustomInstructions != nil ||
		req.QuestionGeneration.ModelID != nil {
		firstEnable := kb.QuestionGenerationConfig == nil || !kb.QuestionGenerationConfig.Enabled
		if kb.QuestionGenerationConfig == nil {
			kb.QuestionGenerationConfig = &types.QuestionGenerationConfig{}
		}
		if req.QuestionGeneration.Enabled != nil {
			kb.QuestionGenerationConfig.Enabled = *req.QuestionGeneration.Enabled
		}
		if req.QuestionGeneration.QuestionCount != nil {
			kb.QuestionGenerationConfig.QuestionCount = *req.QuestionGeneration.QuestionCount
		}
		if req.QuestionGeneration.CustomInstructions != nil {
			kb.QuestionGenerationConfig.CustomInstructions = strings.TrimSpace(
				*req.QuestionGeneration.CustomInstructions,
			)
		}
		kb.QuestionGenerationConfig.ModelID = questionModelID
		// Default only a first enable without an explicit count.
		if firstEnable && req.QuestionGeneration.Enabled != nil && *req.QuestionGeneration.Enabled &&
			req.QuestionGeneration.QuestionCount == nil && kb.QuestionGenerationConfig.QuestionCount <= 0 {
			kb.QuestionGenerationConfig.QuestionCount = 3
		}
		if req.QuestionGeneration.QuestionCount != nil && kb.QuestionGenerationConfig.QuestionCount > 10 {
			kb.QuestionGenerationConfig.QuestionCount = 10
		}
	}
	types.NormalizeKnowledgeBasePromptInstructions(kb)
	if err := validateKnowledgeBasePromptInstructions(kb); err != nil {
		_ = c.Error(err)
		return
	}

	// 保存更新后的知识库
	if err := h.kbService.SaveInitializedKnowledgeBaseConfig(ctx, kb); err != nil {
		logger.Error(ctx, "Failed to update knowledge base", err)
		_ = c.Error(errors.NewInternalServerError("更新知识库失败"))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "配置更新成功",
	})
}

// kbSettingsAccess reports whether the caller's workspace owns kb. Another
// workspace may change KB settings only through an admin share: the frontend
// offers KB settings to share admins alone, and OrgRoleEditor edits content,
// not settings. KBAccessWrite on the route also admits share editors, so the
// grant's effective share permission is checked here.
func kbSettingsAccess(c *gin.Context, kb *types.KnowledgeBase) (bool, error) {
	if kb.TenantID == types.CallerFromContext(c.Request.Context()).TenantID {
		return true, nil
	}
	grant, ok := middleware.KBAccessFromContext(c)
	if !ok || grant.KnowledgeBase == nil || grant.KnowledgeBase.ID != kb.ID ||
		!grant.Permission.HasPermission(types.OrgRoleAdmin) {
		return false, errors.NewForbiddenError("修改共享知识库的设置需要管理员共享权限")
	}
	return false, nil
}

// kbStorageBindingChanged reports whether a config request would rebind the
// KB's storage. A backend ID that matches the current one leaves the binding
// alone (its provider is only a projection); without one, an empty current
// provider means the default, which clients echo back as "local".
func kbStorageBindingChanged(kb *types.KnowledgeBase, backendID, provider string) bool {
	backendID = strings.TrimSpace(backendID)
	current := ""
	if kb.StorageBackendID != nil {
		current = *kb.StorageBackendID
	}
	if backendID != "" {
		return backendID != current
	}
	provider = strings.ToLower(strings.TrimSpace(provider))
	currentProvider := kb.GetStorageProvider()
	if currentProvider == "" {
		currentProvider = "local"
	}
	return provider != "" && provider != currentProvider
}

// applyKBStorageBinding binds the owner's storage instance to the KB. The
// caller's workspace must own the KB: backends resolve against TenantInfo.
func (h *InitializationHandler) applyKBStorageBinding(
	ctx context.Context, kb *types.KnowledgeBase, kbID string, req *KBModelConfigRequest,
) error {
	// Model-only clients omit storage fields. Keep the stored binding and
	// provider projection instead of implicitly switching them to local.
	if strings.TrimSpace(req.StorageBackendID) == "" && strings.TrimSpace(req.StorageProvider) == "" {
		return nil
	}
	// Bind the concrete storage instance. Provider remains a compatibility
	// projection for older clients and historical rows.
	if strings.TrimSpace(req.StorageBackendID) != "" {
		tenant, _ := types.TenantInfoFromContext(ctx)
		backend, resolveErr := h.storageResolver.ResolveBackend(ctx, tenant, req.StorageBackendID, "")
		if resolveErr != nil || backend == nil {
			return errors.NewBadRequestError("Storage backend is unavailable")
		}
		oldID := ""
		if kb.StorageBackendID != nil {
			oldID = *kb.StorageBackendID
		}
		if oldID != "" && oldID != backend.ID {
			knowledgeList, listErr := h.knowledgeService.ListPagedKnowledgeByKnowledgeBaseID(ctx,
				kbID, &types.Pagination{Page: 1, PageSize: 1}, types.KnowledgeListFilter{})
			if listErr == nil && knowledgeList != nil && knowledgeList.Total > 0 {
				return errors.NewBadRequestError(
					"Storage backend cannot be changed while the knowledge base contains files; migrate storage first")
			}
		}
		kb.StorageBackendID = &backend.ID
		req.StorageProvider = backend.Provider
	}
	// Legacy provider projection.
	provider := strings.ToLower(strings.TrimSpace(req.StorageProvider))
	if provider == "" {
		provider = "local"
	}
	if !isStorageProviderAllowed(provider) {
		return errors.NewBadRequestError("Storage provider is not allowed by STORAGE_ALLOW_LIST")
	}
	oldProvider := kb.GetStorageProvider()
	if oldProvider == "" {
		oldProvider = "local"
	}
	if oldProvider != provider {
		knowledgeList, err := h.knowledgeService.ListPagedKnowledgeByKnowledgeBaseID(ctx,
			kbID, &types.Pagination{Page: 1, PageSize: 1}, types.KnowledgeListFilter{})
		if err == nil && knowledgeList != nil && knowledgeList.Total > 0 {
			logger.Warn(ctx, "Storage engine changed with existing files, old files may become inaccessible")
		}
	}
	kb.SetStorageProvider(provider)
	return nil
}

// InitializeByKB godoc
// @Summary      初始化知识库配置
// @Description  根据知识库ID执行完整配置更新
// @Tags         初始化
// @Accept       json
// @Produce      json
// @Param        kb_id    path      string                         true  "知识库ID"
// @Param        request  body      handler.InitializationRequest  true  "初始化请求"
// @Success      200      {object}  map[string]interface{}         "初始化成功"
// @Failure      400      {object}  errors.AppError                "请求参数错误"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /initialization/initialize/{kb_id} [post]
func (h *InitializationHandler) InitializeByKB(c *gin.Context) {
	ctx := c.Request.Context()
	kbIDStr := utils.SanitizeForLog(c.Param("kb_id"))

	req, err := h.bindInitializationRequest(ctx, c)
	if err != nil {
		_ = c.Error(err)
		return
	}

	logger.Infof(
		ctx,
		"Starting knowledge base configuration update, kb_id: %s",
		kbIDStr,
	)

	kb, err := h.getKnowledgeBaseForInitialization(ctx, kbIDStr)
	if err != nil {
		_ = c.Error(err)
		return
	}

	if err := h.validateInitializationConfigs(ctx, req); err != nil {
		_ = c.Error(err)
		return
	}

	processedModels, err := h.processInitializationModels(ctx, kb, kbIDStr, req)
	if err != nil {
		_ = c.Error(err)
		return
	}

	h.applyKnowledgeBaseInitialization(kb, req, processedModels)

	if err := h.kbService.SaveInitializedKnowledgeBaseConfig(ctx, kb); err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{"kb_id": kbIDStr})
		_ = c.Error(errors.NewInternalServerError("更新知识库配置失败"))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "知识库配置更新成功",
		"data": gin.H{
			// Through the response DTO, like every other body carrying a
			// model: types.Model marshals api_key in plaintext, and now that
			// the reuse path keeps the stored credential instead of
			// overwriting it, echoing the row would hand back a key the
			// caller never submitted. KnowledgeBase redacts itself.
			"models":         dto.NewModelResponses(ctx, processedModels),
			"knowledge_base": kb,
		},
	})
}

func (h *InitializationHandler) bindInitializationRequest(
	ctx context.Context, c *gin.Context,
) (*InitializationRequest, error) {
	var req InitializationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Error(ctx, "Failed to parse initialization request", err)
		return nil, errors.NewBadRequestError(err.Error())
	}
	return &req, nil
}

func (h *InitializationHandler) getKnowledgeBaseForInitialization(
	ctx context.Context, kbIDStr string,
) (*types.KnowledgeBase, error) {
	kb, err := h.kbService.GetKnowledgeBaseByID(ctx, kbIDStr)
	if err != nil {
		if stderrors.Is(err, types.ErrKnowledgeBaseNotFound) {
			return nil, errors.NewNotFoundError("知识库不存在")
		}
		logger.ErrorWithFields(ctx, err, map[string]interface{}{"kb_id": utils.SanitizeForLog(kbIDStr)})
		return nil, errors.NewInternalServerError("获取知识库信息失败: " + err.Error())
	}
	if kb == nil {
		logger.Error(ctx, "Knowledge base not found")
		return nil, errors.NewNotFoundError("知识库不存在")
	}
	// Initialization rewrites the models the KB points at, and those rows
	// belong to the KB's workspace. A shared-KB editor passes the route's
	// KBAccessWrite guard (which moves execution into that workspace), so
	// without this check it could repoint the owner's models at its own
	// endpoint and key.
	if kb.TenantID != types.CallerFromContext(ctx).TenantID {
		return nil, errors.NewForbiddenError("只有知识库所属空间可以初始化知识库")
	}
	return kb, nil
}

// canUpdateTenantModels mirrors the PUT /models/:id guard: rewriting a stored
// model changes every KB and agent that uses it, so initializing a KB must not
// let a KB creator do what the model settings page reserves for admins.
func (h *InitializationHandler) canUpdateTenantModels(ctx context.Context) bool {
	if scope, ok := types.TenantAPIKeyScopeFromContext(ctx); ok {
		return scope.FullAccess || scope.HasCapability(types.APIKeyCapabilityManageModels)
	}
	if types.CallerFromContext(ctx).Role.HasPermission(types.TenantRoleAdmin) || types.IsSystemAdminFromContext(ctx) {
		return true
	}
	// Same rollout switch as the route guards: role checks only log while
	// RBAC enforcement is off.
	return h.config == nil || !h.config.Tenant.IsRBACEnforced()
}

func (h *InitializationHandler) validateInitializationConfigs(ctx context.Context, req *InitializationRequest) error {
	// SSRF validation for all user-supplied BaseURLs
	urlsToCheck := []struct {
		label string
		url   string
	}{
		{"LLM BaseURL", req.LLM.BaseURL},
		{"Embedding BaseURL", req.Embedding.BaseURL},
		{"Rerank BaseURL", req.Rerank.BaseURL},
	}
	if req.Multimodal.VLM != nil {
		urlsToCheck = append(urlsToCheck, struct {
			label string
			url   string
		}{"VLM BaseURL", req.Multimodal.VLM.BaseURL})
	}
	for _, u := range urlsToCheck {
		if u.url != "" {
			if err := utils.ValidateURLForSSRF(u.url); err != nil {
				logger.Warnf(ctx, "SSRF validation failed for %s: %v", u.label, err)
				return errors.NewBadRequestError(utils.FormatSSRFError(u.label, u.url, err))
			}
		}
	}

	if err := h.validateMultimodalConfig(ctx, req); err != nil {
		return err
	}
	if err := validateRerankConfig(ctx, req); err != nil {
		return err
	}
	return validateNodeExtractConfig(ctx, req)
}

func (h *InitializationHandler) validateMultimodalConfig(ctx context.Context, req *InitializationRequest) error {
	if !req.Multimodal.Enabled {
		return nil
	}

	storageType := strings.ToLower(req.Multimodal.StorageType)
	if req.Multimodal.VLM == nil {
		logger.Error(ctx, "Multimodal enabled but missing VLM configuration")
		return errors.NewBadRequestError("启用多模态时需要配置VLM信息")
	}
	if req.Multimodal.VLM.InterfaceType == "ollama" {
		req.Multimodal.VLM.BaseURL = os.Getenv("OLLAMA_BASE_URL") + "/v1"
	}
	if req.Multimodal.VLM.ModelName == "" || req.Multimodal.VLM.BaseURL == "" {
		logger.Error(ctx, "VLM configuration incomplete")
		return errors.NewBadRequestError("VLM配置不完整")
	}

	switch storageType {
	case "cos":
		if req.Multimodal.COS == nil || req.Multimodal.COS.SecretID == "" || req.Multimodal.COS.SecretKey == "" ||
			req.Multimodal.COS.Region == "" || req.Multimodal.COS.BucketName == "" ||
			req.Multimodal.COS.AppID == "" {
			logger.Error(ctx, "COS configuration incomplete")
			return errors.NewBadRequestError("COS配置不完整")
		}
	case "minio":
		if req.Multimodal.Minio == nil || req.Multimodal.Minio.BucketName == "" ||
			os.Getenv("MINIO_ACCESS_KEY_ID") == "" || os.Getenv("MINIO_SECRET_ACCESS_KEY") == "" {
			logger.Error(ctx, "MinIO configuration incomplete")
			return errors.NewBadRequestError("MinIO配置不完整")
		}
	}
	return nil
}

func validateRerankConfig(ctx context.Context, req *InitializationRequest) error {
	if !req.Rerank.Enabled {
		return nil
	}
	if req.Rerank.ModelName == "" || req.Rerank.BaseURL == "" {
		logger.Error(ctx, "Rerank configuration incomplete")
		return errors.NewBadRequestError("Rerank配置不完整")
	}
	return nil
}

func validateNodeExtractConfig(ctx context.Context, req *InitializationRequest) error {
	if !req.NodeExtract.Enabled {
		return nil
	}
	if strings.ToLower(os.Getenv("NEO4J_ENABLE")) != "true" {
		logger.Error(ctx, "Node Extractor configuration incomplete")
		return errors.NewBadRequestError("请正确配置环境变量NEO4J_ENABLE")
	}
	if req.NodeExtract.Text == "" || len(req.NodeExtract.Tags) == 0 {
		logger.Error(ctx, "Node Extractor configuration incomplete")
		return errors.NewBadRequestError("Node Extractor配置不完整")
	}
	if len(req.NodeExtract.Nodes) == 0 || len(req.NodeExtract.Relations) == 0 {
		logger.Error(ctx, "Node Extractor configuration incomplete")
		return errors.NewBadRequestError("请先提取实体和关系")
	}
	return nil
}

type modelDescriptor struct {
	modelType     types.ModelType
	name          string
	source        types.ModelSource
	description   string
	baseURL       string
	apiKey        string
	dimension     int
	interfaceType string
}

func buildModelDescriptors(req *InitializationRequest) []modelDescriptor {
	descriptors := []modelDescriptor{
		{
			modelType:   types.ModelTypeKnowledgeQA,
			name:        utils.SanitizeForLog(req.LLM.ModelName),
			source:      types.ModelSource(req.LLM.Source),
			description: "LLM Model for Knowledge QA",
			baseURL:     utils.SanitizeForLog(req.LLM.BaseURL),
			apiKey:      req.LLM.APIKey,
		},
		{
			modelType:   types.ModelTypeEmbedding,
			name:        utils.SanitizeForLog(req.Embedding.ModelName),
			source:      types.ModelSource(req.Embedding.Source),
			description: "Embedding Model",
			baseURL:     utils.SanitizeForLog(req.Embedding.BaseURL),
			apiKey:      req.Embedding.APIKey,
			dimension:   req.Embedding.Dimension,
		},
	}

	if req.Rerank.Enabled {
		descriptors = append(descriptors, modelDescriptor{
			modelType:   types.ModelTypeRerank,
			name:        utils.SanitizeForLog(req.Rerank.ModelName),
			source:      types.ModelSourceRemote,
			description: "Rerank Model",
			baseURL:     utils.SanitizeForLog(req.Rerank.BaseURL),
			apiKey:      req.Rerank.APIKey,
		})
	}

	if req.Multimodal.Enabled && req.Multimodal.VLM != nil {
		descriptors = append(descriptors, modelDescriptor{
			modelType:     types.ModelTypeVLLM,
			name:          utils.SanitizeForLog(req.Multimodal.VLM.ModelName),
			source:        types.ModelSourceRemote,
			description:   "VLM Model",
			baseURL:       utils.SanitizeForLog(req.Multimodal.VLM.BaseURL),
			apiKey:        req.Multimodal.VLM.APIKey,
			interfaceType: req.Multimodal.VLM.InterfaceType,
		})
	}

	return descriptors
}

func (h *InitializationHandler) processInitializationModels(
	ctx context.Context,
	kb *types.KnowledgeBase,
	kbIDStr string,
	req *InitializationRequest,
) ([]*types.Model, error) {
	descriptors := buildModelDescriptors(req)
	var processedModels []*types.Model

	for _, descriptor := range descriptors {
		model := descriptor.toModel()
		// Stamp the KB's tenant before insert: toModel() carries no tenant and
		// stored-model lookups filter on (tenant_id = ? OR is_builtin), so an
		// unstamped row lands at tenant_id = 0 where no tenant can read it back
		// (issue #3333).
		model.TenantID = kb.TenantID
		existingModelID := h.findExistingModelID(kb, descriptor.modelType)

		var existingModel *types.Model
		if existingModelID != "" {
			var err error
			existingModel, err = h.modelService.GetModelByID(ctx, existingModelID)
			if err != nil {
				logger.Warnf(ctx, "Failed to get existing model %s: %v, will create new one", existingModelID, err)
				existingModel = nil
			}
		}

		if existingModel != nil {
			if !h.canUpdateTenantModels(ctx) {
				return nil, errors.NewForbiddenError("修改已有模型配置需要空间管理员权限")
			}
			existingModel.Name = model.Name
			existingModel.Source = model.Source
			existingModel.Description = model.Description
			descriptor.applyToStoredParameters(&existingModel.Parameters)
			existingModel.UpdatedAt = time.Now()

			if err := h.modelService.UpdateModel(ctx, existingModel); err != nil {
				logger.ErrorWithFields(ctx, err, map[string]interface{}{
					"model_id": model.ID,
					"kb_id":    kbIDStr,
				})
				return nil, errors.NewInternalServerError("更新模型失败: " + err.Error())
			}
			processedModels = append(processedModels, existingModel)
			continue
		}

		if err := h.modelService.CreateModel(ctx, model); err != nil {
			logger.ErrorWithFields(ctx, err, map[string]interface{}{
				"model_id": model.ID,
				"kb_id":    kbIDStr,
			})
			return nil, errors.NewInternalServerError("创建模型失败: " + err.Error())
		}
		processedModels = append(processedModels, model)
	}

	return processedModels, nil
}

// applyToStoredParameters merges the initialization payload into the
// parameters of a model row that already exists.
//
// The wizard collects four fields (endpoint, key, interface type, embedding
// dimension); everything else on the row — provider, extra_config, custom
// headers, spec, concurrency, context window — was configured in the model
// editor. Assigning toModel()'s parameters wholesale erased all of it, and
// blanked the stored API key whenever the payload carried none, so a KB that
// was merely re-initialized came back with a model nobody could call. Only
// the fields the payload actually carries are written; an empty one means
// "not submitted", not "clear it".
func (descriptor modelDescriptor) applyToStoredParameters(params *types.ModelParameters) {
	if descriptor.baseURL != "" {
		params.BaseURL = descriptor.baseURL
	}
	if descriptor.apiKey != "" {
		params.APIKey = descriptor.apiKey
	}
	if descriptor.interfaceType != "" {
		params.InterfaceType = descriptor.interfaceType
	}
	if descriptor.modelType == types.ModelTypeEmbedding && descriptor.dimension > 0 {
		params.EmbeddingParameters.Dimension = descriptor.dimension
	}
}

func (descriptor modelDescriptor) toModel() *types.Model {
	model := &types.Model{
		Type:        descriptor.modelType,
		Name:        descriptor.name,
		Source:      descriptor.source,
		Description: descriptor.description,
		Parameters: types.ModelParameters{
			BaseURL:       descriptor.baseURL,
			APIKey:        descriptor.apiKey,
			InterfaceType: descriptor.interfaceType,
		},
		IsDefault: false,
		Status:    types.ModelStatusActive,
	}

	if descriptor.modelType == types.ModelTypeEmbedding {
		model.Parameters.EmbeddingParameters = types.EmbeddingParameters{
			Dimension: descriptor.dimension,
		}
	}

	return model
}

func (h *InitializationHandler) findExistingModelID(kb *types.KnowledgeBase, modelType types.ModelType) string {
	switch modelType {
	case types.ModelTypeEmbedding:
		return kb.EmbeddingModelID
	case types.ModelTypeKnowledgeQA:
		return kb.SummaryModelID
	case types.ModelTypeVLLM:
		return kb.VLMConfig.ModelID
	default:
		return ""
	}
}

func (h *InitializationHandler) applyKnowledgeBaseInitialization(
	kb *types.KnowledgeBase,
	req *InitializationRequest,
	processedModels []*types.Model,
) {
	embeddingModelID, llmModelID, vlmModelID := extractModelIDs(processedModels)

	kb.SummaryModelID = llmModelID
	kb.EmbeddingModelID = embeddingModelID

	kb.ChunkingConfig = types.ChunkingConfig{
		ChunkSize:    req.DocumentSplitting.ChunkSize,
		ChunkOverlap: req.DocumentSplitting.ChunkOverlap,
		Separators:   req.DocumentSplitting.Separators,
	}

	legacyStorage := &kb.StorageConfig //nolint:staticcheck // SA1019: historical cos_config column
	if req.Multimodal.Enabled {
		kb.VLMConfig = types.VLMConfig{
			Enabled: req.Multimodal.Enabled,
			ModelID: vlmModelID,
		}
		switch req.Multimodal.StorageType {
		case "cos":
			if req.Multimodal.COS != nil {
				kb.SetStorageProvider("cos")
				// Legacy: also write to cos_config for backward compat with old code paths
				*legacyStorage = types.StorageConfig{ //nolint:staticcheck // SA1019: historical cos_config column
					Provider:   req.Multimodal.StorageType,
					BucketName: req.Multimodal.COS.BucketName,
					AppID:      req.Multimodal.COS.AppID,
					PathPrefix: req.Multimodal.COS.PathPrefix,
					SecretID:   req.Multimodal.COS.SecretID,
					SecretKey:  req.Multimodal.COS.SecretKey,
					Region:     req.Multimodal.COS.Region,
				}
			}
		case "minio":
			if req.Multimodal.Minio != nil {
				kb.SetStorageProvider("minio")
				// Store non-secret bucket/prefix metadata only; global MINIO_ACCESS_KEY_*
				// environment credentials must never be persisted into the database.
				*legacyStorage = types.StorageConfig{ //nolint:staticcheck // SA1019: historical cos_config column
					Provider:   req.Multimodal.StorageType,
					BucketName: req.Multimodal.Minio.BucketName,
					PathPrefix: req.Multimodal.Minio.PathPrefix,
				}
			}
		}
	} else {
		kb.VLMConfig = types.VLMConfig{}
		kb.SetStorageProvider("")
		*legacyStorage = types.StorageConfig{} //nolint:staticcheck // SA1019: historical cos_config column
	}

	if req.NodeExtract.Enabled {
		kb.ExtractConfig = &types.ExtractConfig{
			Text:      req.NodeExtract.Text,
			Tags:      req.NodeExtract.Tags,
			Nodes:     make([]*types.GraphNode, 0),
			Relations: make([]*types.GraphRelation, 0),
		}
		for _, rnode := range req.NodeExtract.Nodes {
			node := &types.GraphNode{
				Name:       rnode.Name,
				Attributes: rnode.Attributes,
			}
			kb.ExtractConfig.Nodes = append(kb.ExtractConfig.Nodes, node)
		}
		for _, relation := range req.NodeExtract.Relations {
			kb.ExtractConfig.Relations = append(kb.ExtractConfig.Relations, &types.GraphRelation{
				Node1: relation.Node1,
				Node2: relation.Node2,
				Type:  relation.Type,
			})
		}
	}
}

func extractModelIDs(processedModels []*types.Model) (embeddingModelID, llmModelID, vlmModelID string) {
	for _, model := range processedModels {
		if model == nil {
			continue
		}
		switch model.Type {
		case types.ModelTypeEmbedding:
			embeddingModelID = model.ID
		case types.ModelTypeKnowledgeQA:
			llmModelID = model.ID
		case types.ModelTypeVLLM:
			vlmModelID = model.ID
		}
	}
	return
}

// GetCurrentConfigByKB godoc
// @Summary      获取知识库配置
// @Description  根据知识库ID获取当前配置信息
// @Tags         初始化
// @Accept       json
// @Produce      json
// @Param        kb_id  path      string  true  "知识库ID"
// @Success      200    {object}  map[string]interface{}  "配置信息"
// @Failure      404    {object}  errors.AppError         "知识库不存在"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /initialization/config/{kb_id} [get]
func (h *InitializationHandler) GetCurrentConfigByKB(c *gin.Context) {
	ctx := c.Request.Context()
	kbIDStr := utils.SanitizeForLog(c.Param("kb_id"))

	logger.Info(ctx, "Getting configuration for knowledge base")

	// 获取指定知识库信息
	kb, err := h.kbService.GetKnowledgeBaseByID(ctx, kbIDStr)
	if err != nil {
		if stderrors.Is(err, types.ErrKnowledgeBaseNotFound) {
			_ = c.Error(errors.NewNotFoundError("知识库不存在"))
			return
		}
		logger.Error(ctx, "Failed to get knowledge base", err)
		_ = c.Error(errors.NewInternalServerError("获取知识库信息失败: " + err.Error()))
		return
	}

	if kb == nil {
		logger.Error(ctx, "Knowledge base not found")
		_ = c.Error(errors.NewNotFoundError("知识库不存在"))
		return
	}

	// 根据知识库的模型ID获取特定模型
	var models []*types.Model
	modelIDs := []string{
		kb.EmbeddingModelID,
		kb.SummaryModelID,
		kb.VLMConfig.ModelID,
	}

	for _, modelID := range modelIDs {
		if modelID != "" {
			model, err := h.modelService.GetModelByID(ctx, modelID)
			if err != nil {
				logger.Warn(ctx, "Failed to get model", err)
				continue
			}
			if model != nil {
				models = append(models, model)
			}
		}
	}

	// 检查知识库是否有文件
	knowledgeList, err := h.knowledgeService.ListPagedKnowledgeByKnowledgeBaseID(ctx,
		kbIDStr, &types.Pagination{
			Page:     1,
			PageSize: 1,
		}, types.KnowledgeListFilter{})
	hasFiles := err == nil && knowledgeList != nil && knowledgeList.Total > 0

	// 构建配置响应
	config := h.buildConfigResponse(ctx, models, kb, hasFiles)

	logger.Info(ctx, "Knowledge base configuration retrieved successfully")
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    config,
	})
}

// buildConfigResponse 构建配置响应数据
func (h *InitializationHandler) buildConfigResponse(ctx context.Context, models []*types.Model,
	kb *types.KnowledgeBase, hasFiles bool,
) map[string]interface{} {
	config := map[string]interface{}{
		"has_files": hasFiles,
	}
	// Integration details describe the owning workspace's infrastructure. A
	// share receiver — even an admin of its own workspace — only learns
	// whether credentials are configured.
	ownWorkspace := kb != nil && kb.TenantID == types.CallerFromContext(ctx).TenantID
	includeIntegrationDetail := ownWorkspace && dto.CanViewIntegrationSecrets(ctx)

	// 按类型分组模型
	for _, model := range models {
		if model == nil {
			continue
		}
		// Hide sensitive information for builtin models and viewers.
		baseURL := model.Parameters.BaseURL
		if model.IsBuiltin || !includeIntegrationDetail {
			baseURL = ""
		}

		switch model.Type {
		case types.ModelTypeKnowledgeQA:
			config["llm"] = map[string]interface{}{
				"source":     string(model.Source),
				"model_name": model.Name,
				"base_url":   baseURL,
				"credentials": map[string]bool{
					"api_key": model.Parameters.APIKey != "" && !model.IsBuiltin,
				},
			}
		case types.ModelTypeEmbedding:
			config["embedding"] = map[string]interface{}{
				"source":     string(model.Source),
				"model_name": model.Name,
				"base_url":   baseURL,
				"dimension":  model.Parameters.EmbeddingParameters.Dimension,
				"credentials": map[string]bool{
					"api_key": model.Parameters.APIKey != "" && !model.IsBuiltin,
				},
			}
		case types.ModelTypeRerank:
			config["rerank"] = map[string]interface{}{
				"enabled":    true,
				"model_name": model.Name,
				"base_url":   baseURL,
				"credentials": map[string]bool{
					"api_key": model.Parameters.APIKey != "" && !model.IsBuiltin,
				},
			}
		case types.ModelTypeVLLM:
			if config["multimodal"] == nil {
				config["multimodal"] = map[string]interface{}{
					"enabled": true,
				}
			}
			multimodal := config["multimodal"].(map[string]interface{})
			multimodal["vlm"] = map[string]interface{}{
				"model_name":     model.Name,
				"base_url":       baseURL,
				"interface_type": model.Parameters.InterfaceType,
				"model_id":       model.ID,
				"credentials": map[string]bool{
					"api_key": model.Parameters.APIKey != "" && !model.IsBuiltin,
				},
			}
		}
	}

	// 判断多模态是否启用：有VLM模型ID或有存储配置（兼容新旧字段）
	var legacyStorage types.StorageConfig //nolint:staticcheck // SA1019: historical cos_config column
	if kb != nil {
		legacyStorage = kb.StorageConfig //nolint:staticcheck // SA1019: historical cos_config column
	}
	storageProvider := kb.GetStorageProvider()
	hasMultimodal := (kb.VLMConfig.IsEnabled() ||
		legacyStorage.SecretID != "" || legacyStorage.BucketName != "" ||
		(storageProvider != "" && storageProvider != "local"))
	if config["multimodal"] == nil {
		config["multimodal"] = map[string]interface{}{
			"enabled": hasMultimodal,
		}
	} else {
		config["multimodal"].(map[string]interface{})["enabled"] = hasMultimodal
	}
	if kb.VLMConfig.DescriptionLanguage != "" || kb.VLMConfig.CustomInstructions != "" {
		if config["multimodal"] == nil {
			config["multimodal"] = map[string]interface{}{
				"enabled": hasMultimodal,
			}
		}
		multimodal := config["multimodal"].(map[string]interface{})
		if kb.VLMConfig.DescriptionLanguage != "" {
			multimodal["description_language"] = kb.VLMConfig.DescriptionLanguage
		}
		if kb.VLMConfig.CustomInstructions != "" {
			multimodal["custom_instructions"] = kb.VLMConfig.CustomInstructions
		}
	}

	// 如果没有Rerank模型，设置rerank为disabled
	if config["rerank"] == nil {
		config["rerank"] = map[string]interface{}{
			"enabled":    false,
			"model_name": "",
			"base_url":   "",
			"credentials": map[string]bool{
				"api_key": false,
			},
		}
	}

	// 添加知识库的文档分割配置
	if kb != nil {
		ds := map[string]interface{}{
			"chunk_size":    kb.ChunkingConfig.ChunkSize,
			"chunk_overlap": kb.ChunkingConfig.ChunkOverlap,
			"separators":    kb.ChunkingConfig.Separators,
		}
		if kb.ChunkingConfig.Strategy != "" {
			ds["strategy"] = kb.ChunkingConfig.Strategy
		}
		if kb.ChunkingConfig.TokenLimit > 0 {
			ds["token_limit"] = kb.ChunkingConfig.TokenLimit
		}
		if len(kb.ChunkingConfig.Languages) > 0 {
			ds["languages"] = kb.ChunkingConfig.Languages
		}
		if kb.ChunkingConfig.TableMetadataInstructions != "" {
			ds["table_metadata_instructions"] = kb.ChunkingConfig.TableMetadataInstructions
		}
		config["document_splitting"] = ds

		// 添加多模态的存储配置信息（优先读新字段，兼容旧 cos_config）
		effectiveProvider := kb.GetStorageProvider()
		if legacyStorage.SecretID != "" || (effectiveProvider != "" && effectiveProvider != "local") {
			if config["multimodal"] == nil {
				config["multimodal"] = map[string]interface{}{
					"enabled": true,
				}
			}
			multimodal := config["multimodal"].(map[string]interface{})
			multimodal["storage_type"] = effectiveProvider
			switch effectiveProvider {
			case "cos":
				multimodal["cos"] = map[string]interface{}{
					"region":      legacyStorage.Region,
					"bucket_name": legacyStorage.BucketName,
					"app_id":      legacyStorage.AppID,
					"path_prefix": legacyStorage.PathPrefix,
					"credentials": map[string]bool{
						"secret_id":  legacyStorage.SecretID != "",
						"secret_key": legacyStorage.SecretKey != "",
					},
				}
			case "minio":
				multimodal["minio"] = map[string]interface{}{
					"bucket_name": legacyStorage.BucketName,
					"path_prefix": legacyStorage.PathPrefix,
				}
			}
			if !ownWorkspace {
				// Bucket locations are the owner's infrastructure too.
				for _, provider := range []string{"cos", "minio"} {
					if detail, ok := multimodal[provider].(map[string]interface{}); ok {
						for _, field := range []string{"region", "bucket_name", "app_id", "path_prefix"} {
							delete(detail, field)
						}
					}
				}
			}
		}
	}

	if kb.ExtractConfig != nil {
		nodeExtract := map[string]interface{}{
			"enabled":   kb.ExtractConfig.Enabled,
			"text":      kb.ExtractConfig.Text,
			"tags":      kb.ExtractConfig.Tags,
			"nodes":     kb.ExtractConfig.Nodes,
			"relations": kb.ExtractConfig.Relations,
		}
		if kb.ExtractConfig.CustomInstructions != "" {
			nodeExtract["custom_instructions"] = kb.ExtractConfig.CustomInstructions
		}
		config["node_extract"] = nodeExtract
	} else {
		config["node_extract"] = map[string]interface{}{
			"enabled": false,
		}
	}

	if kb.QuestionGenerationConfig != nil {
		config["question_generation"] = map[string]interface{}{
			"enabled":             kb.QuestionGenerationConfig.Enabled,
			"question_count":      kb.QuestionGenerationConfig.QuestionCount,
			"model_id":            kb.QuestionGenerationConfig.ModelID,
			"custom_instructions": kb.QuestionGenerationConfig.CustomInstructions,
		}
	} else {
		config["question_generation"] = map[string]interface{}{
			"enabled": false,
		}
	}

	return config
}
