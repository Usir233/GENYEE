package handler

import (
	"context"
	"encoding/json"
	goerrors "errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Usir233/GENYEE/internal/application/access"
	"github.com/Usir233/GENYEE/internal/application/service"
	"github.com/Usir233/GENYEE/internal/config"
	"github.com/Usir233/GENYEE/internal/errors"
	"github.com/Usir233/GENYEE/internal/filetransport"
	"github.com/Usir233/GENYEE/internal/logger"
	"github.com/Usir233/GENYEE/internal/middleware"
	"github.com/Usir233/GENYEE/internal/tracing/langfuse"
	"github.com/Usir233/GENYEE/internal/types"
	"github.com/Usir233/GENYEE/internal/types/interfaces"
	"github.com/Usir233/GENYEE/internal/utils"
	"github.com/gin-gonic/gin"
	"github.com/hibiken/asynq"
)

// KnowledgeHandler processes HTTP requests related to knowledge resources
type KnowledgeHandler struct {
	cfg               *config.Config
	kgService         interfaces.KnowledgeService
	kbService         interfaces.KnowledgeBaseService
	kbShareService    interfaces.KBShareService
	agentShareService interfaces.AgentShareService
	chunkService      interfaces.ChunkService
	systemSettingSvc  interfaces.SystemSettingService
	userSvc           interfaces.UserService
	asynqClient       interfaces.TaskEnqueuer
	backlog           backlogProbe
}

// backlogProbe tells a backlogged document (work still queued) from a stuck
// one; HousekeepingService implements it with the sweep's own probes.
type backlogProbe interface {
	QueuedWork(ctx context.Context, ids []string) (map[string]bool, error)
}

// stallHintAfter is how long an in-flight row may go without progress before
// it gets a stall verdict; keep in step with PROCESSING_STALL_THRESHOLD_MS in
// the frontend.
const stallHintAfter = 20 * time.Minute

// maxKnowledgeIDFilter caps how many source documents one gallery request may
// restrict itself to via the knowledge_ids query parameter.
const maxKnowledgeIDFilter = 50

// maxKnowledgeIDFilterBody is the same cap for the POST /images body variant:
// without URL length limits a generous guard against abuse is enough.
const maxKnowledgeIDFilterBody = 1000

// stallVerdicts probes ids (all quiet past stallHintAfter) and returns each
// one's StallState. Nil when the probe is unavailable or failed: an unknown
// row gets no verdict rather than being called stuck.
func (h *KnowledgeHandler) stallVerdicts(ctx context.Context, ids []string) map[string]string {
	if h.backlog == nil || len(ids) == 0 {
		return nil
	}
	queued, err := h.backlog.QueuedWork(ctx, ids)
	if err != nil {
		logger.Warnf(ctx, "backlog probe failed: %v", err)
		return nil
	}
	out := make(map[string]string, len(ids))
	for _, id := range ids {
		out[id] = types.StallStateStalled
		if queued[id] {
			out[id] = types.StallStateQueued
		}
	}
	return out
}

// NewKnowledgeHandler creates a new knowledge handler instance
func NewKnowledgeHandler(
	cfg *config.Config,
	kgService interfaces.KnowledgeService,
	kbService interfaces.KnowledgeBaseService,
	kbShareService interfaces.KBShareService,
	agentShareService interfaces.AgentShareService,
	chunkService interfaces.ChunkService,
	systemSettingSvc interfaces.SystemSettingService,
	userSvc interfaces.UserService,
	asynqClient interfaces.TaskEnqueuer,
	housekeeping *service.HousekeepingService,
) *KnowledgeHandler {
	var backlog backlogProbe
	if housekeeping != nil {
		backlog = housekeeping
	}
	return &KnowledgeHandler{
		backlog:           backlog,
		cfg:               cfg,
		kgService:         kgService,
		kbService:         kbService,
		kbShareService:    kbShareService,
		agentShareService: agentShareService,
		chunkService:      chunkService,
		systemSettingSvc:  systemSettingSvc,
		userSvc:           userSvc,
		asynqClient:       asynqClient,
	}
}

// requireKBOwnershipOrAdmin enforces the same "KB creator OR Admin+" matrix
// used by OwnedKBOrAdmin for routes whose KB id comes from the request body.
func (h *KnowledgeHandler) requireKBOwnershipOrAdmin(c *gin.Context, kbID string) error {
	evalErr := middleware.EvaluateOwnershipOrRole(
		c.Request.Context(),
		h.cfg,
		types.TenantRoleAdmin,
		func() (string, error) { return resolveKBCreatorByKBID(c, h.kbService, kbID) },
	)
	if evalErr == nil {
		return nil
	}
	if goerrors.Is(evalErr, middleware.ErrResourceNotFound) {
		return errors.NewNotFoundError("knowledge base not found")
	}
	if goerrors.Is(evalErr, middleware.ErrOwnershipForbidden) {
		return errors.NewForbiddenError("No permission to operate on this knowledge base")
	}
	logger.ErrorWithFields(c.Request.Context(), evalErr, map[string]interface{}{
		"kb_id": utils.SanitizeForLog(kbID),
	})
	return errors.NewInternalServerError("cannot verify knowledge base ownership")
}

// validateKnowledgeBaseAccess validates access permissions to a knowledge base
// using the ":id" URL path parameter. It delegates to validateKnowledgeBaseAccessWithKBID.
func (h *KnowledgeHandler) validateKnowledgeBaseAccess(
	c *gin.Context,
) (*types.KnowledgeBase, string, uint64, types.OrgMemberRole, error) {
	kbID := utils.SanitizeForLog(c.Param("id"))
	return h.validateKnowledgeBaseAccessWithKBID(c, kbID)
}

