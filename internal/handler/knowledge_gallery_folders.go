package handler

import (
	"context"
	"encoding/json"
	goerrors "errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/Usir233/GENYEE/internal/errors"
	"github.com/Usir233/GENYEE/internal/logger"
	"github.com/Usir233/GENYEE/internal/types"
	"github.com/Usir233/GENYEE/internal/utils"
	"github.com/gin-gonic/gin"
)

// ListImages lists the image assets of a knowledge base for the gallery view.
//
// @Summary      列出知识库图片资产
// @Description  浏览某知识库内的全部图片资产，支持关键字搜索、按图片属性筛选与排序分页。
// @Tags         knowledge
// @Accept       json
// @Produce      json
// @Param        id          path      string  true  "知识库 ID"
// @Param        page        query     int     false "页码，默认 1"
// @Param        page_size   query     int     false "每页数量，默认 20，最大 1000"
// @Param        keyword     query     string  false "关键字，对 search_in 指定字段做不区分大小写的子串匹配"
// @Param        search_in   query     string  false "参与搜索的属性 ID（逗号分隔）；须为 in_searchfield=true"
// @Param        sort_by     query     string  false "排序属性 ID；须为 in_sortfield=true，默认 created_at"
// @Param        sort_order  query     string  false "排序方向：asc / desc，默认 desc"
// @Param        is_enabled  query     bool    false "仅包含启用状态的图片"
// @Param        knowledge_ids query  string  false "来源文档 ID 列表（逗号分隔，最多 50 个），仅返回这些文档的图片"
// @Param        attr_filters query    string  false "属性筛选 JSON，同一属性多值 OR、属性间 AND"
// @Param        attr_rules  query     string  false "属性逐值裁决 JSON（off 隐藏 / on 强制显示，on 优先）"
// @Success      200  {object}  handler.PageResponse{data=[]types.ImageAsset}  "图片资产分页列表"
// @Failure      400  {object}  errors.AppError         "请求参数错误"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases/{id}/images [get]
func (h *KnowledgeHandler) ListImages(c *gin.Context) {
	_, kbID, effectiveTenantID, _, err := h.validateKnowledgeBaseAccess(c)
	if err != nil {
		_ = c.Error(err)
		return
	}
	ctx := types.WithExecutionTenant(c.Request.Context(), effectiveTenantID)

	var pagination types.Pagination
	if err := c.ShouldBindQuery(&pagination); err != nil {
		logger.Error(ctx, "Failed to parse pagination parameters", err)
		_ = c.Error(errors.NewBadRequestError(err.Error()))
		return
	}

	filter := &types.ImageListFilter{
		Keyword:   strings.TrimSpace(c.Query("keyword")),
		SortBy:    strings.TrimSpace(c.Query("sort_by")),
		SortOrder: c.DefaultQuery("sort_order", "desc"),
	}
	if v := c.Query("is_enabled"); v != "" {
		enabled := v == "true"
		filter.IsEnabled = &enabled
	}

	// knowledge_ids: comma-separated knowledge item ids the gallery should
	// restrict itself to (the "filter by source document" picker). Order is
	// irrelevant; the cap keeps a hand-rolled caller from sending an
	// unbounded URL. The POST variant lifts the cap via a JSON body.
	if raw := strings.TrimSpace(c.Query("knowledge_ids")); raw != "" {
		var ids []string
		for _, id := range strings.Split(raw, ",") {
			if id = strings.TrimSpace(id); id != "" {
				ids = append(ids, id)
			}
			if len(ids) >= maxKnowledgeIDFilter {
				break
			}
		}
		filter.KnowledgeIDs = ids
	}

	// Attribute filters / rules / search fields arrive as raw JSON objects
	// keyed by namespaced attribute id; eligibility against the resolved
	// contract is enforced by constrainGalleryFilter below.
	if raw := c.Query("attr_filters"); raw != "" {
		var attrFilters map[string][]string
		if err := json.Unmarshal([]byte(raw), &attrFilters); err == nil && len(attrFilters) > 0 {
			filter.AttrFilters = attrFilters
		}
	}
	if raw := c.Query("attr_rules"); raw != "" {
		var attrRules map[string]map[string]string
		if err := json.Unmarshal([]byte(raw), &attrRules); err == nil && len(attrRules) > 0 {
			filter.AttrRules = attrRules
		}
	}

	// search_in: comma-separated namespaced attribute ids; ineligible ids
	// are dropped, and an empty remainder falls back to the service default
	// (builtin caption + ocr_text).
	if raw := strings.TrimSpace(c.Query("search_in")); raw != "" {
		for _, id := range strings.Split(raw, ",") {
			if id = strings.TrimSpace(id); id != "" {
				filter.SearchIn = append(filter.SearchIn, id)
			}
		}
	}

	h.constrainGalleryFilter(ctx, kbID, filter)

	result, err := h.chunkService.ListImagesByKnowledgeBaseID(ctx, kbID, &pagination, filter)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{"kb_id": kbID})
		_ = c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success":   true,
		"data":      result.Data,
		"total":     result.Total,
		"page":      result.Page,
		"page_size": result.PageSize,
	})
}

