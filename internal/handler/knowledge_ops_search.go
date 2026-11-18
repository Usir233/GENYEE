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
	"github.com/Usir233/GENYEE/internal/errors"
	"github.com/Usir233/GENYEE/internal/logger"
	"github.com/Usir233/GENYEE/internal/tracing/langfuse"
	"github.com/Usir233/GENYEE/internal/types"
	"github.com/Usir233/GENYEE/internal/utils"
	"github.com/gin-gonic/gin"
	"github.com/hibiken/asynq"
)

// UpdateKnowledgeRequest defines the partial-update body for PUT /knowledge/:id.
// Omitted fields are left unchanged; an explicit empty description clears the summary.
type UpdateKnowledgeRequest struct {
	// New title; omit to keep it.
	Title *string `json:"title" extensions:"x-agent"`
	// New summary; omit to keep it, empty clears it.
	Description    *string         `json:"description" extensions:"x-agent"`
	CustomMetadata json.RawMessage `json:"custom_metadata"`
}

// UpdateKnowledge godoc
// @Summary      更新知识
// @Description  部分更新知识条目（标题/描述/自定义元数据）；未传字段保持不变，显式传空 description 可清空摘要
// @Tags         知识管理
// @Accept       json
// @Produce      json
// @Param        id       path      string                   true  "知识ID"  extensions(x-agent=knowledge_id)
// @Param        request  body      UpdateKnowledgeRequest   true  "更新字段（均可选）"
// @Success      200      {object}  handler.UpdateKnowledgeResponse  "更新成功"
// @Failure      400      {object}  errors.AppError         "请求参数错误"
// @Security     Bearer
// @Security     ApiKeyAuth
// @x-agent      {"id":"knowledge.update","risk":"write","target":"knowledge.get","label":"title"}
// @x-agent-hint "Sets a document's title or summary; a field left out is kept."
// @Router       /knowledge/{id} [put]
func (h *KnowledgeHandler) UpdateKnowledge(c *gin.Context) {
	ctx := c.Request.Context()

	id := utils.SanitizeForLog(c.Param("id"))
	if id == "" {
		logger.Error(ctx, "Knowledge ID is empty")
		_ = c.Error(errors.NewBadRequestError("Knowledge ID cannot be empty"))
		return
	}

	_, effCtx, err := h.resolveKnowledgeAndValidateKBAccess(c, id, types.OrgRoleEditor)
	if err != nil {
		_ = c.Error(err)
		return
	}

	var req UpdateKnowledgeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Error(ctx, "Failed to parse request parameters", err)
		_ = c.Error(errors.NewBadRequestError(err.Error()))
		return
	}
	knowledge := types.Knowledge{ID: id, CustomMetadata: types.JSON(req.CustomMetadata)}
	if req.Title != nil {
		knowledge.Title = *req.Title
	}
	if req.Description != nil {
		knowledge.Description = *req.Description
		knowledge.DescriptionSpecified = true
	}

	if err := h.kgService.UpdateKnowledge(effCtx, &knowledge); err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		var appErr *errors.AppError
		if goerrors.As(err, &appErr) {
			_ = c.Error(appErr)
		} else {
			_ = c.Error(errors.NewInternalServerError(err.Error()))
		}
		return
	}
	updated, getErr := h.kgService.GetKnowledgeByID(effCtx, id)
	if getErr != nil {
		logger.Warnf(ctx, "Knowledge updated but failed to reload status for %s: %v", id, getErr)
	}

	logger.Infof(ctx, "Knowledge updated successfully, knowledge ID: %s", id)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Knowledge updated successfully",
		"data":    updated,
	})
}

// RegenerateKnowledgeSummary refreshes a stale summary after chunk or metadata edits.
//
// RegenerateKnowledgeSummary godoc
// @Summary      重新生成知识摘要
// @Description  在分块或元数据编辑后刷新或重新触发知识条目的文档摘要生成。
// @Tags         知识管理
// @Accept       json
// @Produce      json
// @Param        id   path      string  true  "知识ID"
// @Success      200  {object}  handler.DataResponse{data=types.Knowledge}  "更新后的知识"
// @Failure      400  {object}  errors.AppError                             "请求参数错误"
// @Failure      403  {object}  errors.AppError                             "权限不足"
// @Failure      404  {object}  errors.AppError                             "知识不存在"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge/{id}/regenerate-summary [post]
func (h *KnowledgeHandler) RegenerateKnowledgeSummary(c *gin.Context) {
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
	knowledge, err := h.kgService.GetKnowledgeByID(effCtx, id)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		var appErr *errors.AppError
		if goerrors.As(err, &appErr) {
			_ = c.Error(appErr)
		} else {
			_ = c.Error(errors.NewInternalServerError(err.Error()))
		}
		return
	}
	if knowledge.SummaryStatus == "" || knowledge.SummaryStatus == types.SummaryStatusNone {
		knowledge, err = h.kgService.RegenerateKnowledgeSummary(effCtx, id)
	} else {
		err = h.kgService.RequestKnowledgeSummaryRefresh(effCtx, id)
		if err == nil {
			knowledge, err = h.kgService.GetKnowledgeByID(effCtx, id)
		}
	}
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		var appErr *errors.AppError
		if goerrors.As(err, &appErr) {
			_ = c.Error(appErr)
		} else {
			_ = c.Error(errors.NewBadRequestError(err.Error()))
		}
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": knowledge})
}

