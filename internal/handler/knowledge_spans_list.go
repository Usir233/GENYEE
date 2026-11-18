package handler

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Usir233/GENYEE/internal/application/access"
	"github.com/Usir233/GENYEE/internal/errors"
	"github.com/Usir233/GENYEE/internal/logger"
	"github.com/Usir233/GENYEE/internal/middleware"
	"github.com/Usir233/GENYEE/internal/types"
	"github.com/Usir233/GENYEE/internal/utils"
	"github.com/gin-gonic/gin"
)

// GetKnowledgeSpans godoc
// @Summary      获取知识文档解析的 Span 树（含历史尝试）
// @Description  返回该知识在解析流水线的 trace tree（root → stage → subspan）：每段状态、耗时、错误码与追踪 ID；支持 ?attempt=N 查看历史尝试。
// @Tags         知识管理
// @Accept       json
// @Produce      json
// @Param        id        path   string  true   "知识ID"  extensions(x-agent=knowledge_id)
// @Param        attempt   query  int     false  "指定尝试号；省略或非正数=最新"  minimum(1)
// @Success      200       {object}  handler.DataResponse{data=handler.KnowledgeSpansData}
// @Failure      403       {object}  errors.AppError  "无权访问所属知识库"
// @Failure      404       {object}  errors.AppError  "知识不存在"
// @Security     Bearer
// @Security     ApiKeyAuth
// @x-agent      {"id":"knowledge.get_parse_progress","risk":"read"}
// @x-agent-hint "Use when a document is stuck or failed: tells a stalled parse from a running one, names the failed stage."
// @Router       /knowledge/{id}/spans [get]
// /knowledge/{id}/stages is registered as a pure alias of this handler
// (issue #1909). It is deliberately NOT annotated as a second @Router:
// swag would copy the x-agent block above onto the alias path, and the
// agent operations catalog enforces global uniqueness of operation IDs,
// so annotating it would make server startup fail with
// operation "knowledge.get_parse_progress" declared twice.
//
// Always returns the canonical 5-stage timeline; missing stage rows are
// synthesized as "pending" so the frontend timeline always renders five
// segments. Subspans (multimodal.image[i], generation.*) ride along under
// each stage as children when present.
func (h *KnowledgeHandler) GetKnowledgeSpans(c *gin.Context) {
	ctx := c.Request.Context()

	id := utils.SanitizeForLog(c.Param("id"))
	if id == "" {
		_ = c.Error(errors.NewBadRequestError("Knowledge ID cannot be empty"))
		return
	}

	knowledge, _, err := h.resolveKnowledgeAndValidateKBAccess(c, id, types.OrgRoleViewer)
	if err != nil {
		_ = c.Error(err)
		return
	}

	// Pick attempt: explicit ?attempt=N wins; otherwise pull the
	// latest attempt from the spans table. Lite-mode / fresh installs
	// with zero rows fall through to attempt=0, in which case we
	// return a placeholder tree (5 pending stages, no root, no
	// children) so the UI still renders.
	requestedAttempt := 0
	if v := strings.TrimSpace(c.Query("attempt")); v != "" {
		if n, perr := strconv.Atoi(v); perr == nil && n > 0 {
			requestedAttempt = n
		}
	}

	rows := []types.KnowledgeProcessingSpan{}
	currentAttempt := 0
	latestAttempt := 0
	if h.kgService != nil {
		latest, lerr := h.kgService.GetLatestProcessingAttempt(ctx, knowledge.ID)
		if lerr != nil {
			logger.Warnf(ctx, "spans LatestAttempt failed for %s: %v", knowledge.ID, lerr)
		} else {
			latestAttempt = latest
		}
		if requestedAttempt > 0 {
			currentAttempt = requestedAttempt
		} else {
			currentAttempt = latestAttempt
		}
		if currentAttempt > 0 {
			rows, err = h.kgService.ListProcessingSpansByAttempt(ctx, knowledge.ID, currentAttempt)
			if err != nil {
				logger.Warnf(ctx, "spans ListByAttempt failed kid=%s attempt=%d: %v",
					knowledge.ID, currentAttempt, err)
				rows = nil
			}
		}
	}

	// Build tree: index by SpanID, then attach to parents. Stages
	// missing from the DB are synthesized as "pending" placeholders
	// under a synthetic (or real, if present) root so the timeline
	// always renders five segments. parse_status threads through so
	// pre-tracker historical knowledge (no rows but parse_status is
	// already terminal) renders as done/failed instead of pending —
	// otherwise legacy completed documents would forever look like
	// they're still waiting in the queue.
	tree, currentStageName, lastErr := buildSpanTree(knowledge.ID, currentAttempt, rows, knowledge.ParseStatus)

	resp := KnowledgeSpansData{
		KnowledgeID:    knowledge.ID,
		Attempt:        currentAttempt,
		LatestAttempt:  latestAttempt,
		ParseStatus:    knowledge.ParseStatus,
		CurrentAttempt: currentAttempt,
		CurrentStage:   currentStageName,
		Trace:          tree,
	}
	if isParseInFlight(knowledge.ParseStatus) {
		last := spansLastActivity(knowledge.UpdatedAt, rows)
		resp.LastActivityAt = &last
		if time.Since(last) >= stallHintAfter {
			resp.StallState = h.stallVerdicts(ctx, []string{knowledge.ID})[knowledge.ID]
		}
	}
	resp.LastError = knowledgeSpansLastError(
		currentAttempt,
		latestAttempt,
		knowledge.ParseStatus,
		knowledge.ErrorMessage,
		knowledge.UpdatedAt,
		lastErr,
	)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    resp,
	})
}