// constrainGalleryFilter prunes a client-supplied gallery filter to the KB's
// resolved contract: a client may only search / sort / filter on fields whose
// usage flags allow it, so stale or hand-rolled callers cannot smuggle in
// fields the configuration no longer serves. The GET (query params) and POST
// (JSON body) handlers share it; the filter carries raw values on entry.
func (h *KnowledgeHandler) constrainGalleryFilter(ctx context.Context, kbID string, filter *types.ImageListFilter) {
	resolved := h.resolveGalleryConfig(ctx, kbID)
	filterable, searchable, sortable := galleryEligibility(resolved)

	if len(filter.AttrFilters) > 0 {
		kept := make(map[string][]string, len(filter.AttrFilters))
		for id, values := range filter.AttrFilters {
			if filterable[id] && len(values) > 0 {
				kept[id] = values
			}
		}
		filter.AttrFilters = nil
		if len(kept) > 0 {
			filter.AttrFilters = kept
		}
	}
	if len(filter.AttrRules) > 0 {
		kept := make(map[string]map[string]string, len(filter.AttrRules))
		for id, verdicts := range filter.AttrRules {
			if !filterable[id] || len(verdicts) == 0 {
				continue
			}
			clean := make(map[string]string, len(verdicts))
			for value, verdict := range verdicts {
				switch verdict {
				case "off", "on":
					clean[value] = verdict
				}
			}
			if len(clean) > 0 {
				kept[id] = clean
			}
		}
		filter.AttrRules = nil
		if len(kept) > 0 {
			filter.AttrRules = kept
		}
	}
	if len(filter.SearchIn) > 0 {
		kept := make([]string, 0, len(filter.SearchIn))
		for _, id := range filter.SearchIn {
			if searchable[id] {
				kept = append(kept, id)
			}
		}
		filter.SearchIn = kept // empty falls back to the service default
	}
	// An ineligible sort falls back to the service default (builtin
	// created_at, descending).
	if filter.SortBy != "" && !sortable[filter.SortBy] {
		filter.SortBy = ""
	}
}

// imageListRequestBody carries the same parameters as the GET /images query,
// as JSON. knowledge_ids moved into the body lifts the URL-length cap that
// keeps the GET variant to a handful of documents, so the source picker's
// select-all can name every document of a large KB.
type imageListRequestBody struct {
	types.Pagination
	Keyword      string                       `json:"keyword"`
	SearchIn     []string                     `json:"search_in"`
	SortBy       string                       `json:"sort_by"`
	SortOrder    string                       `json:"sort_order"`
	IsEnabled    *bool                        `json:"is_enabled"`
	KnowledgeIDs []string                     `json:"knowledge_ids"`
	AttrFilters  map[string][]string          `json:"attr_filters"`
	AttrRules    map[string]map[string]string `json:"attr_rules"`
}