// UpdateManualKnowledge godoc
// @Summary      更新手工知识
// @Description  更新手工录入的Markdown知识内容
// @Tags         知识管理
// @Accept       json
// @Produce      json
// @Param        id       path      string                       true  "知识ID"  extensions(x-agent=knowledge_id)
// @Param        request  body      types.ManualKnowledgePayload true  "手工知识内容"
// @Success      200      {object}  handler.DataResponse{data=types.Knowledge}       "更新后的知识"
// @Failure      400      {object}  errors.AppError              "请求参数错误"
// @Security     Bearer
// @Security     ApiKeyAuth
// @x-agent      {"id":"knowledge.update_manual","risk":"write","target":"knowledge.get","label":"title"}
// @x-agent-hint "Replaces the whole Markdown of a document created as manual knowledge. Send the complete content, not a diff. status draft keeps it out of search; publish indexes it. Uploaded files cannot be edited this way."
// @Router       /knowledge/manual/{id} [put]
func (h *KnowledgeHandler) UpdateManualKnowledge(c *gin.Context) {
	ctx := c.Request.Context()
	logger.Info(ctx, "Start updating manual knowledge")

	id := utils.SanitizeForLog(c.Param("id"))
	if id == "" {
		logger.Error(ctx, "Knowledge ID is empty")
		_ = c.Error(errors.NewBadRequestError("Knowledge ID cannot be empty"))
		return
	}

	_, effCtx, err := h.resolveKnowledgeAndValidateKBAccess(c, id, types.OrgRoleEditor)
	if err != nil {
		_ = c.Error(err)
		return
	}

	var req types.ManualKnowledgePayload
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Error(ctx, "Failed to parse manual knowledge update request", err)
		_ = c.Error(errors.NewBadRequestError(err.Error()))
		return
	}

	knowledge, err := h.kgService.UpdateManualKnowledge(effCtx, id, &req)
	if err != nil {
		if appErr, ok := errors.IsAppError(err); ok {
			_ = c.Error(appErr)
			return
		}
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"knowledge_id": id,
		})
		_ = c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	logger.Infof(ctx, "Manual knowledge updated successfully, knowledge ID: %s", id)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    knowledge,
	})
}

// ReparseKnowledgeRequest is the optional body of POST /knowledge/{id}/reparse.
type ReparseKnowledgeRequest struct {
	// ProcessConfig overrides the processing options stored at upload time.
	ProcessConfig *types.KnowledgeProcessOverrides `json:"process_config"`
}

// ReparseKnowledge godoc
// @Summary      重新解析知识
// @Description  删除知识中现有的文档内容并重新解析，使用异步任务方式处理
// @Tags         知识管理
// @Accept       json
// @Produce      json
// @Param        id    path  string                   true   "知识ID"  extensions(x-agent=knowledge_id)
// @Param        body  body  ReparseKnowledgeRequest  false  "可选；不传则沿用上传时的处理配置"
// @Success      200  {object}  handler.DataMessageResponse{data=types.Knowledge}  "重新解析任务已提交"
// @Failure      400  {object}  errors.AppError  "请求参数错误"
// @Failure      403  {object}  errors.AppError  "权限不足"
// @Failure      404  {object}  errors.AppError  "知识不存在"
// @Security     Bearer
// @Security     ApiKeyAuth
// @x-agent      {"id":"knowledge.reparse","risk":"write","target":"knowledge.get","label":"title"}
// @x-agent-hint "Runs in the background; follow it with knowledge.get_parse_progress. Search results change meanwhile."
// @Router       /knowledge/{id}/reparse [post]
func (h *KnowledgeHandler) ReparseKnowledge(c *gin.Context) {
	ctx := c.Request.Context()
	logger.Info(ctx, "Start re-parsing knowledge")

	id := utils.SanitizeForLog(c.Param("id"))
	if id == "" {
		logger.Error(ctx, "Knowledge ID is empty")
		_ = c.Error(errors.NewBadRequestError("Knowledge ID cannot be empty"))
		return
	}

	// Validate KB access with editor permission (reparse requires write access)
	_, effCtx, err := h.resolveKnowledgeAndValidateKBAccess(c, id, types.OrgRoleEditor)
	if err != nil {
		_ = c.Error(err)
		return
	}

	// Optional per-reparse parse config override. Empty body keeps the
	// overrides stored at upload time.
	var processOverrides *types.KnowledgeProcessOverrides
	if c.Request.ContentLength != 0 {
		var req ReparseKnowledgeRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			logger.Error(ctx, "Failed to parse reparse request body", err)
			_ = c.Error(errors.NewBadRequestError("Invalid reparse request body").WithDetails(err.Error()))
			return
		}
		processOverrides = req.ProcessConfig
	}

	// Call service to reparse knowledge
	knowledge, err := h.kgService.ReparseKnowledge(effCtx, id, processOverrides)
	if err != nil {
		if appErr, ok := errors.IsAppError(err); ok {
			_ = c.Error(appErr)
			return
		}
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"knowledge_id": id,
		})
		_ = c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	logger.Infof(ctx, "Knowledge reparse task submitted successfully, knowledge ID: %s", id)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Knowledge reparse task submitted",
		"data":    knowledge,
	})
}