// GetKnowledgeStages godoc
// @Summary      获取知识文档解析阶段状态（/spans 兼容别名）
// @Description  与 GET /knowledge/{id}/spans 返回结构一致的只读别名接口，不重复声明 x-agent 操作 ID。
// @Tags         知识管理
// @Accept       json
// @Produce      json
// @Param        id        path   string  true   "知识ID"
// @Param        attempt   query  int     false  "指定尝试号；省略或非正数=最新"  minimum(1)
// @Success      200       {object}  handler.DataResponse{data=handler.KnowledgeSpansData}
// @Failure      403       {object}  errors.AppError  "无权访问所属知识库"
// @Failure      404       {object}  errors.AppError  "知识不存在"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge/{id}/stages [get]
func (h *KnowledgeHandler) GetKnowledgeStages(c *gin.Context) {
	h.GetKnowledgeSpans(c)
}

// KnowledgeSpansData is the data object of GET /knowledge/{id}/spans.
type KnowledgeSpansData struct {
	KnowledgeID string `json:"knowledge_id" extensions:"x-agent"`
	// Attempt is the attempt the trace describes; equals CurrentAttempt.
	Attempt        int    `json:"attempt" extensions:"x-agent"`
	LatestAttempt  int    `json:"latest_attempt" extensions:"x-agent"`
	ParseStatus    string `json:"parse_status" extensions:"x-agent"`
	CurrentAttempt int    `json:"current_attempt"`
	// CurrentStage is the stage running now, or the last one that ran.
	CurrentStage string              `json:"current_stage" extensions:"x-agent"`
	Trace        *types.SpanTreeNode `json:"trace"`
	// LastActivityAt is set while the parse is in flight.
	LastActivityAt *time.Time `json:"last_activity_at,omitempty" extensions:"x-agent"`
	// StallState is types.StallStateQueued or types.StallStateStalled when an
	// in-flight parse has gone quiet.
	StallState string              `json:"stall_state,omitempty" extensions:"x-agent"`
	LastError  *KnowledgeSpanError `json:"last_error,omitempty" extensions:"x-agent"`
}

// KnowledgeSpanError is the failure of a parse attempt. Name, ErrorCode and
// ErrorMessage repeat Stage, Code and Message for older clients.
type KnowledgeSpanError struct {
	Stage        string     `json:"stage" extensions:"x-agent=failed_stage"`
	Code         string     `json:"code" extensions:"x-agent=error_code"`
	Message      string     `json:"message" extensions:"x-agent=error_message"`
	Name         string     `json:"name"`
	ErrorCode    string     `json:"error_code"`
	ErrorMessage string     `json:"error_message"`
	FinishedAt   *time.Time `json:"finished_at"`
}