// ListImagesPost lists image assets like ListImages, with the filter arriving
// as a JSON body.
//
// @Summary      列出知识库图片资产（body 版）
// @Description  与 GET /images 同义，筛选参数经 JSON body 传递（knowledge_ids 上限 1000）。
// @Tags         knowledge
// @Accept       json
// @Produce      json
// @Param        id    path      string              true  "知识库 ID"
// @Param        body  body      imageListRequestBody false "筛选参数（与 GET /images 同名参数同义）"
// @Success      200  {object}  handler.PageResponse{data=[]types.ImageAsset}  "图片资产分页列表"
// @Failure      400  {object}  errors.AppError         "请求参数错误"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases/{id}/images [post]
func (h *KnowledgeHandler) ListImagesPost(c *gin.Context) {
	_, kbID, effectiveTenantID, _, err := h.validateKnowledgeBaseAccess(c)
	if err != nil {
		_ = c.Error(err)
		return
	}
	ctx := types.WithExecutionTenant(c.Request.Context(), effectiveTenantID)

	var body imageListRequestBody
	if err := c.ShouldBindJSON(&body); err != nil && !goerrors.Is(err, io.EOF) {
		logger.Error(ctx, "Failed to parse image list body", err)
		_ = c.Error(errors.NewBadRequestError(err.Error()))
		return
	}
	if len(body.KnowledgeIDs) > maxKnowledgeIDFilterBody {
		_ = c.Error(errors.NewBadRequestError(
			fmt.Sprintf("knowledge_ids exceeds the cap of %d", maxKnowledgeIDFilterBody)))
		return
	}

	ids := make([]string, 0, len(body.KnowledgeIDs))
	for _, id := range body.KnowledgeIDs {
		if id = strings.TrimSpace(id); id != "" {
			ids = append(ids, id)
		}
	}
	sortOrder := body.SortOrder
	if sortOrder == "" {
		sortOrder = "desc"
	}
	filter := &types.ImageListFilter{
		Keyword:      strings.TrimSpace(body.Keyword),
		SearchIn:     body.SearchIn,
		SortBy:       strings.TrimSpace(body.SortBy),
		SortOrder:    sortOrder,
		IsEnabled:    body.IsEnabled,
		KnowledgeIDs: ids,
		AttrFilters:  body.AttrFilters,
		AttrRules:    body.AttrRules,
	}

	h.constrainGalleryFilter(ctx, kbID, filter)

	result, err := h.chunkService.ListImagesByKnowledgeBaseID(ctx, kbID, &body.Pagination, filter)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{"kb_id": kbID})
		_ = c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success":   true,
		"data":      result.Data,
		"total":     result.Total,
		"page":      result.Page,
		"page_size": result.PageSize,
	})
}

// galleryEligibility extracts the per-attribute usage sets that incoming
// query parameters are validated against, keyed by namespaced attribute id.
func galleryEligibility(resolved *types.GalleryResolvedConfig) (filterable, searchable, sortable map[string]bool) {
	filterable = map[string]bool{}
	searchable = map[string]bool{}
	sortable = map[string]bool{}
	if resolved == nil {
		return
	}
	for _, attr := range resolved.Attributes {
		if attr.Usage.InFilter {
			filterable[attr.ID] = true
		}
		if attr.Usage.InSearchField {
			searchable[attr.ID] = true
		}
		if attr.Usage.InSortField {
			sortable[attr.ID] = true
		}
	}
	return
}

// resolveGalleryConfig merges the gallery configuration tiers for one
// request: the system tier from system_settings ("gallery.policy"), the user
// tier from the caller's saved preferences. The KB tier slot is reserved for
// the per-KB config (not wired yet — the merge engine already accepts it).
// A missing or malformed tier degrades to "no overrides", never to an error:
// the gallery must render even on a half-configured deployment.
func (h *KnowledgeHandler) resolveGalleryConfig(ctx context.Context, kbID string) *types.GalleryResolvedConfig {
	var systemTier *types.GalleryPolicyTier
	if h.systemSettingSvc != nil {
		if row, err := h.systemSettingSvc.Get(ctx, "gallery.policy"); err == nil && row != nil {
			if raw, err := row.AsString(); err == nil && strings.TrimSpace(raw) != "" {
				tier := &types.GalleryPolicyTier{}
				if err := json.Unmarshal([]byte(raw), tier); err == nil {
					systemTier = tier
				} else {
					logger.Warnf(ctx, "gallery.policy is not valid JSON; ignoring: %v", err)
				}
			}
		}
	}

	var userTier *types.GalleryPolicyTier
	if h.userSvc != nil {
		if userID, ok := types.UserIDFromContext(ctx); ok && userID != "" {
			if u, err := h.userSvc.GetUserByID(ctx, userID); err == nil && u != nil && u.Preferences.Gallery != nil {
				g := u.Preferences.Gallery
				userTier = &types.GalleryPolicyTier{Mode: g.Mode, Status: g.Status}
			}
		}
	}

	return types.ResolveGalleryConfig(kbID, systemTier, nil, userTier)
}