// CancelKnowledgeParse godoc
// @Summary      取消知识解析
// @Description  取消进行中的知识解析任务；已写入分块保留，可通过 reparse 重新解析。终态或删除中的知识不支持取消。
// @Tags         知识管理
// @Accept       json
// @Produce      json
// @Param        id   path      string  true  "知识ID"  extensions(x-agent=knowledge_id)
// @Success      200  {object}  handler.DataMessageResponse{data=types.Knowledge}  "取消已提交"
// @Failure      400  {object}  errors.AppError         "状态不支持取消"
// @Failure      403  {object}  errors.AppError         "权限不足"
// @Failure      404  {object}  errors.AppError         "知识不存在"
// @Security     Bearer
// @Security     ApiKeyAuth
// @x-agent      {"id":"knowledge.cancel_parse","risk":"write","target":"knowledge.get","label":"title"}
// @Router       /knowledge/{id}/cancel-parse [post]
func (h *KnowledgeHandler) CancelKnowledgeParse(c *gin.Context) {
	ctx := c.Request.Context()
	logger.Info(ctx, "Start cancelling knowledge parse")

	id := utils.SanitizeForLog(c.Param("id"))
	if id == "" {
		logger.Error(ctx, "Knowledge ID is empty")
		_ = c.Error(errors.NewBadRequestError("Knowledge ID cannot be empty"))
		return
	}

	// Editor permission — same gate as ReparseKnowledge / DeleteKnowledge.
	_, effCtx, err := h.resolveKnowledgeAndValidateKBAccess(c, id, types.OrgRoleEditor)
	if err != nil {
		_ = c.Error(err)
		return
	}

	knowledge, err := h.kgService.CancelKnowledgeParse(effCtx, id)
	if err != nil {
		if appErr, ok := errors.IsAppError(err); ok {
			_ = c.Error(appErr)
			return
		}
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"knowledge_id": id,
		})
		_ = c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	logger.Infof(ctx, "Knowledge parse cancelled successfully, knowledge ID: %s", id)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Knowledge parse cancelled",
		"data":    knowledge,
	})
}

// KnowledgeTagBatchRequest sets tags on documents. The editor sends updates;
// the assistant sends knowledge_ids and tag_ids for one knowledge base.
type KnowledgeTagBatchRequest struct {
	// Map of knowledge id to the tag ids that replace its current tags.
	Updates map[string][]string `json:"updates"`
	// Knowledge base the documents belong to.
	KBID string `json:"kb_id" extensions:"x-agent=knowledge_base_id,x-agent-required"`
	// Documents that receive the same tags. Mutually exclusive with updates.
	KnowledgeIDs []string `json:"knowledge_ids" extensions:"x-agent,x-agent-required,x-agent-maximum=20"`
	// Tags to set on every listed document. An empty list clears them.
	TagIDs []string `json:"tag_ids" extensions:"x-agent,x-agent-required,x-agent-maximum=50"`
}

const (
	maxKnowledgeTagBatchDocuments = 20
	maxKnowledgeTagBatchTags      = 50
)

// applyKnowledgeTagBatch turns the list form into the updates map the
// service already stores. An omitted tag_ids with knowledge_ids is refused
// so a partial body cannot clear tags by accident; an empty list still clears.
func applyKnowledgeTagBatch(req *KnowledgeTagBatchRequest) error {
	if len(req.KnowledgeIDs) == 0 {
		if len(req.Updates) == 0 {
			return errors.NewBadRequestError("updates is required")
		}
		return nil
	}
	if len(req.Updates) > 0 {
		return errors.NewBadRequestError("knowledge_ids and updates cannot both be set")
	}
	if req.TagIDs == nil {
		return errors.NewBadRequestError("tag_ids is required when knowledge_ids is set")
	}
	if len(req.KnowledgeIDs) > maxKnowledgeTagBatchDocuments {
		return errors.NewBadRequestError("at most 20 documents")
	}
	if len(req.TagIDs) > maxKnowledgeTagBatchTags {
		return errors.NewBadRequestError("at most 50 tags")
	}
	seen := make(map[string]struct{}, len(req.KnowledgeIDs))
	updates := make(map[string][]string, len(req.KnowledgeIDs))
	for _, id := range req.KnowledgeIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			return errors.NewBadRequestError("knowledge id cannot be empty")
		}
		if _, ok := seen[id]; ok {
			return errors.NewBadRequestError("duplicate knowledge id")
		}
		seen[id] = struct{}{}
		updates[id] = req.TagIDs
	}
	req.Updates = updates
	return nil
}

// UpdateKnowledgeTagBatch godoc
// @Summary      批量更新知识标签
// @Description  批量更新知识条目的标签（传 updates 或 knowledge_ids + tag_ids）；可选 kb_id 指定知识库范围。
// @Tags         知识管理
// @Accept       json
// @Produce      json
// @Param        request  body      handler.KnowledgeTagBatchRequest  true  "标签更新请求"
// @Success      200      {object}  handler.SuccessResponse  "更新成功"
// @Failure      400      {object}  errors.AppError         "请求参数错误"
// @Security     Bearer
// @Security     ApiKeyAuth
// @x-agent      {"id":"knowledge.set_tags","risk":"write","target":"knowledge_base.get","label":"name","items":{"param":"knowledge_ids","target":"knowledge.get","as":"knowledge_id","label":"title"}}
// @x-agent-hint "Sets the same tags on up to 20 documents in one knowledge base, replacing the tags they have now. To add or remove one tag, read each document's tags with knowledge.get or knowledge.list first and pass the full resulting list. tag_ids come from knowledge_base.list_tags; an empty tag_ids clears every tag. It does not delete documents."
// @Router       /knowledge/tags [put]
func (h *KnowledgeHandler) UpdateKnowledgeTagBatch(c *gin.Context) {
	ctx := c.Request.Context()

	// Ensure tenant ID is in context (service reads it; may be missing if request context was not set by auth)
	tenantID := c.GetUint64(types.TenantIDContextKey.String())
	if tenantID == 0 {
		_ = c.Error(errors.NewUnauthorizedError("Unauthorized"))
		return
	}
	ctx = types.WithExecutionTenant(ctx, tenantID)

	var req KnowledgeTagBatchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Error(ctx, "Failed to parse knowledge tag batch request", err)
		_ = c.Error(errors.NewBadRequestError("请求参数不合法").WithDetails(err.Error()))
		return
	}
	if err := applyKnowledgeTagBatch(&req); err != nil {
		_ = c.Error(err)
		return
	}
	// Resolve effective tenant and the authorized KB scope.
	var authorizedKBID string
	if kbID := utils.SanitizeForLog(req.KBID); kbID != "" {
		_, _, effID, permission, err := h.validateKnowledgeBaseWriteAccessWithKBID(c, kbID)
		if err != nil {
			_ = c.Error(err)
			return
		}
		if permission != types.OrgRoleAdmin && permission != types.OrgRoleEditor {
			_ = c.Error(errors.NewForbiddenError("No permission to update knowledge tags"))
			return
		}
		authorizedKBID = kbID
		ctx = types.WithExecutionTenant(c.Request.Context(), effID)
	} else if len(req.Updates) > 0 {
		// No kb_id: infer from first knowledge ID so shared-KB updates work without client sending kb_id
		var firstKnowledgeID string
		for id := range req.Updates {
			firstKnowledgeID = id
			break
		}
		if firstKnowledgeID != "" {
			knowledge, effCtx, err := h.resolveKnowledgeAndValidateKBAccess(c, firstKnowledgeID, types.OrgRoleEditor)
			if err != nil {
				_ = c.Error(err)
				return
			}
			authorizedKBID = knowledge.KnowledgeBaseID
			ctx = effCtx
		}
	}
	if err := h.requireKBOwnershipOrAdmin(c, authorizedKBID); err != nil {
		_ = c.Error(err)
		return
	}

	if err := h.kgService.UpdateKnowledgeTagBatch(ctx, authorizedKBID, req.Updates); err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
	})
}