// knowledgeSpansLastError builds the last_error payload for GetKnowledgeSpans.
// Span failures win when present; otherwise a failed knowledge row without a
// matching span error (recovery / dead-letter paths) surfaces ErrorMessage.
func knowledgeSpansLastError(
	currentAttempt, latestAttempt int,
	parseStatus, knowledgeErrorMessage string,
	knowledgeUpdatedAt time.Time,
	spanFailure *types.KnowledgeProcessingSpan,
) *KnowledgeSpanError {
	if spanFailure != nil {
		return &KnowledgeSpanError{
			Stage:        spanFailure.Name,
			Code:         spanFailure.ErrorCode,
			Message:      spanFailure.ErrorMessage,
			Name:         spanFailure.Name,
			ErrorCode:    spanFailure.ErrorCode,
			ErrorMessage: spanFailure.ErrorMessage,
			FinishedAt:   spanFailure.FinishedAt,
		}
	}
	if currentAttempt != latestAttempt || parseStatus != types.ParseStatusFailed || knowledgeErrorMessage == "" {
		return nil
	}
	errorCode := "UNKNOWN"
	if strings.EqualFold(strings.TrimSpace(knowledgeErrorMessage),
		"Task interrupted due to application restart") {
		errorCode = "SERVER_RESTART"
	}
	return &KnowledgeSpanError{
		Stage:        "knowledge_processing",
		Code:         errorCode,
		Message:      knowledgeErrorMessage,
		Name:         "knowledge_processing",
		ErrorCode:    errorCode,
		ErrorMessage: knowledgeErrorMessage,
		FinishedAt:   &knowledgeUpdatedAt,
	}
}

// missingStageStatusFunc resolves a rowless stage against the real failure:
// earlier → skipped, downstream → cancelled, none → the parse_status fallback (#3452).
func missingStageStatusFunc(
	stageRowByName map[string]*types.KnowledgeProcessingSpan, fallback string,
) func(string) string {
	failedIdx := -1
	for i, name := range types.AllStages {
		if row, ok := stageRowByName[name]; ok && row.Status == types.SpanStatusFailed {
			failedIdx = i
			break
		}
	}
	if failedIdx < 0 {
		return func(string) string { return fallback }
	}

	// Redundant with canonical order today; keeps holding if stages are reordered.
	cancelled := map[string]bool{}
	var markDependents func(string)
	markDependents = func(stage string) {
		for candidate, upstreams := range types.StageDependencies {
			if cancelled[candidate] {
				continue
			}
			for _, up := range upstreams {
				if up == stage {
					cancelled[candidate] = true
					markDependents(candidate)
					break
				}
			}
		}
	}
	markDependents(types.AllStages[failedIdx])

	stageIdx := make(map[string]int, len(types.AllStages))
	for i, name := range types.AllStages {
		stageIdx[name] = i
	}
	return func(name string) string {
		if cancelled[name] || stageIdx[name] > failedIdx {
			return types.SpanStatusCancelled
		}
		return types.SpanStatusSkipped
	}
}