// validateKnowledgeBaseAccessWithKBID validates access to the given knowledge base ID (e.g. from query or body).
// Enforces per-API-key KB scope before tenant/share/agent resolution.
// Returns the knowledge base, kbID, effective tenant ID, permission, and error.
func (h *KnowledgeHandler) validateKnowledgeBaseAccessWithKBID(
	c *gin.Context,
	kbID string,
) (*types.KnowledgeBase, string, uint64, types.OrgMemberRole, error) {
	kbID = utils.SanitizeForLog(kbID)
	grant, err := resolveHandlerKBAccess(c, kbID, h.kbService, h.kbShareService, h.agentShareService)
	if err != nil {
		return nil, kbID, 0, "", err
	}
	return grant.KnowledgeBase, kbID, grant.EffectiveTenantID, grant.Permission, nil
}

func (h *KnowledgeHandler) validateKnowledgeBaseWriteAccessWithKBID(
	c *gin.Context,
	kbID string,
) (*types.KnowledgeBase, string, uint64, types.OrgMemberRole, error) {
	grant, err := resolveHandlerKBAccessFor(
		c,
		kbID,
		h.kbService,
		h.kbShareService,
		h.agentShareService,
		types.OrgRoleEditor,
	)
	if err != nil {
		return nil, kbID, 0, "", err
	}
	if err := access.RequireKBWrite(grant.Context(c.Request.Context()), grant.KnowledgeBase); err != nil {
		return nil, kbID, 0, "", kbAccessHTTPError(err)
	}
	return grant.KnowledgeBase, kbID, grant.EffectiveTenantID, grant.Permission, nil
}

// resolveKnowledgeAndValidateKBAccess resolves a document and checks its parent
// KB using the same policy as KB routes. The persisted document supplies the KB
// and owner reference, so unguarded callers do not need an additional KB lookup.
func (h *KnowledgeHandler) resolveKnowledgeAndValidateKBAccess(
	c *gin.Context,
	knowledgeID string,
	requiredPermission types.OrgMemberRole,
) (*types.Knowledge, context.Context, error) {
	ctx := c.Request.Context()
	request := middleware.KBAccessRequest(c)
	if request.Caller.TenantID == 0 {
		return nil, ctx, errors.NewUnauthorizedError("Unauthorized")
	}
	knowledge, err := h.kgService.GetKnowledgeByIDOnly(ctx, knowledgeID)
	if err != nil || knowledge == nil {
		return nil, ctx, errors.NewNotFoundError("Knowledge not found")
	}
	if err := requireTenantAPIKeyKnowledgeBase(ctx, knowledge.KnowledgeBaseID); err != nil {
		return nil, ctx, err
	}
	if grant, ok := resolvedKBAccess(c, knowledge.KnowledgeBaseID, requiredPermission); ok {
		if grant.EffectiveTenantID != knowledge.TenantID {
			return nil, ctx, errors.NewForbiddenError("Permission denied to access this knowledge")
		}
		return knowledge, grant.Context(ctx), nil
	}
	kb := &types.KnowledgeBase{ID: knowledge.KnowledgeBaseID, TenantID: knowledge.TenantID}
	grant, err := access.ResolveKB(ctx, request, kb, requiredPermission, h.kbShareService, h.agentShareService)
	if goerrors.Is(err, access.ErrForbidden) {
		return nil, ctx, errors.NewForbiddenError("Permission denied to access this knowledge")
	}
	if err != nil {
		return nil, ctx, kbAccessHTTPError(err)
	}
	return knowledge, grant.Context(ctx), nil
}

// handleDuplicateKnowledgeError handles cases where duplicate knowledge is detected
// Returns true if the error was a duplicate error and was handled, false otherwise
func (h *KnowledgeHandler) handleDuplicateKnowledgeError(c *gin.Context,
	err error, knowledge *types.Knowledge, duplicateType string,
) bool {
	var dupErr *types.DuplicateKnowledgeError
	if goerrors.As(err, &dupErr) {
		ctx := c.Request.Context()
		logger.Warnf(ctx, "Detected duplicate %s: %s", duplicateType, utils.SanitizeForLog(dupErr.Error()))
		_ = c.Error(errors.NewConflictError(dupErr.Error()).WithDetails(gin.H{
			"code": fmt.Sprintf("duplicate_%s", duplicateType),
			"data": knowledge,
		}))
		return true
	}
	return false
}

// enqueueKnowledgeListDelete enqueues an async batch-delete task for the
// given knowledge IDs and returns the asynq task ID.
func (h *KnowledgeHandler) enqueueKnowledgeListDelete(
	ctx context.Context, tenantID uint64, kbID string, ids []string,
) (string, error) {
	payload := types.KnowledgeListDeletePayload{
		KnowledgeBaseID: kbID,
		TenantID:        tenantID,
		KnowledgeIDs:    ids,
		Initiator:       types.TaskInitiatorFromContext(ctx),
	}
	langfuse.InjectTracing(ctx, &payload)
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal payload: %w", err)
	}
	task := asynq.NewTask(types.TypeKnowledgeListDelete, payloadBytes,
		asynq.Queue(types.QueueMaintenance), asynq.MaxRetry(3), asynq.Timeout(2*time.Hour))
	info, err := h.asynqClient.Enqueue(task)
	if err != nil {
		return "", fmt.Errorf("enqueue task: %w", err)
	}
	return info.ID, nil
}