// UpdateImageInfo godoc
// @Summary      更新图像信息
// @Description  更新知识分块的图像信息
// @Tags         知识管理
// @Accept       json
// @Produce      json
// @Param        id        path      string  true  "知识ID"
// @Param        chunk_id  path      string  true  "分块ID"
// @Param        request   body      object{image_info=string}  true  "图像信息"
// @Success      200       {object}  handler.MessageResponse     "更新成功"
// @Failure      400       {object}  errors.AppError            "请求参数错误"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge/image/{id}/{chunk_id} [put]
func (h *KnowledgeHandler) UpdateImageInfo(c *gin.Context) {
	ctx := c.Request.Context()
	logger.Info(ctx, "Start updating image info")

	id := utils.SanitizeForLog(c.Param("id"))
	if id == "" {
		logger.Error(ctx, "Knowledge ID is empty")
		_ = c.Error(errors.NewBadRequestError("Knowledge ID cannot be empty"))
		return
	}
	chunkID := utils.SanitizeForLog(c.Param("chunk_id"))
	if chunkID == "" {
		logger.Error(ctx, "Chunk ID is empty")
		_ = c.Error(errors.NewBadRequestError("Chunk ID cannot be empty"))
		return
	}

	_, effCtx, err := h.resolveKnowledgeAndValidateKBAccess(c, id, types.OrgRoleEditor)
	if err != nil {
		_ = c.Error(err)
		return
	}

	var request struct {
		ImageInfo string `json:"image_info"`
	}

	if err := c.ShouldBindJSON(&request); err != nil {
		logger.Error(ctx, "Failed to parse request parameters", err)
		_ = c.Error(errors.NewBadRequestError(err.Error()))
		return
	}

	logger.Infof(ctx, "Updating knowledge chunk, knowledge ID: %s, chunk ID: %s", id, chunkID)
	err = h.kgService.UpdateImageInfo(effCtx, id, chunkID, utils.SanitizeForLog(request.ImageInfo))
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		var appErr *errors.AppError
		if goerrors.As(err, &appErr) {
			_ = c.Error(appErr)
		} else {
			_ = c.Error(errors.NewInternalServerError(err.Error()))
		}
		return
	}

	logger.Infof(ctx, "Knowledge chunk updated successfully, knowledge ID: %s, chunk ID: %s", id, chunkID)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Knowledge chunk image updated successfully",
	})
}