// buildSpanTree assembles a flat list of span rows into a parent-child
// tree rooted at the (knowledge, attempt)'s root span. Missing canonical
// stages are filled in with pending placeholders so the UI always renders
// the five timeline segments. Returns the root, the current_stage name
// (the running stage if any), and the most recent failed span if one
// exists.
//
// parseStatus is the knowledge.parse_status string. When the spans table
// has zero rows for this attempt (legacy data parsed before tracking, or
// a fresh knowledge before the pipeline starts), the placeholder status
// is inferred from parseStatus: completed → done, failed → failed,
// otherwise pending. Without this, every historical knowledge would
// render as "all 5 stages pending" forever despite having actually
// completed parsing.
func buildSpanTree(knowledgeID string, attempt int, rows []types.KnowledgeProcessingSpan, parseStatus string) (
	root *types.SpanTreeNode, currentStage string, lastFailure *types.KnowledgeProcessingSpan,
) {
	now := time.Now()
	// Build node lookup, identify root.
	nodes := make(map[string]*types.SpanTreeNode, len(rows))
	var rootRow *types.KnowledgeProcessingSpan
	stageRowByName := map[string]*types.KnowledgeProcessingSpan{}
	for i := range rows {
		r := rows[i]
		nodes[r.SpanID] = &types.SpanTreeNode{KnowledgeProcessingSpan: r}
		if r.Kind == types.SpanKindRoot && rootRow == nil {
			cp := r
			rootRow = &cp
		}
		if r.Kind == types.SpanKindStage {
			cp := r
			stageRowByName[r.Name] = &cp
		}
		if r.Status == types.SpanStatusRunning && r.Kind == types.SpanKindStage && currentStage == "" {
			currentStage = r.Name
		}
		// Housekeeping's TASK_STALLED marks where a stuck run stopped;
		// an older subtask failure must not hide it.
		if r.Status == types.SpanStatusFailed &&
			(lastFailure == nil || !isStallFailure(lastFailure) || isStallFailure(&r)) {
			cp := r
			lastFailure = &cp
		}
	}
	if currentStage == "" {
		currentStage = stageOfRunningSpan(rows)
	}

	// Pick the synthesized stage status from parse_status. Without this,
	// historical knowledge that completed before span tracking was wired
	// would render as "5 pending stages" forever — the rows simply
	// weren't recorded, but parse_status correctly reads "completed".
	// The synthesized stages don't carry duration/timing data; they
	// just communicate the inferred terminal state.
	syntheticStatus := types.SpanStatusPending
	switch parseStatus {
	case types.ParseStatusCompleted:
		syntheticStatus = types.SpanStatusDone
	case types.ParseStatusFailed:
		syntheticStatus = types.SpanStatusFailed
	case types.ParseStatusCancelled:
		syntheticStatus = types.SpanStatusCancelled
	}

	// Synthesize root if no rows came back so the API contract stays
	// stable (frontend always expects a `trace` object).
	if rootRow == nil {
		root = &types.SpanTreeNode{KnowledgeProcessingSpan: types.KnowledgeProcessingSpan{
			KnowledgeID: knowledgeID,
			Attempt:     attempt,
			SpanID:      "",
			Name:        "knowledge_processing",
			Kind:        types.SpanKindRoot,
			Status:      syntheticStatus,
			CreatedAt:   now,
			UpdatedAt:   now,
		}}
	} else {
		root = nodes[rootRow.SpanID]
	}

	// Link real children to their parents. Walk `rows` (not the
	// `nodes` map!) so the append order matches the repo's stable
	// `ORDER BY id ASC`. Iterating the map directly would give
	// callers a different child ordering on every request — Go map
	// iteration is intentionally randomised — and the UI would
	// flicker subspans into a different order on each refresh.
	for i := range rows {
		r := &rows[i]
		n := nodes[r.SpanID]
		if n == nil || n == root {
			continue
		}
		if r.ParentSpanID == "" {
			// Real top-level row with no parent and not the root
			// itself — attach to root so it doesn't dangle.
			root.Children = append(root.Children, n)
			continue
		}
		parent, ok := nodes[r.ParentSpanID]
		if !ok {
			// Unknown parent (orphan); attach to root.
			root.Children = append(root.Children, n)
			continue
		}
		parent.Children = append(parent.Children, n)
	}

	// Synthesize missing stage rows as children of root so the timeline
	// always shows 5 segments. Status mirrors the synthesized root —
	// pending while the pipeline is still running, done/failed for
	// historical knowledge whose terminal state we know but whose
	// per-stage timing was never recorded. Appended in AllStages order
	// so the canonical stage layout is deterministic regardless of
	// which rows are missing.
	missingStageStatus := missingStageStatusFunc(stageRowByName, syntheticStatus)
	for _, name := range types.AllStages {
		if _, ok := stageRowByName[name]; ok {
			continue
		}
		placeholder := types.KnowledgeProcessingSpan{
			KnowledgeID: knowledgeID,
			Attempt:     attempt,
			Name:        name,
			Kind:        types.SpanKindStage,
			Status:      missingStageStatus(name),
			CreatedAt:   now,
			UpdatedAt:   now,
		}
		root.Children = append(root.Children, &types.SpanTreeNode{KnowledgeProcessingSpan: placeholder})
	}

	return root, currentStage, lastFailure
}