// enqueueKnowledgeListReparse enqueues an async batch-reparse task for the
// given knowledge IDs and returns the asynq task ID.
func (h *KnowledgeHandler) enqueueKnowledgeListReparse(
	ctx context.Context, tenantID uint64, kbID string, ids []string, processConfig *types.KnowledgeProcessOverrides,
) (string, error) {
	payload := types.KnowledgeListReparsePayload{
		KnowledgeBaseID: kbID,
		TenantID:        tenantID,
		KnowledgeIDs:    ids,
		ProcessConfig:   processConfig,
		Initiator:       types.TaskInitiatorFromContext(ctx),
	}
	langfuse.InjectTracing(ctx, &payload)
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal payload: %w", err)
	}
	task := asynq.NewTask(types.TypeKnowledgeListReparse, payloadBytes,
		asynq.Queue(types.QueueMaintenance), asynq.MaxRetry(3), asynq.Timeout(time.Hour))
	info, err := h.asynqClient.Enqueue(task)
	if err != nil {
		return "", fmt.Errorf("enqueue task: %w", err)
	}
	return info.ID, nil
}

// CreateKnowledgeFromFile godoc
// @Summary      从文件创建知识
// @Description  上传文件并创建知识条目
// @Tags         知识管理
// @Accept       multipart/form-data
// @Produce      json
// @Param        id                path      string  true   "知识库ID"
// @Param        file              formData  file    true   "上传的文件"
// @Param        file_name         formData  string  false  "自定义文件名"
// @Param        metadata          formData  string  false  "元数据JSON"
// @Param        enable_multimodal formData  bool    false  "启用多模态处理"
// @Param        tag_ids       formData  string  false  "分类ID列表，逗号分隔"
// @Param        process_config    formData  string  false  "处理配置JSON（KnowledgeProcessOverrides）"
// @Success      200               {object}  handler.DataResponse{data=types.Knowledge}  "创建的知识"
// @Failure      400               {object}  errors.AppError         "请求参数错误"
// @Failure      409               {object}  errors.AppError         "文件重复"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases/{id}/knowledge/file [post]
func (h *KnowledgeHandler) CreateKnowledgeFromFile(c *gin.Context) {
	ctx := c.Request.Context()
	logger.Info(ctx, "Start creating knowledge from file")

	// Validate access to the knowledge base (only owner or admin/editor can create)
	_, kbID, effectiveTenantID, permission, err := h.validateKnowledgeBaseAccess(c)
	if err != nil {
		_ = c.Error(err)
		return
	}
	ctx = types.WithExecutionTenant(c.Request.Context(), effectiveTenantID)

	// Check write permission
	if permission != types.OrgRoleAdmin && permission != types.OrgRoleEditor {
		_ = c.Error(errors.NewForbiddenError("No permission to create knowledge"))
		return
	}

	// Validate file size — read MAX_FILE_SIZE_MB env (50MB default).
	// Deliberately not a runtime system_setting; see filesize.go for the
	// rationale (nginx / docreader / browser bundle all cache this at
	// container startup, so a UI knob would silently mismatch).
	maxSizeMB := utils.GetMaxFileSizeMB()
	maxSize := maxSizeMB * 1024 * 1024
	// Capped before the multipart parse, not after: FormFile buffers the whole
	// body first, so the size check below only ever sees an upload we already
	// accepted. nginx location /api/ still enforces MAX_FILE_SIZE; this is the
	// same cap for requests that reach the app without that proxy.
	limitUploadBody(c, maxSize)

	// Get the uploaded file
	file, err := c.FormFile("file")
	if err != nil {
		if isRequestBodyTooLarge(err) {
			logger.Error(ctx, "File size too large")
			_ = c.Error(errors.NewBadRequestError(fmt.Sprintf("文件大小不能超过%dMB", maxSizeMB)))
			return
		}
		logger.Error(ctx, "File upload failed", err)
		_ = c.Error(errors.NewBadRequestError("File upload failed").WithDetails(err.Error()))
		return
	}
	if file.Size > maxSize {
		logger.Error(ctx, "File size too large")
		_ = c.Error(errors.NewBadRequestError(fmt.Sprintf("文件大小不能超过%dMB", maxSizeMB)))
		return
	}

	// Get custom filename if provided (for folder uploads with path)
	customFileName := c.PostForm("file_name")
	customFileName = utils.SanitizeForLog(customFileName)
	displayFileName := file.Filename
	displayFileName = utils.SanitizeForLog(displayFileName)
	if customFileName != "" {
		displayFileName = customFileName
		logger.Infof(ctx, "Using custom filename: %s (original: %s)", customFileName, displayFileName)
	}

	logger.Infof(ctx, "File upload successful, filename: %s, size: %.2f KB", displayFileName, float64(file.Size)/1024)
	logger.Infof(ctx, "Creating knowledge, knowledge base ID: %s, filename: %s", kbID, displayFileName)

	// Parse metadata if provided
	var metadata map[string]string
	metadataStr := c.PostForm("metadata")
	if metadataStr != "" {
		if err := json.Unmarshal([]byte(metadataStr), &metadata); err != nil {
			logger.Error(ctx, "Failed to parse metadata", err)
			_ = c.Error(errors.NewBadRequestError("Invalid metadata format").WithDetails(err.Error()))
			return
		}
		logger.Infof(ctx, "Received file metadata: %s", utils.SanitizeForLog(fmt.Sprintf("%v", metadata)))
	}

	enableMultimodalForm := c.PostForm("enable_multimodal")
	var enableMultimodal *bool
	if enableMultimodalForm != "" {
		parseBool, err := strconv.ParseBool(enableMultimodalForm)
		if err != nil {
			logger.Error(ctx, "Failed to parse enable_multimodal", err)
			_ = c.Error(errors.NewBadRequestError("Invalid enable_multimodal format").WithDetails(err.Error()))
			return
		}
		enableMultimodal = &parseBool
	}

	var processOverrides *types.KnowledgeProcessOverrides
	if raw := c.PostForm("process_config"); raw != "" {
		processOverrides = &types.KnowledgeProcessOverrides{}
		if err := json.Unmarshal([]byte(raw), processOverrides); err != nil {
			logger.Error(ctx, "Failed to parse process_config", err)
			_ = c.Error(errors.NewBadRequestError("Invalid process_config format").WithDetails(err.Error()))
			return
		}
	}
	if enableMultimodal != nil && (processOverrides == nil || processOverrides.EnableMultimodal == nil) {
		if processOverrides == nil {
			processOverrides = &types.KnowledgeProcessOverrides{EnableMultimodal: enableMultimodal}
		} else {
			processOverrides.EnableMultimodal = enableMultimodal
		}
	}

	// 获取分类ID列表（如果提供），逗号分隔，用于知识多标签分类管理
	tagIDs := parseCommaSeparatedTagIDs(c.PostForm("tag_ids"))

	channel := c.PostForm("channel")

	// Reject malformed .json at the HTTP boundary so users get 400 immediately.
	// Kept out of CreateKnowledgeFromFile so datasource sync / IM keep creating
	// a knowledge row that fails in async parse instead of vanishing.
	jsonCheckName := file.Filename
	if customFileName != "" {
		if _, base := types.SplitKnowledgeRelativePath(customFileName); base != "" {
			jsonCheckName = base
		}
	}
	if err := service.ValidateJSONUploadContent(jsonCheckName, file); err != nil {
		logger.Errorf(ctx, "Invalid JSON upload content for %s: %v", jsonCheckName, err)
		_ = c.Error(err)
		return
	}

	// Create knowledge entry from the file
	knowledge, err := h.kgService.CreateKnowledgeFromFile(
		ctx, kbID, file, metadata, enableMultimodal, customFileName, tagIDs, channel, processOverrides,
	)
	// Check for duplicate knowledge error
	if err != nil {
		if h.handleDuplicateKnowledgeError(c, err, knowledge, "file") {
			return
		}
		if appErr, ok := errors.IsAppError(err); ok {
			_ = c.Error(appErr)
			return
		}
		logger.ErrorWithFields(ctx, err, nil)
		_ = c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	logger.Infof(
		ctx,
		"Knowledge created successfully, ID: %s, title: %s",
		utils.SanitizeForLog(knowledge.ID),
		utils.SanitizeForLog(knowledge.Title),
	)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    knowledge,
	})
}