// SearchKnowledge godoc
// @Summary      Search knowledge
// @Description  Search knowledge files by keyword (or pass recent=true); optional agent_id scopes to shared agent KBs.
// @Tags         Knowledge
// @Accept       json
// @Produce      json
// @Param        keyword    query     string  false "Keyword to search"
// @Param        offset     query     int     false "Offset for pagination (minimum 0)" minimum(0)
// @Param        limit      query     int     false "Limit for pagination (default 20, maximum 100)" minimum(1) maximum(100)
// @Param        file_types query     string  false "Comma-separated file extensions to filter (e.g., csv,xlsx)"
// @Param        agent_id   query     string  false "Shared agent ID (search within agent's KB scope)"
// @Param        recent     query     bool    false "Return recent files when keyword is empty"
// @Success      200         {object}  handler.KnowledgeSearchKnowledgeResponse     "Search results"
// @Failure      400         {object}  errors.AppError            "Invalid request"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge/search [get]
func (h *KnowledgeHandler) SearchKnowledge(c *gin.Context) {
	ctx := c.Request.Context()
	if userID, ok := c.Get(types.UserIDContextKey.String()); ok {
		ctx = context.WithValue(ctx, types.UserIDContextKey, userID)
	}
	// Accept both ?keyword= (legacy / upstream name) and ?query= (what most
	// MCP / agent integrations send). Empty input is only valid for an explicit
	// recent-file browse request; ordinary callers still get a clear 400 instead
	// of silently receiving the same newest cards for every missing query.
	keyword := c.Query("keyword")
	if keyword == "" {
		keyword = c.Query("query")
	}
	recent, _ := strconv.ParseBool(c.DefaultQuery("recent", "false"))
	if strings.TrimSpace(keyword) == "" && !recent {
		_ = c.Error(errors.NewBadRequestError("missing search keyword: pass ?keyword=... or ?query=..."))
		return
	}
	keyword = strings.TrimSpace(keyword)
	offset, limit, ok := parseOffsetPagination(c)
	if !ok {
		return
	}

	var fileTypes []string
	if fileTypesStr := c.Query("file_types"); fileTypesStr != "" {
		for _, ft := range strings.Split(fileTypesStr, ",") {
			ft = strings.TrimSpace(ft)
			if ft != "" {
				fileTypes = append(fileTypes, ft)
			}
		}
	}

	agentID := c.Query("agent_id")
	if agentID != "" {
		agent, err := resolveSharedAgentForRequest(c, agentID, h.agentShareService)
		if err != nil {
			_ = c.Error(err)
			return
		}
		sourceTenantID := agent.TenantID
		ctx = access.WithSharedAgent(ctx, agent)
		scope := types.NewSharedAgentKBScope(agent)
		if scope.IsEmpty() {
			c.JSON(http.StatusOK, gin.H{
				"success":  true,
				"data":     []interface{}{},
				"has_more": false,
				"total":    0,
			})
			return
		}
		var scopes []types.KnowledgeSearchScope
		if !scope.IsAll() {
			for _, kbID := range scope.IDs() {
				if kbID != "" {
					scopes = append(scopes, types.KnowledgeSearchScope{TenantID: sourceTenantID, KBID: kbID})
				}
			}
		}
		if scope.IsAll() {
			kbs, err := h.kbService.ListKnowledgeBasesByTenantID(ctx, sourceTenantID)
			if err != nil {
				logger.ErrorWithFields(ctx, err, nil)
				_ = c.Error(errors.NewInternalServerError("Failed to list knowledge bases").WithDetails(err.Error()))
				return
			}
			for _, kb := range filterKnowledgeBasesForSharedAgent(kbs, agent) {
				if kb.Type == types.KnowledgeBaseTypeDocument {
					scopes = append(scopes, types.KnowledgeSearchScope{TenantID: kb.TenantID, KBID: kb.ID})
				}
			}
		}
		scopes = filterKnowledgeSearchScopesForAPIKey(ctx, scopes)
		if len(scopes) == 0 {
			c.JSON(http.StatusOK, gin.H{
				"success":  true,
				"data":     []interface{}{},
				"has_more": false,
				"total":    0,
			})
			return
		}
		knowledges, hasMore, total, err := h.kgService.SearchKnowledgeForScopes(
			ctx, scopes, keyword, offset, limit, fileTypes,
		)
		if err != nil {
			logger.ErrorWithFields(ctx, err, nil)
			_ = c.Error(errors.NewInternalServerError("Failed to search knowledge").WithDetails(err.Error()))
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"success":  true,
			"data":     knowledges,
			"has_more": hasMore,
			"total":    total,
		})
		return
	}

	if scopes, restricted := tenantAPIKeySearchScopes(ctx); restricted {
		if len(scopes) == 0 {
			c.JSON(http.StatusOK, gin.H{
				"success":  true,
				"data":     []interface{}{},
				"has_more": false,
				"total":    0,
			})
			return
		}
		knowledges, hasMore, total, err := h.kgService.SearchKnowledgeForScopes(
			ctx, scopes, keyword, offset, limit, fileTypes,
		)
		if err != nil {
			logger.ErrorWithFields(ctx, err, nil)
			_ = c.Error(errors.NewInternalServerError("Failed to search knowledge").WithDetails(err.Error()))
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"success":  true,
			"data":     knowledges,
			"has_more": hasMore,
			"total":    total,
		})
		return
	}

	// Default: own + shared KBs
	knowledges, hasMore, total, err := h.kgService.SearchKnowledge(ctx, keyword, offset, limit, fileTypes)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		_ = c.Error(errors.NewInternalServerError("Failed to search knowledge").WithDetails(err.Error()))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success":  true,
		"data":     knowledges,
		"has_more": hasMore,
		"total":    total,
	})
}

// MoveKnowledgeRequest defines the request for moving knowledge items
type MoveKnowledgeRequest struct {
	// Documents to move.
	KnowledgeIDs []string `json:"knowledge_ids" binding:"required,min=1" extensions:"x-agent,x-agent-maximum=20"`
	// Knowledge base the documents are in now.
	SourceKBID string `json:"source_kb_id"  binding:"required" extensions:"x-agent=source_knowledge_base_id"`
	// Knowledge base to move them to.
	TargetKBID string `json:"target_kb_id"  binding:"required" extensions:"x-agent=knowledge_base_id"`
	// reuse_vectors keeps the embeddings; reparse processes the documents again.
	Mode string `json:"mode"          binding:"required,oneof=reuse_vectors reparse" enums:"reuse_vectors,reparse" extensions:"x-agent"`
}

// MoveKnowledgeResponse defines the response for move knowledge
type MoveKnowledgeResponse struct {
	TaskID         string `json:"task_id" extensions:"x-agent"`
	SourceKBID     string `json:"source_kb_id"`
	TargetKBID     string `json:"target_kb_id"`
	KnowledgeCount int    `json:"knowledge_count" extensions:"x-agent"`
	Message        string `json:"message"`
}