// ListKnowledge godoc
// @Summary      获取知识列表
// @Description  获取知识库下的知识列表，支持分页和筛选
// @Tags         知识管理
// @Accept       json
// @Produce      json
// @Description  不传 parse_status 时不返回删除中（deleting）的文档。
// @Param        id         path      string  true   "知识库ID"  extensions(x-agent=knowledge_base_id)
// @Param        page       query     int     false  "页码"  minimum(1)  default(1)  extensions(x-agent,x-agent-maximum=10000)
// @Param        page_size  query     int     false  "每页数量"  minimum(1)  maximum(1000)  default(20)  extensions(x-agent,x-agent-maximum=50)
// @Param        tag_ids       query     string  false  "标签ID筛选，逗号分隔（OR语义）"  extensions(x-agent)
// @Param        keyword       query     string  false  "关键词搜索"  extensions(x-agent)
// @Param        file_type     query     string  false  "文件类型筛选"  extensions(x-agent)
// @Param parse_status query string false "状态" Enums(pending,processing,finalizing,completed,failed,cancelled,deleting)  extensions(x-agent)
// @Param        source        query     string  false  "来源/渠道筛选 (web/api/feishu/notion/yuque/wechat/...，或 manual/url 按 type 过滤)"
// @Param        start_time    query     string  false  "更新时间起点，RFC3339 格式"
// @Param        end_time      query     string  false  "更新时间终点，RFC3339 格式"
// @Param        folder_path      query     string  false  "文件夹路径筛选，空字符串表示知识库根目录；不传该参数则不按文件夹过滤"  extensions(x-agent)
// @Param        folder_recursive query     bool    false  "为 true 时同时返回子文件夹内的文档"  extensions(x-agent)
// @Param        sort_by      query     string  false  "排序字段" Enums(created_at,updated_at,file_name) default(created_at)  extensions(x-agent)
// @Param        sort_order   query     string  false  "排序方向"  Enums(asc, desc)  default(desc)  extensions(x-agent)
// @Success      200        {object}  handler.PageResponse{data=[]types.Knowledge}  "知识列表"
// @Failure      400        {object}  errors.AppError                               "请求参数错误"
// @Failure      403        {object}  errors.AppError                               "无权访问知识库"
// @Security     Bearer
// @Security     ApiKeyAuth
// @x-agent      {"id":"knowledge.list","risk":"read"}
// @x-agent-hint "Use parse_status=failed to find documents that failed to parse; meta.total counts every match."
// @Router       /knowledge-bases/{id}/knowledge [get]
func (h *KnowledgeHandler) ListKnowledge(c *gin.Context) {
	ctx := c.Request.Context()

	logger.Info(ctx, "Start retrieving knowledge list")

	// Validate access to the knowledge base (read access - any permission level)
	_, kbID, effectiveTenantID, _, err := h.validateKnowledgeBaseAccess(c)
	if err != nil {
		_ = c.Error(err)
		return
	}

	// Update context with effective tenant ID for shared KB access
	ctx = types.WithExecutionTenant(c.Request.Context(), effectiveTenantID)

	// Parse pagination parameters from query string
	var pagination types.Pagination
	if err := c.ShouldBindQuery(&pagination); err != nil {
		logger.Error(ctx, "Failed to parse pagination parameters", err)
		_ = c.Error(errors.NewBadRequestError(err.Error()))
		return
	}

	filter := types.KnowledgeListFilter{
		TagIDs:      parseCommaSeparatedTagIDs(c.Query("tag_ids")),
		Keyword:     c.Query("keyword"),
		FileType:    c.Query("file_type"),
		ParseStatus: c.Query("parse_status"),
		Source:      c.Query("source"),
		SortBy: types.KnowledgeListSortField(
			c.DefaultQuery("sort_by", string(types.KnowledgeListSortByCreatedAt)),
		),
		SortOrder: types.KnowledgeListSortOrder(
			c.DefaultQuery("sort_order", string(types.KnowledgeListSortDescending)),
		),
	}
	if !filter.SortBy.Valid() {
		_ = c.Error(errors.NewBadRequestError("invalid sort_by: must be updated_at, created_at, or file_name"))
		return
	}
	if !filter.SortOrder.Valid() {
		_ = c.Error(errors.NewBadRequestError("invalid sort_order: must be asc or desc"))
		return
	}
	if raw := c.Query("start_time"); raw != "" {
		t, err := parseFilterTime(raw)
		if err != nil {
			_ = c.Error(errors.NewBadRequestError("invalid start_time: " + err.Error()))
			return
		}
		filter.UpdatedFrom = t
	}
	if raw := c.Query("end_time"); raw != "" {
		t, err := parseFilterTime(raw)
		if err != nil {
			_ = c.Error(errors.NewBadRequestError("invalid end_time: " + err.Error()))
			return
		}
		filter.UpdatedTo = t
	}
	// The folder dimension is opt-in by parameter *presence*: an empty
	// folder_path is meaningful (the knowledge base root), so it cannot be
	// distinguished from "no folder filter" by value alone.
	if raw, ok := c.GetQuery("folder_path"); ok {
		filter.FolderPath = types.NormalizeKnowledgeFolderPath(raw)
		filter.FolderScope = types.FolderScopeExact
		if recursive, err := strconv.ParseBool(c.DefaultQuery("folder_recursive", "false")); err == nil && recursive {
			filter.FolderScope = types.FolderScopeSubtree
		}
	}

	logger.Infof(
		ctx,
		"Retrieving knowledge list under knowledge base, kb_id=%s tag_ids=%s keyword=%s file_type=%s "+
			"parse_status=%s source=%s start_time=%s end_time=%s folder_path=%s folder_scope=%s "+
			"sort_by=%s sort_order=%s page=%d page_size=%d effectiveTenantID=%d",
		utils.SanitizeForLog(kbID),
		utils.SanitizeForLog(strings.Join(filter.TagIDs, ",")),
		utils.SanitizeForLog(filter.Keyword),
		utils.SanitizeForLog(filter.FileType),
		utils.SanitizeForLog(filter.ParseStatus),
		utils.SanitizeForLog(filter.Source),
		utils.SanitizeForLog(c.Query("start_time")),
		utils.SanitizeForLog(c.Query("end_time")),
		utils.SanitizeForLog(filter.FolderPath),
		string(filter.FolderScope),
		string(filter.SortBy),
		string(filter.SortOrder),
		pagination.Page,
		pagination.PageSize,
		effectiveTenantID,
	)

	// Retrieve paginated knowledge entries
	result, err := h.kgService.ListPagedKnowledgeByKnowledgeBaseID(ctx, kbID, &pagination, filter)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		_ = c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	logger.Infof(
		ctx,
		"Knowledge list retrieved successfully, knowledge base ID: %s, total: %d",
		utils.SanitizeForLog(kbID),
		result.Total,
	)
	c.JSON(http.StatusOK, gin.H{
		"success":   true,
		"data":      result.Data,
		"total":     result.Total,
		"page":      result.Page,
		"page_size": result.PageSize,
	})
}