// CreateKnowledgeFromURLRequest is the body of the URL import endpoint.
type CreateKnowledgeFromURLRequest struct {
	// Page or file URL to import.
	URL              string `json:"url" binding:"required" extensions:"x-agent"`
	FileName         string `json:"file_name"`
	FileType         string `json:"file_type"`
	EnableMultimodal *bool  `json:"enable_multimodal"`
	// Title of the document; defaults to the fetched one.
	Title         string                           `json:"title" extensions:"x-agent"`
	TagIDs        []string                         `json:"tag_ids"`
	Channel       string                           `json:"channel"`
	ProcessConfig *types.KnowledgeProcessOverrides `json:"process_config"`
}

// CreateKnowledgeFromURL godoc
// @Summary      从URL创建知识
// @Description  从指定URL抓取内容并创建知识条目。当提供 file_name/file_type 或 URL 路径含已知文件扩展名时，自动切换为文件下载模式
// @Tags         知识管理
// @Accept       json
// @Produce      json
// @Param        id       path      string  true  "知识库ID"  extensions(x-agent=knowledge_base_id)
// @Param        request  body      CreateKnowledgeFromURLRequest  true  "URL请求"
// @Success      201      {object}  handler.DataResponse{data=types.Knowledge}  "创建的知识"
// @Failure      400      {object}  errors.AppError         "请求参数错误"
// @Failure      409      {object}  errors.AppError         "URL重复"
// @Security     Bearer
// @Security     ApiKeyAuth
// @x-agent      {"id":"knowledge.create_from_url","risk":"write","target":"knowledge_base.get","label":"name"}
// @x-agent-hint "Imports a user-provided public web page or file URL as a source document; ingests asynchronously. Check knowledge.get_parse_progress."
// @Router       /knowledge-bases/{id}/knowledge/url [post]
func (h *KnowledgeHandler) CreateKnowledgeFromURL(c *gin.Context) {
	ctx := c.Request.Context()
	logger.Info(ctx, "Start creating knowledge from URL")

	// Validate access to the knowledge base (only owner or admin/editor can create)
	_, kbID, effectiveTenantID, permission, err := h.validateKnowledgeBaseAccess(c)
	if err != nil {
		_ = c.Error(err)
		return
	}
	ctx = types.WithExecutionTenant(c.Request.Context(), effectiveTenantID)

	// Check write permission
	if permission != types.OrgRoleAdmin && permission != types.OrgRoleEditor {
		_ = c.Error(errors.NewForbiddenError("No permission to create knowledge"))
		return
	}

	// Parse URL from request body
	var req CreateKnowledgeFromURLRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Error(ctx, "Failed to parse URL request", err)
		_ = c.Error(errors.NewBadRequestError(err.Error()))
		return
	}

	logger.Infof(ctx, "Received URL request: %s, file_name: %s, file_type: %s",
		utils.SanitizeForLog(req.URL),
		utils.SanitizeForLog(req.FileName),
		utils.SanitizeForLog(req.FileType),
	)

	// SSRF validation for user-supplied URL
	if err := utils.ValidateURLForSSRF(req.URL); err != nil {
		logger.Warnf(ctx, "SSRF validation failed for knowledge URL: %v", err)
		_ = c.Error(errors.NewBadRequestError(utils.FormatSSRFError("URL", req.URL, err)))
		return
	}

	logger.Infof(ctx,
		"Creating knowledge from URL, knowledge base ID: %s, URL: %s",
		utils.SanitizeForLog(kbID),
		utils.SanitizeForLog(req.URL),
	)

	// Create knowledge entry from the URL
	knowledge, err := h.kgService.CreateKnowledgeFromURL(
		ctx, kbID, req.URL, req.FileName, req.FileType,
		req.EnableMultimodal, req.Title, req.TagIDs, req.Channel, req.ProcessConfig,
	)
	// Check for duplicate knowledge error
	if err != nil {
		if h.handleDuplicateKnowledgeError(c, err, knowledge, "url") {
			return
		}
		if appErr, ok := errors.IsAppError(err); ok {
			_ = c.Error(appErr)
			return
		}
		logger.ErrorWithFields(ctx, err, nil)
		_ = c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	logger.Infof(
		ctx,
		"Knowledge created successfully from URL, ID: %s, title: %s",
		utils.SanitizeForLog(knowledge.ID),
		utils.SanitizeForLog(knowledge.Title),
	)
	c.JSON(http.StatusCreated, gin.H{
		"success": true,
		"data":    knowledge,
	})
}