// MoveKnowledge moves knowledge items from one knowledge base to another (async task).
//
// MoveKnowledge godoc
// @Summary      移动知识到其他知识库
// @Description  将一条或多条知识从源知识库移动到目标知识库（异步），返回任务 ID 用于查询进度
// @Tags         知识
// @Accept       json
// @Produce      json
// @Param        request  body      handler.MoveKnowledgeRequest  true  "{source_kb_id, target_kb_id, knowledge_ids}"
// @Success      200      {object}  handler.DataResponse{data=handler.MoveKnowledgeResponse}  "任务信息"
// @Failure      400      {object}  errors.AppError                "请求参数错误"
// @Security     Bearer
// @Security     ApiKeyAuth
// @x-agent      {"id":"knowledge.move","risk":"write","target":"knowledge_base.get","label":"name","items":{"param":"knowledge_ids","target":"knowledge.get","as":"knowledge_id","label":"title"}}
// @x-agent-hint "Moves up to 20 documents to another knowledge base of the same workspace; knowledge_base_id is the destination, from knowledge_base.list_move_targets. Runs in the background; follow with knowledge.get_move_progress."
// @Router       /knowledge/move [post]
func (h *KnowledgeHandler) MoveKnowledge(c *gin.Context) {
	ctx := c.Request.Context()

	var req MoveKnowledgeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Error(ctx, "MoveKnowledge: failed to parse request", err)
		_ = c.Error(errors.NewBadRequestError("Invalid request parameters: " + err.Error()))
		return
	}

	// Validate source != target
	if req.SourceKBID == req.TargetKBID {
		_ = c.Error(errors.NewBadRequestError("Source and target knowledge base cannot be the same"))
		return
	}

	callerTenantID := c.GetUint64(types.TenantIDContextKey.String())
	if callerTenantID == 0 {
		_ = c.Error(errors.NewUnauthorizedError("Unauthorized"))
		return
	}
	if err := requireTenantAPIKeyKnowledgeBases(ctx, req.SourceKBID, req.TargetKBID); err != nil {
		_ = c.Error(err)
		return
	}

	// Validate source KB
	sourceKB, err := h.kbService.GetKnowledgeBaseByID(ctx, req.SourceKBID)
	if err != nil {
		if goerrors.Is(err, types.ErrKnowledgeBaseNotFound) {
			_ = c.Error(errors.NewNotFoundError("Source knowledge base not found"))
			return
		}
		_ = c.Error(errors.NewInternalServerError(err.Error()))
		return
	}
	if sourceKB.TenantID != callerTenantID {
		_ = c.Error(errors.NewForbiddenError("No permission to access source knowledge base"))
		return
	}
	if err := h.requireKBOwnershipOrAdmin(c, req.SourceKBID); err != nil {
		_ = c.Error(err)
		return
	}

	// Validate target KB
	targetKB, err := h.kbService.GetKnowledgeBaseByID(ctx, req.TargetKBID)
	if err != nil {
		if goerrors.Is(err, types.ErrKnowledgeBaseNotFound) {
			_ = c.Error(errors.NewNotFoundError("Target knowledge base not found"))
			return
		}
		_ = c.Error(errors.NewInternalServerError(err.Error()))
		return
	}
	if targetKB.TenantID != callerTenantID {
		_ = c.Error(errors.NewForbiddenError("No permission to access target knowledge base"))
		return
	}
	if err := h.requireKBOwnershipOrAdmin(c, req.TargetKBID); err != nil {
		_ = c.Error(err)
		return
	}

	// Resolve explicit write grants independently for both body IDs.
	if _, err := resolveHandlerKBAccessFor(c, req.SourceKBID, h.kbService, nil, nil, types.OrgRoleEditor); err != nil {
		_ = c.Error(err)
		return
	}
	if _, err := resolveHandlerKBAccessFor(c, req.TargetKBID, h.kbService, nil, nil, types.OrgRoleEditor); err != nil {
		_ = c.Error(err)
		return
	}
	taskID := utils.GenerateTaskID("kg_move", callerTenantID, req.SourceKBID)
	ctx, err = access.WithKBTransfer(c.Request.Context(), sourceKB, targetKB, access.KBTransferMove, taskID, false)
	if err != nil {
		_ = c.Error(kbAccessHTTPError(err))
		return
	}
	tenant, _ := ctx.Value(types.TenantInfoContextKey).(*types.Tenant)
	if err := access.ValidateKBTransferCompatibility(sourceKB,
		targetKB,
		access.KBTransferMove,
		req.Mode,
		tenant); err != nil {
		_ = c.Error(errors.NewBadRequestError(err.Error()))
		return
	}
	uniqueIDs := make([]string, 0, len(req.KnowledgeIDs))
	seen := make(map[string]bool, len(req.KnowledgeIDs))
	for _, id := range req.KnowledgeIDs {
		if strings.TrimSpace(id) == "" {
			_ = c.Error(errors.NewBadRequestError("Knowledge ID cannot be empty"))
			return
		}
		if !seen[id] {
			seen[id] = true
			uniqueIDs = append(uniqueIDs, id)
		}
	}
	req.KnowledgeIDs = uniqueIDs

	// Validate all knowledge IDs belong to source KB and are in completed status
	for _, kID := range req.KnowledgeIDs {
		knowledge, err := h.kgService.GetKnowledgeByID(ctx, kID)
		if err != nil {
			_ = c.Error(errors.NewBadRequestError(fmt.Sprintf("Knowledge item %s not found", kID)))
			return
		}
		if knowledge == nil || knowledge.ID != kID || knowledge.TenantID != callerTenantID ||
			knowledge.KnowledgeBaseID != req.SourceKBID {
			_ = c.Error(
				errors.NewBadRequestError(
					fmt.Sprintf("Knowledge item %s does not belong to the source knowledge base", kID),
				),
			)
			return
		}
		if knowledge.ParseStatus != types.ParseStatusCompleted {
			_ = c.Error(errors.NewBadRequestError(
				fmt.Sprintf("Knowledge item %s is not in completed status (current: %s)", kID, knowledge.ParseStatus),
			))
			return
		}
	}

	// Create move payload
	payload := types.KnowledgeMovePayload{
		TenantID:     callerTenantID,
		TaskID:       taskID,
		KnowledgeIDs: req.KnowledgeIDs,
		SourceKBID:   req.SourceKBID,
		TargetKBID:   req.TargetKBID,
		Mode:         req.Mode,
		Initiator:    types.TaskInitiatorFromContext(ctx),
	}
	langfuse.InjectTracing(ctx, &payload)

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		logger.Errorf(ctx, "MoveKnowledge: failed to marshal payload: %v", err)
		_ = c.Error(errors.NewInternalServerError("Failed to create task"))
		return
	}

	// Enqueue move task
	task := asynq.NewTask(types.TypeKnowledgeMove, payloadBytes,
		asynq.TaskID(taskID), asynq.Queue(types.QueueMaintenance),
		asynq.MaxRetry(3), asynq.Timeout(2*time.Hour))
	info, err := h.asynqClient.Enqueue(task)
	if err != nil {
		logger.Errorf(ctx, "MoveKnowledge: failed to enqueue task: %v", err)
		_ = c.Error(errors.NewInternalServerError("Failed to enqueue task"))
		return
	}

	logger.Infof(
		ctx,
		"MoveKnowledge: task enqueued: %s, asynq_id: %s, source: %s, target: %s, count: %d",
		taskID,
		info.ID,
		utils.SanitizeForLog(req.SourceKBID),
		utils.SanitizeForLog(req.TargetKBID),
		len(req.KnowledgeIDs),
	)

	// Save initial progress
	initialProgress := &types.KnowledgeMoveProgress{
		TaskID:     taskID,
		SourceKBID: req.SourceKBID,
		TargetKBID: req.TargetKBID,
		Status:     types.KBCloneStatusPending,
		Total:      len(req.KnowledgeIDs),
		Progress:   0,
		Message:    "Task queued, waiting to start...",
		CreatedAt:  time.Now().Unix(),
		UpdatedAt:  time.Now().Unix(),
	}
	if err := h.kgService.SaveKnowledgeMoveProgress(ctx, initialProgress); err != nil {
		logger.Warnf(ctx, "MoveKnowledge: failed to save initial progress: %v", err)
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": MoveKnowledgeResponse{
			TaskID:         taskID,
			SourceKBID:     req.SourceKBID,
			TargetKBID:     req.TargetKBID,
			KnowledgeCount: len(req.KnowledgeIDs),
			Message:        "Knowledge move task started",
		},
	})
}