// GetKnowledgeBatchRequest defines parameters for batch knowledge retrieval
type GetKnowledgeBatchRequest struct {
	IDs                 []string `form:"ids" binding:"required"`
	KBID                string   `form:"kb_id"`
	AgentID             string   `form:"agent_id"`
	AgentSourceTenantID uint64   `form:"agent_source_tenant_id"`
}

// GetKnowledgeBatch godoc
// @Summary      批量获取知识
// @Description  根据ID列表批量获取知识条目；可选 kb_id 或共享 agent_id 限定空间范围与访问校验。
// @Tags         知识管理
// @Accept       json
// @Produce      json
// @Param        ids       query     []string  true   "知识ID列表"
// @Param        kb_id     query     string   false  "可选，知识库ID（用于共享知识库时指定范围）"
// @Param        agent_id  query     string   false  "可选，共享智能体ID（用于按智能体空间批量拉取文件详情）"
// @Success      200       {object}  handler.DataResponse{data=[]types.Knowledge}  "知识列表"
// @Failure      400       {object}  errors.AppError        "请求参数错误"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge/batch [get]
func (h *KnowledgeHandler) GetKnowledgeBatch(c *gin.Context) {
	ctx := c.Request.Context()

	effectiveTenantID := middleware.KBAccessRequest(c).Caller.TenantID
	if effectiveTenantID == 0 {
		_ = c.Error(errors.NewUnauthorizedError("Unauthorized"))
		return
	}

	var req GetKnowledgeBatchRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		logger.Error(ctx, "Failed to parse request parameters", err)
		_ = c.Error(errors.NewBadRequestError("Invalid request parameters").WithDetails(err.Error()))
		return
	}
	if _, parseErr := types.ParseAgentSourceTenantID(c.Query(types.AgentSourceTenantIDParam)); parseErr != nil {
		_ = c.Error(errors.NewBadRequestError(parseErr.Error()))
		return
	}

	// A nil scope means no agent selector. Every resolved scope, including
	// mode=all, binds results to the authorized agent's source tenant.
	var agentScope *types.SharedAgentKBScope
	if agentID := utils.SanitizeForLog(req.AgentID); agentID != "" {
		agent, err := resolveSharedAgentForRequest(c, agentID, h.agentShareService)
		if err != nil {
			_ = c.Error(err)
			return
		}
		ctx = access.WithSharedAgent(ctx, agent)
		scope := types.NewSharedAgentKBScope(agent)
		agentScope = &scope
		effectiveTenantID = agent.TenantID
		if scope.IsEmpty() {
			c.JSON(http.StatusOK, gin.H{"success": true, "data": []*types.Knowledge{}})
			return
		}
	}

	var knowledges []*types.Knowledge
	var err error

	// scopeKBID tracks the single KB the results must belong to (set by explicit kb_id).
	var scopeKBID string

	// Optional kb_id: validate KB access and use effective tenant for shared KB
	if kbID := utils.SanitizeForLog(req.KBID); kbID != "" {
		_, _, effID, _, accessErr := h.validateKnowledgeBaseAccessWithKBID(c, kbID)
		if accessErr != nil {
			_ = c.Error(accessErr)
			return
		}
		if agentScope != nil && !agentScope.Allows(kbID, effID) {
			_ = c.Error(errors.NewForbiddenError("Knowledge base not accessible through this agent"))
			return
		}
		scopeKBID = kbID
		effectiveTenantID = effID
		ctx = types.WithExecutionTenant(c.Request.Context(), effectiveTenantID)

		logger.Infof(ctx, "Batch retrieving knowledge with kb_id, effective tenant ID: %d, IDs count: %d",
			effectiveTenantID, len(req.IDs))

		knowledges, err = h.kgService.GetKnowledgeBatch(ctx, effectiveTenantID, req.IDs)
	} else if agentScope != nil {
		// Keep the authorized agent's read scope and the original caller.
		ctx = types.WithExecutionTenant(ctx, effectiveTenantID)
		knowledges, err = h.kgService.GetKnowledgeBatch(ctx, effectiveTenantID, req.IDs)
	} else {
		knowledges, err = h.kgService.GetKnowledgeBatchWithSharedAccess(ctx, effectiveTenantID, req.IDs)
	}
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		_ = c.Error(errors.NewInternalServerError("Failed to retrieve knowledge list").WithDetails(err.Error()))
		return
	}

	// Build the effective allowed-KB set from explicit kb_id, shared agent
	// scope, and per-API-key KB restrictions.
	var allowedKBSet map[string]bool
	if scopeKBID != "" {
		allowedKBSet = map[string]bool{scopeKBID: true}
	}
	if agentScope != nil {
		knowledges = filterKnowledgeByAgentScope(knowledges, *agentScope)
	}

	if apiKeySet := tenantAPIKeyAllowedKBSet(ctx); apiKeySet != nil {
		allowedKBSet = intersectKBAllowSet(allowedKBSet, apiKeySet)
	}
	if allowedKBSet != nil {
		knowledges = filterKnowledgesByKBAllowSet(knowledges, allowedKBSet)
	}

	h.attachLastActivity(ctx, knowledges)

	logger.Infof(ctx, "Batch knowledge retrieval successful, requested count: %d, returned count: %d",
		len(req.IDs), len(knowledges))

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    knowledges,
	})
}