// CreateManualKnowledge godoc
// @Summary      手工创建知识
// @Description  手工录入Markdown格式的知识内容
// @Tags         知识管理
// @Accept       json
// @Produce      json
// @Param        id       path      string                       true  "知识库ID"  extensions(x-agent=knowledge_base_id)
// @Param        request  body      types.ManualKnowledgePayload true  "手工知识内容"
// @Success      200      {object}  handler.DataResponse{data=types.Knowledge}       "创建的知识"
// @Failure      400      {object}  errors.AppError              "请求参数错误"
// @Security     Bearer
// @Security     ApiKeyAuth
// @x-agent      {"id":"knowledge.create_manual","risk":"write","target":"knowledge_base.get","label":"name"}
// @x-agent-hint "Creates a source document from complete Markdown. Set status publish to start asynchronous ingestion; omitted status defaults to draft without indexing. Use draft only when requested. Check knowledge.get_parse_progress."
// @Router       /knowledge-bases/{id}/knowledge/manual [post]
func (h *KnowledgeHandler) CreateManualKnowledge(c *gin.Context) {
	ctx := c.Request.Context()
	logger.Info(ctx, "Start creating manual knowledge")

	// Validate access to the knowledge base (only owner or admin/editor can create)
	_, kbID, effectiveTenantID, permission, err := h.validateKnowledgeBaseAccess(c)
	if err != nil {
		_ = c.Error(err)
		return
	}
	ctx = types.WithExecutionTenant(c.Request.Context(), effectiveTenantID)

	// Check write permission
	if permission != types.OrgRoleAdmin && permission != types.OrgRoleEditor {
		_ = c.Error(errors.NewForbiddenError("No permission to create knowledge"))
		return
	}

	var req types.ManualKnowledgePayload
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Error(ctx, "Failed to parse manual knowledge request", err)
		_ = c.Error(errors.NewBadRequestError(err.Error()))
		return
	}

	knowledge, err := h.kgService.CreateKnowledgeFromManual(ctx, kbID, &req, req.Channel)
	if err != nil {
		if appErr, ok := errors.IsAppError(err); ok {
			_ = c.Error(appErr)
			return
		}
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"kb_id": kbID,
		})
		_ = c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	logger.Infof(ctx, "Manual knowledge created successfully, knowledge ID: %s",
		utils.SanitizeForLog(knowledge.ID))
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    knowledge,
	})
}

// GetKnowledge godoc
// @Summary      获取知识详情
// @Description  根据ID获取知识条目详情
// @Tags         知识管理
// @Accept       json
// @Produce      json
// @Param        id   path      string  true  "知识ID"  extensions(x-agent=knowledge_id)
// @Success      200  {object}  handler.DataResponse{data=types.Knowledge}  "知识详情"
// @Failure      400  {object}  errors.AppError                             "请求参数错误"
// @Failure      403  {object}  errors.AppError                             "无权访问所属知识库"
// @Failure      404  {object}  errors.AppError                             "知识不存在"
// @Security     Bearer
// @Security     ApiKeyAuth
// @x-agent      {"id":"knowledge.get","risk":"read"}
// @Router       /knowledge/{id} [get]
func (h *KnowledgeHandler) GetKnowledge(c *gin.Context) {
	ctx := c.Request.Context()

	logger.Info(ctx, "Start retrieving knowledge")

	id := utils.SanitizeForLog(c.Param("id"))
	if id == "" {
		logger.Error(ctx, "Knowledge ID is empty")
		_ = c.Error(errors.NewBadRequestError("Knowledge ID cannot be empty"))
		return
	}

	// Resolve knowledge and validate KB access (at least viewer)
	_, effCtx, err := h.resolveKnowledgeAndValidateKBAccess(c, id, types.OrgRoleViewer)
	if err != nil {
		_ = c.Error(err)
		return
	}

	// Re-fetch with tenant-scoped service so tags and other joined fields are populated.
	knowledge, err := h.kgService.GetKnowledgeByID(effCtx, id)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		_ = c.Error(errors.NewNotFoundError("Knowledge not found"))
		return
	}

	logger.Infof(ctx, "Knowledge retrieved successfully, ID: %s, title: %s",
		utils.SanitizeForLog(knowledge.ID), utils.SanitizeForLog(knowledge.Title))
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    knowledge,
	})
}