// GetKnowledgeMoveProgress retrieves the progress of a knowledge move task.
//
// GetKnowledgeMoveProgress godoc
// @Summary      获取知识移动进度
// @Description  按任务 ID 查询移动进度
// @Tags         知识
// @Produce      json
// @Param        task_id  path      string                       true  "移动任务 ID"  extensions(x-agent)
// @Success      200      {object}  handler.DataResponse{data=types.KnowledgeMoveProgress}  "进度信息"
// @Failure      404      {object}  errors.AppError              "任务不存在"
// @Security     Bearer
// @Security     ApiKeyAuth
// @x-agent      {"id":"knowledge.get_move_progress","risk":"read"}
// @Router       /knowledge/move/progress/{task_id} [get]
func (h *KnowledgeHandler) GetKnowledgeMoveProgress(c *gin.Context) {
	ctx := c.Request.Context()

	taskID := c.Param("task_id")
	if taskID == "" {
		_ = c.Error(errors.NewBadRequestError("Task ID cannot be empty"))
		return
	}
	if err := requireTaskProgressTenant(ctx, taskID); err != nil {
		_ = c.Error(err)
		return
	}

	progress, err := h.kgService.GetKnowledgeMoveProgress(ctx, taskID)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		_ = c.Error(err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    progress,
	})
}

type batchReparseKnowledgeRequest struct {
	// Knowledge base the documents belong to.
	KBID string `json:"kb_id" binding:"required" extensions:"x-agent=knowledge_base_id"`
	// Documents to reparse.
	IDs           []string                         `json:"ids" binding:"required" extensions:"x-agent=knowledge_ids"`
	ProcessConfig *types.KnowledgeProcessOverrides `json:"process_config,omitempty"`
}