func isStallFailure(span *types.KnowledgeProcessingSpan) bool {
	return span.ErrorCode == errors.ErrCodeTaskStalled
}

// stageOfRunningSpan names the stage owning the newest running span, for the
// window where no stage span is running but its work still is: post-process
// closes its stage once summary / question / graph / wiki are fanned out.
func stageOfRunningSpan(rows []types.KnowledgeProcessingSpan) string {
	bySpanID := make(map[string]*types.KnowledgeProcessingSpan, len(rows))
	for i := range rows {
		bySpanID[rows[i].SpanID] = &rows[i]
	}
	for i := len(rows) - 1; i >= 0; i-- {
		if rows[i].Status != types.SpanStatusRunning || rows[i].Kind == types.SpanKindRoot {
			continue
		}
		for span, depth := &rows[i], 0; span != nil && depth < 64; depth++ {
			if span.Kind == types.SpanKindStage {
				return span.Name
			}
			span = bySpanID[span.ParentSpanID]
		}
	}
	return ""
}

// spansLastActivity is the latest of the row's updated_at and the listed
// spans' writes.
func spansLastActivity(updatedAt time.Time, rows []types.KnowledgeProcessingSpan) time.Time {
	last := updatedAt
	for _, row := range rows {
		last = latestActivity(last, row.UpdatedAt)
	}
	return last
}

// attachLastActivity sets LastActivityAt on in-flight rows. updated_at only
// moves at stage transitions, so the latest span write is folded in: it
// advances with every subspan while a long stage is still working.
func (h *KnowledgeHandler) attachLastActivity(ctx context.Context, knowledges []*types.Knowledge) {
	var ids []string
	for _, k := range knowledges {
		if k != nil && isParseInFlight(k.ParseStatus) {
			ids = append(ids, k.ID)
		}
	}
	if len(ids) == 0 {
		return
	}
	var spanActivity map[string]time.Time
	if h.kgService != nil {
		var err error
		if spanActivity, err = h.kgService.GetLastSpanActivity(ctx, ids); err != nil {
			logger.Warnf(ctx, "span last activity lookup failed: %v", err)
		}
	}
	var quiet []*types.Knowledge
	var quietIDs []string
	for _, k := range knowledges {
		if k == nil || !isParseInFlight(k.ParseStatus) {
			continue
		}
		last := latestActivity(k.UpdatedAt, spanActivity[k.ID])
		k.LastActivityAt = &last
		if time.Since(last) >= stallHintAfter {
			quiet = append(quiet, k)
			quietIDs = append(quietIDs, k.ID)
		}
	}
	verdicts := h.stallVerdicts(ctx, quietIDs)
	for _, k := range quiet {
		k.StallState = verdicts[k.ID]
	}
}