// dedupeKnowledgeIDs trims, drops empty and de-duplicates a batch of IDs while
// preserving the caller's order.
func dedupeKnowledgeIDs(raw []string) []string {
	seen := make(map[string]struct{}, len(raw))
	ids := make([]string, 0, len(raw))
	for _, item := range raw {
		id := strings.TrimSpace(item)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	return ids
}

// requireKnowledgeWriteAccess resolves a knowledge base from a request body and
// enforces the editor-or-admin plus ownership gate shared by the batch routes.
func (h *KnowledgeHandler) requireKnowledgeWriteAccess(
	c *gin.Context,
	requestedKBID string,
) (string, uint64, error) {
	_, kbID, effectiveTenantID, permission, err := h.validateKnowledgeBaseWriteAccessWithKBID(c, requestedKBID)
	if err != nil {
		return "", 0, err
	}
	if permission != types.OrgRoleAdmin && permission != types.OrgRoleEditor {
		return "", 0, errors.NewForbiddenError("No permission to modify knowledge")
	}
	if err := h.requireKBOwnershipOrAdmin(c, kbID); err != nil {
		return "", 0, err
	}
	return kbID, effectiveTenantID, nil
}

// requireKnowledgeInKB verifies every ID exists and belongs to the given
// knowledge base, so a batch operation cannot reach across knowledge bases.
func (h *KnowledgeHandler) requireKnowledgeInKB(
	ctx context.Context,
	tenantID uint64,
	kbID string,
	ids []string,
) error {
	knowledgeList, err := h.kgService.GetKnowledgeBatch(ctx, tenantID, ids)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		return errors.NewInternalServerError(err.Error())
	}
	if len(knowledgeList) != len(ids) {
		return errors.NewBadRequestError("One or more knowledge entries not found")
	}
	for _, k := range knowledgeList {
		if k.KnowledgeBaseID != kbID {
			return errors.NewBadRequestError(
				fmt.Sprintf("Knowledge %s does not belong to knowledge base %s",
					utils.SanitizeForLog(k.ID), utils.SanitizeForLog(kbID)))
		}
	}
	return nil
}

// DeleteKnowledge godoc
// @Summary      删除知识
// @Description  根据ID异步删除知识条目。请求会被入队到与批量删除相同的异步管道（asynq）；
// @Description  接口返回 200 仅表示任务已提交（响应 data.task_id 为任务 ID），实际删除由后台 worker 完成。
// @Tags         知识管理
// @Accept       json
// @Produce      json
// @Param        id   path      string  true  "知识ID"  extensions(x-agent=knowledge_id)
// @Success      200  {object}  handler.DataMessageResponse{data=handler.TaskIDData}  "任务已提交"
// @Failure      400  {object}  errors.AppError  "请求参数错误或知识正在移动"
// @Failure      403  {object}  errors.AppError  "需要编辑权限"
// @Failure      404  {object}  errors.AppError  "知识不存在"
// @Security     Bearer
// @Security     ApiKeyAuth
// @x-agent      {"id":"knowledge.delete","risk":"destructive","target":"knowledge.get","label":"title"}
// @x-agent-hint "Removes the document with its chunks, embeddings and graph data. Cannot be undone."
// @Router       /knowledge/{id} [delete]
func (h *KnowledgeHandler) DeleteKnowledge(c *gin.Context) {
	ctx := c.Request.Context()

	logger.Info(ctx, "Start deleting knowledge")

	id := utils.SanitizeForLog(c.Param("id"))
	if id == "" {
		logger.Error(ctx, "Knowledge ID is empty")
		_ = c.Error(errors.NewBadRequestError("Knowledge ID cannot be empty"))
		return
	}

	knowledge, effCtx, err := h.resolveKnowledgeAndValidateKBAccess(c, id, types.OrgRoleEditor)
	if err != nil {
		_ = c.Error(err)
		return
	}

	if err := access.RejectMovingKnowledge(knowledge); err != nil {
		_ = c.Error(err)
		return
	}

	// Reuse the batch async pipeline so single-item delete shares the same
	// hardening (asynq retries, business-aware queue routing, marking-as-deleting
	// inside the worker) as BatchDeleteKnowledge / ClearKnowledgeBaseContents.
	effectiveTenantID, _ := effCtx.Value(types.TenantIDContextKey).(uint64)
	if effectiveTenantID == 0 {
		logger.Error(ctx, "Effective tenant ID missing after access validation")
		_ = c.Error(errors.NewInternalServerError("workspace context unavailable"))
		return
	}

	logger.Infof(ctx, "Enqueuing knowledge delete, ID: %s", utils.SanitizeForLog(id))
	taskID, err := h.enqueueKnowledgeListDelete(effCtx, effectiveTenantID, knowledge.KnowledgeBaseID, []string{id})
	if err != nil {
		logger.Errorf(ctx, "Failed to enqueue knowledge delete task: %v", err)
		_ = c.Error(errors.NewInternalServerError("Failed to enqueue delete task"))
		return
	}

	logger.Infof(ctx, "Knowledge delete task enqueued: %s, knowledge_id: %s", taskID, utils.SanitizeForLog(id))
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Delete task submitted",
		"data": gin.H{
			"task_id": taskID,
		},
	})
}