// GetGalleryConfig serves the gallery's self-describing contract: resolved
// attribute sources, attribute definitions/usage, and search activation state.
//
// GetGalleryConfig godoc
// @Summary      获取知识库图库配置
// @Description  返回知识库解析后的图库属性来源、属性定义及使用策略、检索模式与各字段状态。
// @Tags         knowledge
// @Accept       json
// @Produce      json
// @Param        id   path      string  true  "知识库 ID"
// @Success      200  {object}  handler.DataResponse  "图库配置"
// @Failure      403  {object}  errors.AppError       "无权访问知识库"
// @Failure      404  {object}  errors.AppError       "知识库不存在"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases/{id}/gallery-config [get]
func (h *KnowledgeHandler) GetGalleryConfig(c *gin.Context) {
	ctx := c.Request.Context()

	_, kbID, effectiveTenantID, _, err := h.validateKnowledgeBaseAccess(c)
	if err != nil {
		_ = c.Error(err)
		return
	}
	ctx = types.WithExecutionTenant(ctx, effectiveTenantID)

	resolved := h.resolveGalleryConfig(ctx, kbID)
	status := resolved.Status
	if status == nil {
		status = map[string]string{}
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"attribute_sources": resolved.AttributeSources,
			"attributes":        resolved.Attributes,
			"mode":              resolved.Mode,
			"status":            status,
		},
	})
}

// ListKnowledgeFolders godoc
// @Summary      获取知识库文件夹目录树
// @Description  返回知识库内由文件夹上传形成的目录树，包含每个文件夹的直接文档数与含子目录的总数
// @Tags         知识管理
// @Accept       json
// @Produce      json
// @Param        id   path      string  true  "知识库ID"  extensions(x-agent=knowledge_base_id)
// @Success      200  {object}  handler.DataResponse{data=types.KnowledgeFolderTree}  "目录树"
// @Failure      400  {object}  errors.AppError         "请求参数错误"
// @Security     Bearer
// @Security     ApiKeyAuth
// @x-agent      {"id":"knowledge.list_folders","risk":"read","root":"data.folders","max_items":200}
// @x-agent-hint "Top-level document folders, up to 200. meta.root_document_count is documents with no folder and meta.total_document_count is every document. Sub-folders are not listed; filter knowledge.list by folder_path to look inside one."
// @Router       /knowledge-bases/{id}/knowledge/folders [get]
func (h *KnowledgeHandler) ListKnowledgeFolders(c *gin.Context) {
	// Read access mirrors ListKnowledge so the sidebar tree is available to
	// every viewer of a shared knowledge base.
	_, kbID, effectiveTenantID, _, err := h.validateKnowledgeBaseAccess(c)
	if err != nil {
		_ = c.Error(err)
		return
	}
	ctx := types.WithExecutionTenant(c.Request.Context(), effectiveTenantID)

	tree, err := h.kgService.ListKnowledgeFolderTree(ctx, kbID)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		_ = c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	logger.Infof(ctx, "Knowledge folder tree retrieved, kb_id=%s folders=%d",
		utils.SanitizeForLog(kbID), len(tree.Folders))
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    tree,
	})
}

// MoveKnowledgeToFolderRequest is the body schema for POST /knowledge/folder.
type MoveKnowledgeToFolderRequest struct {
	// Knowledge base the documents belong to.
	KBID string `json:"kb_id" binding:"required" extensions:"x-agent=knowledge_base_id"`
	// Documents to move.
	IDs []string `json:"knowledge_ids" binding:"required" extensions:"x-agent"`
	// Target folder path; empty (allowed) means the knowledge base top level.
	FolderPath string `json:"folder_path" extensions:"x-agent"`
}