func isParseInFlight(status string) bool {
	switch status {
	case types.ParseStatusPending, types.ParseStatusProcessing, types.ParseStatusFinalizing:
		return true
	}
	return false
}

func latestActivity(times ...time.Time) time.Time {
	var last time.Time
	for _, t := range times {
		if t.After(last) {
			last = t
		}
	}
	return last
}

// parseCommaSeparatedTagIDs splits a comma-separated string of tag IDs and
// filters out empty strings and the "__untagged__" sentinel value.
func parseCommaSeparatedTagIDs(raw string) []string {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" || p == "__untagged__" {
			continue
		}
		result = append(result, p)
	}
	return result
}

// parseFilterTime parses a query-string timestamp accepted by knowledge list
// filters. It supports RFC3339, RFC3339 with milliseconds, and the date-only
// "2006-01-02" form (interpreted at start of day in the local timezone).
func parseFilterTime(raw string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, nil
	}
	layouts := []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05", "2006-01-02"}
	var lastErr error
	for _, layout := range layouts {
		t, err := time.ParseInLocation(layout, raw, time.Local)
		if err == nil {
			return t, nil
		}
		lastErr = err
	}
	return time.Time{}, lastErr
}

func requireTenantAPIKeyKnowledgeBase(ctx context.Context, kbID string) error {
	return requireTenantAPIKeyKnowledgeBases(ctx, kbID)
}

func requireTenantAPIKeyKnowledgeBases(ctx context.Context, kbIDs ...string) error {
	return types.AuthorizeTenantAPIKeyKnowledgeBases(ctx, kbIDs...)
}

func tenantAPIKeyAllowedKBSet(ctx context.Context) map[string]bool {
	scope, ok := types.TenantAPIKeyScopeFromContext(ctx)
	if !ok || !scope.IsKnowledgeBaseRestricted() {
		return nil
	}
	allowed := make(map[string]bool, len(scope.KnowledgeBaseIDs))
	for _, id := range scope.KnowledgeBaseIDs {
		allowed[id] = true
	}
	return allowed
}

func intersectKBAllowSet(base, restrict map[string]bool) map[string]bool {
	if restrict == nil {
		return base
	}
	if base == nil {
		return restrict
	}
	out := make(map[string]bool)
	for id := range base {
		if restrict[id] {
			out[id] = true
		}
	}
	return out
}

func filterKnowledgesByKBAllowSet(knowledges []*types.Knowledge, allowed map[string]bool) []*types.Knowledge {
	if allowed == nil {
		return knowledges
	}
	filtered := make([]*types.Knowledge, 0, len(knowledges))
	for _, k := range knowledges {
		if k != nil && allowed[k.KnowledgeBaseID] {
			filtered = append(filtered, k)
		}
	}
	return filtered
}

func filterKnowledgeSearchScopesForAPIKey(
	ctx context.Context, scopes []types.KnowledgeSearchScope,
) []types.KnowledgeSearchScope {
	allowed := tenantAPIKeyAllowedKBSet(ctx)
	if allowed == nil {
		return scopes
	}
	filtered := make([]types.KnowledgeSearchScope, 0, len(scopes))
	for _, scope := range scopes {
		if allowed[scope.KBID] {
			filtered = append(filtered, scope)
		}
	}
	return filtered
}

func tenantAPIKeySearchScopes(ctx context.Context) ([]types.KnowledgeSearchScope, bool) {
	scope, ok := types.TenantAPIKeyScopeFromContext(ctx)
	if !ok || !scope.IsKnowledgeBaseRestricted() {
		return nil, false
	}
	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok || tenantID == 0 {
		return nil, true
	}
	scopes := make([]types.KnowledgeSearchScope, 0, len(scope.KnowledgeBaseIDs))
	for _, kbID := range scope.KnowledgeBaseIDs {
		scopes = append(scopes, types.KnowledgeSearchScope{TenantID: tenantID, KBID: kbID})
	}
	return scopes, true
}