// BatchDeleteKnowledgeRequest is the body schema for POST /knowledge/batch-delete.
type BatchDeleteKnowledgeRequest struct {
	// Knowledge base the documents belong to.
	KBID string `json:"kb_id" binding:"required" extensions:"x-agent=knowledge_base_id"`
	// Documents to delete.
	IDs []string `json:"ids"  binding:"required" extensions:"x-agent=knowledge_ids,x-agent-maximum=20"`
}

// BatchDeleteKnowledge godoc
// @Summary      批量删除知识
// @Description  按 ID 列表批量删除单个知识库下的多个知识条目
// @Tags         知识管理
// @Accept       json
// @Produce      json
// @Param        request  body      BatchDeleteKnowledgeRequest  true  "批量删除请求"
// @Success      200      {object}  handler.SubmittedTaskResponse  "删除成功"
// @Failure      400      {object}  errors.AppError              "请求参数错误"
// @Failure      403      {object}  errors.AppError              "权限不足"
// @Security     Bearer
// @Security     ApiKeyAuth
// @x-agent      {"id":"knowledge.batch_delete","risk":"destructive","target":"knowledge_base.get","label":"name","items":{"param":"knowledge_ids","target":"knowledge.get","as":"knowledge_id","label":"title"}}
// @x-agent-hint "Removes up to 20 documents of one knowledge base with their chunks, embeddings and graph data. Cannot be undone; the confirmation lists every document."
// @Router       /knowledge/batch-delete [post]
func (h *KnowledgeHandler) BatchDeleteKnowledge(c *gin.Context) {
	var req BatchDeleteKnowledgeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(errors.NewBadRequestError("Invalid request parameters: " + err.Error()))
		return
	}

	ids := dedupeKnowledgeIDs(req.IDs)
	if len(ids) == 0 {
		_ = c.Error(errors.NewBadRequestError("ids cannot be empty"))
		return
	}
	const maxBatch = 200
	if len(ids) > maxBatch {
		_ = c.Error(errors.NewBadRequestError(fmt.Sprintf("too many ids (max %d per batch)", maxBatch)))
		return
	}

	// Validate KB access (editor or admin) using the kb_id from body.
	_, kbID, effectiveTenantID, permission, err := h.validateKnowledgeBaseWriteAccessWithKBID(c, req.KBID)
	if err != nil {
		_ = c.Error(err)
		return
	}
	if permission != types.OrgRoleAdmin && permission != types.OrgRoleEditor {
		_ = c.Error(errors.NewForbiddenError("No permission to delete knowledge"))
		return
	}
	if err := h.requireKBOwnershipOrAdmin(c, kbID); err != nil {
		_ = c.Error(err)
		return
	}
	ctx := types.WithExecutionTenant(c.Request.Context(), effectiveTenantID)

	// Single batch fetch to validate that every id exists and belongs to the
	// requested KB before admitting a task with that exact KB scope.
	knowledgeList, err := h.kgService.GetKnowledgeBatch(ctx, effectiveTenantID, ids)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		_ = c.Error(errors.NewInternalServerError(err.Error()))
		return
	}
	if len(knowledgeList) != len(ids) {
		_ = c.Error(errors.NewBadRequestError("One or more knowledge entries not found"))
		return
	}
	for _, k := range knowledgeList {
		if err := access.RejectMovingKnowledge(k); err != nil {
			_ = c.Error(err)
			return
		}
		if k.KnowledgeBaseID != kbID {
			_ = c.Error(errors.NewBadRequestError(
				fmt.Sprintf("Knowledge %s does not belong to knowledge base %s",
					utils.SanitizeForLog(k.ID), utils.SanitizeForLog(kbID))))
			return
		}
	}

	taskID, err := h.enqueueKnowledgeListDelete(ctx, effectiveTenantID, kbID, ids)
	if err != nil {
		logger.Errorf(ctx, "Failed to enqueue batch knowledge delete task: %v", err)
		_ = c.Error(errors.NewInternalServerError("Failed to enqueue batch delete task"))
		return
	}

	logger.Infof(ctx, "Batch knowledge delete task enqueued: %s, kb_id: %s, count: %d",
		taskID, utils.SanitizeForLog(kbID), len(ids))

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Batch delete task submitted",
		"data": gin.H{
			"task_id":       taskID,
			"deleted_count": len(ids),
		},
	})
}