// BatchReparseKnowledge godoc
// @Summary      批量重新解析知识
// @Description  按 ID 列表批量重新解析单个知识库下的多个知识条目
// @Tags         知识管理
// @Accept       json
// @Produce      json
// @Param        request  body      batchReparseKnowledgeRequest  true  "批量重解析请求"
// @Success      200      {object}  handler.SubmittedTaskResponse  "任务已提交"
// @Failure      400      {object}  errors.AppError               "请求参数错误"
// @Failure      403      {object}  errors.AppError               "权限不足"
// @Security     Bearer
// @Security     ApiKeyAuth
// @x-agent      {"id":"knowledge.batch_reparse","risk":"write","target":"knowledge_base.get","label":"name"}
// @x-agent-hint "Reparses several documents of one knowledge base in the background; check them afterwards with knowledge.list parse_status. Search results change meanwhile."
// @Router       /knowledge/batch-reparse [post]
func (h *KnowledgeHandler) BatchReparseKnowledge(c *gin.Context) {
	ctx := c.Request.Context()
	var req batchReparseKnowledgeRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Errorf(ctx, "failed to parse batch reparse knowledge request: %v", err)
		_ = c.Error(errors.NewBadRequestError("invalid batch reparse knowledge request parameters"))
		return
	}

	seen := make(map[string]struct{}, len(req.IDs))
	ids := make([]string, 0, len(req.IDs))
	for _, raw := range req.IDs {
		id := strings.TrimSpace(raw)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}

	if len(ids) == 0 {
		_ = c.Error(errors.NewBadRequestError("no knowledge IDs provided for batch reparse"))
		return
	}
	const maxBatch = 200
	if len(ids) > maxBatch {
		_ = c.Error(errors.NewBadRequestError(fmt.Sprintf("too many ids (max %d per batch)", maxBatch)))
		return
	}

	_, kbID, effectiveTenantID, permission, err := h.validateKnowledgeBaseWriteAccessWithKBID(c, req.KBID)
	if err != nil {
		_ = c.Error(err)
		return
	}
	if permission != types.OrgRoleAdmin && permission != types.OrgRoleEditor {
		_ = c.Error(errors.NewForbiddenError("no permission to reparse knowledge in this kb"))
		return
	}
	if err := h.requireKBOwnershipOrAdmin(c, kbID); err != nil {
		_ = c.Error(err)
		return
	}
	ctx = types.WithExecutionTenant(c.Request.Context(), effectiveTenantID)

	knowledgeList, err := h.kgService.GetKnowledgeBatch(ctx, effectiveTenantID, ids)
	if err != nil {
		logger.Errorf(ctx, "failed to get knowledge batch, kb_id: %s, size: %d, err: %v", kbID, len(ids), err)
		_ = c.Error(errors.NewInternalServerError("failed to get knowledge batch"))
		return
	}
	if len(knowledgeList) != len(ids) {
		_ = c.Error(errors.NewBadRequestError("some knowledge entries were not found"))
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

	taskID, err := h.enqueueKnowledgeListReparse(ctx, effectiveTenantID, kbID, ids, req.ProcessConfig)
	if err != nil {
		logger.Errorf(ctx, "Failed to enqueue batch knowledge reparse task: %v", err)
		_ = c.Error(errors.NewInternalServerError("Failed to enqueue batch reparse task"))
		return
	}

	logger.Infof(ctx, "Batch knowledge reparse task enqueued: %s, kb_id: %s, count: %d",
		taskID, utils.SanitizeForLog(kbID), len(ids))

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Batch reparse task submitted",
		"data": gin.H{
			"task_id":       taskID,
			"reparse_count": len(ids),
		},
	})
}

// RebuildKnowledgeBaseIndex godoc
// @Summary      重建整个知识库的索引
// @Description  索引策略变更后，把知识库下全部文档提交重新解析。前端知识库设置弹窗
// @Description  (KnowledgeBaseEditorModal) 在保存新策略后调用此接口，此前该路由在后端缺失，调用返回 404。
// @Tags         知识管理
// @Produce      json
// @Param        id   path      string  true  "知识库ID"
// @Success      200  {object}  handler.DataResponse  "任务已提交"
// @Failure      400  {object}  errors.AppError       "请求参数错误"
// @Failure      403  {object}  errors.AppError       "权限不足"
// @Failure      404  {object}  errors.AppError       "知识库不存在"
// @Failure      409  {object}  errors.AppError       "有文档正在迁移中"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases/{id}/rebuild-index [post]
func (h *KnowledgeHandler) RebuildKnowledgeBaseIndex(c *gin.Context) {
	kbID := strings.TrimSpace(c.Param("id"))
	if kbID == "" {
		_ = c.Error(errors.NewBadRequestError("knowledge base id is required"))
		return
	}

	_, _, effectiveTenantID, permission, err := h.validateKnowledgeBaseWriteAccessWithKBID(c, kbID)
	if err != nil {
		_ = c.Error(err)
		return
	}
	if permission != types.OrgRoleAdmin && permission != types.OrgRoleEditor {
		_ = c.Error(errors.NewForbiddenError("no permission to rebuild index of this knowledge base"))
		return
	}
	if err := h.requireKBOwnershipOrAdmin(c, kbID); err != nil {
		_ = c.Error(err)
		return
	}
	ctx := types.WithExecutionTenant(c.Request.Context(), effectiveTenantID)

	knowledges, err := h.kgService.ListKnowledgeByKnowledgeBaseID(ctx, kbID)
	if err != nil {
		logger.Errorf(ctx, "failed to list knowledge for rebuild-index, kb_id: %s, err: %v",
			utils.SanitizeForLog(kbID), err)
		_ = c.Error(errors.NewInternalServerError("failed to list knowledge of the knowledge base"))
		return
	}
	ids := make([]string, 0, len(knowledges))
	for _, k := range knowledges {
		if k == nil || strings.TrimSpace(k.ID) == "" {
			continue
		}
		// Same admission rule as /knowledge/batch-reparse: fail visibly
		// instead of silently dropping a document that is mid-move.
		if err := access.RejectMovingKnowledge(k); err != nil {
			_ = c.Error(err)
			return
		}
		ids = append(ids, k.ID)
	}
	if len(ids) == 0 {
		_ = c.Error(errors.NewBadRequestError("no documents to rebuild in this knowledge base"))
		return
	}

	// Keep the per-task admission cap of /knowledge/batch-reparse.
	const maxBatch = 200
	taskIDs := make([]string, 0, (len(ids)+maxBatch-1)/maxBatch)
	for start := 0; start < len(ids); start += maxBatch {
		end := start + maxBatch
		if end > len(ids) {
			end = len(ids)
		}
		taskID, err := h.enqueueKnowledgeListReparse(ctx, effectiveTenantID, kbID, ids[start:end], nil)
		if err != nil {
			logger.Errorf(ctx, "failed to enqueue rebuild-index task, kb_id: %s, err: %v",
				utils.SanitizeForLog(kbID), err)
			_ = c.Error(errors.NewInternalServerError("failed to enqueue rebuild index task"))
			return
		}
		taskIDs = append(taskIDs, taskID)
	}

	logger.Infof(ctx, "Rebuild index tasks enqueued: kb_id: %s, documents: %d, tasks: %d",
		utils.SanitizeForLog(kbID), len(ids), len(taskIDs))

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Rebuild index tasks submitted",
		"data": gin.H{
			"document_count": len(ids),
			"task_ids":       taskIDs,
		},
	})
}