// MoveKnowledgeToFolder godoc
// @Summary      移动知识到文件夹
// @Description  批量修改知识条目所属文件夹；目标路径不存在时自动创建，空路径表示顶层，不重新解析文档。
// @Tags         知识管理
// @Accept       json
// @Produce      json
// @Param        request  body      MoveKnowledgeToFolderRequest  true  "移动请求"
// @Success      200      {object}  handler.DataResponse{data=handler.FolderMoveData}  "移动成功"
// @Failure      400      {object}  errors.AppError               "请求参数错误"
// @Failure      403      {object}  errors.AppError               "权限不足"
// @Security     Bearer
// @Security     ApiKeyAuth
// @x-agent      {"id":"knowledge.move_to_folder","risk":"write","target":"knowledge_base.get","label":"name"}
// @x-agent-hint "Files documents of one knowledge base under a folder path such as docs/spec, created on demand; an empty path moves them back to the top level. Nothing is reparsed."
// @Router       /knowledge/folder [post]
func (h *KnowledgeHandler) MoveKnowledgeToFolder(c *gin.Context) {
	var req MoveKnowledgeToFolderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(errors.NewBadRequestError("Invalid request parameters: " + err.Error()))
		return
	}

	ids := dedupeKnowledgeIDs(req.IDs)
	if len(ids) == 0 {
		_ = c.Error(errors.NewBadRequestError("knowledge_ids cannot be empty"))
		return
	}
	const maxBatch = 200
	if len(ids) > maxBatch {
		_ = c.Error(errors.NewBadRequestError(fmt.Sprintf("too many ids (max %d per batch)", maxBatch)))
		return
	}

	kbID, effectiveTenantID, err := h.requireKnowledgeWriteAccess(c, req.KBID)
	if err != nil {
		_ = c.Error(err)
		return
	}
	ctx := types.WithExecutionTenant(c.Request.Context(), effectiveTenantID)

	// Guard against cross-KB moves: the service layer scopes by tenant, so the
	// handler must confirm every entry belongs to the requested knowledge base.
	if err := h.requireKnowledgeInKB(ctx, effectiveTenantID, kbID, ids); err != nil {
		_ = c.Error(err)
		return
	}

	affected, err := h.kgService.MoveKnowledgeToFolder(ctx, kbID, ids, req.FolderPath)
	if err != nil {
		if appErr, ok := errors.IsAppError(err); ok {
			_ = c.Error(appErr)
			return
		}
		logger.ErrorWithFields(ctx, err, nil)
		_ = c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"moved_count": affected,
			"folder_path": types.NormalizeKnowledgeFolderPath(req.FolderPath),
		},
	})
}

// RenameKnowledgeFolderRequest is the body schema for
// PUT /knowledge-bases/:id/knowledge/folders.
type RenameKnowledgeFolderRequest struct {
	// Current folder path.
	From string `json:"from" binding:"required" extensions:"x-agent=from_path"`
	// New folder path.
	To string `json:"to"   binding:"required" extensions:"x-agent=to_path"`
}

// RenameKnowledgeFolder godoc
// @Summary      重命名或移动文件夹
// @Description  把一个文件夹及其所有子目录改到新路径。目标路径已存在时两个文件夹合并；不能移动到自身子目录下
// @Tags         知识管理
// @Accept       json
// @Produce      json
// @Param        id       path      string                        true  "知识库ID"  extensions(x-agent=knowledge_base_id)
// @Param        request  body      RenameKnowledgeFolderRequest  true  "重命名请求"
// @Success      200      {object}  handler.DataResponse{data=handler.FolderMoveData}  "重命名成功"
// @Failure      400      {object}  errors.AppError               "请求参数错误"
// @Failure      403      {object}  errors.AppError               "权限不足"
// @Security     Bearer
// @Security     ApiKeyAuth
// @x-agent      {"id":"knowledge.rename_folder","risk":"write","target":"knowledge_base.get","label":"name"}
// @x-agent-hint "Moves a document folder and everything under it to a new path; an existing path is merged into."
// @Router       /knowledge-bases/{id}/knowledge/folders [put]
func (h *KnowledgeHandler) RenameKnowledgeFolder(c *gin.Context) {
	var req RenameKnowledgeFolderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(errors.NewBadRequestError("Invalid request parameters: " + err.Error()))
		return
	}

	_, kbID, effectiveTenantID, permission, err := h.validateKnowledgeBaseWriteAccessWithKBID(c, c.Param("id"))
	if err != nil {
		_ = c.Error(err)
		return
	}
	if permission != types.OrgRoleAdmin && permission != types.OrgRoleEditor {
		_ = c.Error(errors.NewForbiddenError("No permission to modify knowledge"))
		return
	}
	if err := h.requireKBOwnershipOrAdmin(c, kbID); err != nil {
		_ = c.Error(err)
		return
	}
	ctx := types.WithExecutionTenant(c.Request.Context(), effectiveTenantID)

	affected, err := h.kgService.RenameKnowledgeFolder(ctx, kbID, req.From, req.To)
	if err != nil {
		if appErr, ok := errors.IsAppError(err); ok {
			_ = c.Error(appErr)
			return
		}
		logger.ErrorWithFields(ctx, err, nil)
		_ = c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"moved_count": affected,
			"folder_path": types.NormalizeKnowledgeFolderPath(req.To),
		},
	})
}