// ClearKnowledgeBaseContents godoc
// @Summary      清空知识库内容
// @Description  删除知识库下的所有知识条目（异步任务）。知识库本身保留，仅清空其中的内容
// @Tags         知识管理
// @Accept       json
// @Produce      json
// @Param        id   path      string  true  "知识库ID"
// @Success      200  {object}  handler.SubmittedTaskResponse  "清空任务已提交"
// @Failure      400  {object}  errors.AppError         "请求参数错误"
// @Failure      403  {object}  errors.AppError         "权限不足"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases/{id}/knowledge [delete]
func (h *KnowledgeHandler) ClearKnowledgeBaseContents(c *gin.Context) {
	ctx := c.Request.Context()
	logger.Info(ctx, "Start clearing knowledge base contents")

	kb, kbID, effectiveTenantID, permission, err := h.validateKnowledgeBaseWriteAccessWithKBID(c, c.Param("id"))
	if err != nil {
		_ = c.Error(err)
		return
	}

	// Only owner (admin with matching tenant) can clear knowledge base contents
	tenantID := c.GetUint64(types.TenantIDContextKey.String())
	if kb.TenantID != tenantID || permission != types.OrgRoleAdmin {
		_ = c.Error(errors.NewForbiddenError("Only knowledge base owner can clear contents"))
		return
	}

	ctx = types.WithExecutionTenant(c.Request.Context(), effectiveTenantID)

	knowledgeList, err := h.kgService.ListKnowledgeByKnowledgeBaseID(ctx, kbID)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		_ = c.Error(errors.NewInternalServerError("Failed to list knowledge entries").WithDetails(err.Error()))
		return
	}

	if len(knowledgeList) == 0 {
		logger.Infof(ctx, "Knowledge base %s is already empty", utils.SanitizeForLog(kbID))
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"message": "Knowledge base is already empty",
			"data":    gin.H{"deleted_count": 0},
		})
		return
	}

	knowledgeIDs := make([]string, 0, len(knowledgeList))
	for _, knowledge := range knowledgeList {
		if err := access.RejectMovingKnowledge(knowledge); err != nil {
			_ = c.Error(err)
			return
		}
		knowledgeIDs = append(knowledgeIDs, knowledge.ID)
	}

	taskID, err := h.enqueueKnowledgeListDelete(ctx, effectiveTenantID, kbID, knowledgeIDs)
	if err != nil {
		logger.Errorf(ctx, "Failed to enqueue knowledge list delete task: %v", err)
		_ = c.Error(errors.NewInternalServerError("Failed to enqueue cleanup task"))
		return
	}

	logger.Infof(ctx, "Knowledge base contents clear task enqueued: %s, kb_id: %s, count: %d",
		taskID, utils.SanitizeForLog(kbID), len(knowledgeIDs))

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Knowledge base contents clear task submitted",
		"data":    gin.H{"deleted_count": len(knowledgeIDs)},
	})
}

// DownloadKnowledgeFile godoc
// @Summary      下载知识文件
// @Description  下载知识条目关联的原始文件
// @Tags         知识管理
// @Accept       json
// @Produce      application/octet-stream
// @Param        id   path      string  true  "知识ID"
// @Success      200  {file}    file    "文件内容"
// @Failure      400  {object}  errors.AppError  "请求参数错误"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge/{id}/download [get]
func (h *KnowledgeHandler) DownloadKnowledgeFile(c *gin.Context) {
	ctx := c.Request.Context()

	logger.Info(ctx, "Start downloading knowledge file")

	id := utils.SanitizeForLog(c.Param("id"))
	if id == "" {
		logger.Error(ctx, "Knowledge ID is empty")
		_ = c.Error(errors.NewBadRequestError("Knowledge ID cannot be empty"))
		return
	}

	// Keep a handler-level Editor check in addition to the route guard. The
	// original file is more sensitive than parsed-content reads and must not
	// be downloadable through a read-only organization share.
	_, effCtx, err := h.resolveKnowledgeAndValidateKBAccess(c, id, types.OrgRoleEditor)
	if err != nil {
		_ = c.Error(err)
		return
	}
	logger.Infof(ctx, "Retrieving knowledge file, ID: %s", utils.SanitizeForLog(id))

	file, filename, err := h.kgService.GetKnowledgeFile(effCtx, id)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		_ = c.Error(errors.NewInternalServerError("Failed to retrieve file"))
		return
	}
	c.Header("Content-Description", "File Transfer")
	c.Header("Content-Transfer-Encoding", "binary")
	c.Header("Expires", "0")
	if err := filetransport.Serve(c.Writer, c.Request, file, filetransport.Options{
		Filename: filename, Download: true, ContentType: "application/octet-stream", CacheControl: "private, no-store",
	}); err != nil {
		logger.Errorf(ctx, "Failed to send file: %v", err)
	}
}

// PreviewKnowledgeFile godoc
// @Summary      预览知识文件
// @Description  返回知识条目关联的原始文件，Content-Type 根据文件类型设置，用于浏览器内嵌预览
// @Tags         知识管理
// @Accept       json
// @Produce      application/pdf,image/jpeg,image/png,text/plain
// @Param        id   path      string  true  "知识ID"
// @Success      200  {file}    file    "文件内容"
// @Failure      400  {object}  errors.AppError  "请求参数错误"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge/{id}/preview [get]
func (h *KnowledgeHandler) PreviewKnowledgeFile(c *gin.Context) {
	ctx := c.Request.Context()

	id := utils.SanitizeForLog(c.Param("id"))
	if id == "" {
		_ = c.Error(errors.NewBadRequestError("Knowledge ID cannot be empty"))
		return
	}

	_, effCtx, err := h.resolveKnowledgeAndValidateKBAccess(c, id, types.OrgRoleEditor)
	if err != nil {
		_ = c.Error(err)
		return
	}

	file, filename, err := h.kgService.GetKnowledgeFile(effCtx, id)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		_ = c.Error(errors.NewInternalServerError("Failed to retrieve file"))
		return
	}
	if err := filetransport.Serve(c.Writer, c.Request, file, filetransport.Options{
		Filename: filename, CacheControl: "private, no-store",
	}); err != nil {
		logger.Errorf(ctx, "Failed to stream preview: %v", err)
	}
}
