package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Usir233/GENYEE/internal/application/service"
	apperrors "github.com/Usir233/GENYEE/internal/errors"
	"github.com/Usir233/GENYEE/internal/logger"
	modellimiter "github.com/Usir233/GENYEE/internal/models/limiter"
	"github.com/Usir233/GENYEE/internal/types"
	"github.com/Usir233/GENYEE/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

// RuntimeWorkerPool reports configured per-process concurrency plus live
// cluster capacity/active workers aggregated from asynq server heartbeats.
type RuntimeWorkerPool struct {
	Name            string  `json:"name" extensions:"x-agent"`
	Concurrency     int     `json:"concurrency" extensions:"x-agent"` // configured per instance
	QueueCount      int     `json:"queue_count" extensions:"x-agent"`
	Instances       int     `json:"instances" extensions:"x-agent"`        // live asynq server heartbeats
	ClusterCapacity int     `json:"cluster_capacity" extensions:"x-agent"` // sum of live server concurrency
	Active          int     `json:"active" extensions:"x-agent"`           // live workers assigned to this pool
	Utilization     float64 `json:"utilization" extensions:"x-agent"`      // Active / ClusterCapacity
}

// RuntimeQueuesResponse is the payload for the SystemAdmin runtime queue dashboard.
type RuntimeQueuesResponse struct {
	QueuesError         bool `json:"queues_error" extensions:"x-agent"`
	WorkersError        bool `json:"workers_error" extensions:"x-agent"`
	ModelsError         bool `json:"models_error" extensions:"x-agent"`
	Available           bool `json:"available" extensions:"x-agent"`
	UpstreamConcurrency int  `json:"upstream_concurrency" extensions:"x-agent"`
	// compatibility alias for upstream_concurrency
	ParseConcurrency      int                        `json:"parse_concurrency"`
	WikiConcurrency       int                        `json:"wiki_concurrency"` // compatibility field
	Pools                 []RuntimeWorkerPool        `json:"pools" extensions:"x-agent"`
	Queues                []types.QueueStat          `json:"queues" extensions:"x-agent"`
	ModelLimiterAvailable bool                       `json:"model_limiter_available" extensions:"x-agent"`
	Models                []modellimiter.RuntimeStat `json:"models" extensions:"x-agent"`
	Timestamp             int64                      `json:"timestamp" extensions:"x-agent"`
}

func aggregateRuntimeWorkerPools(pools []RuntimeWorkerPool, servers []types.WorkerServerStat) {
	queueConfigs := map[string]map[string]int{
		types.WorkerPoolCore:        types.QueueWeightsForPool(types.WorkerPoolCore),
		types.WorkerPoolPostProcess: types.QueueWeightsForPool(types.WorkerPoolPostProcess),
		types.WorkerPoolEnrichment:  types.QueueWeightsForPool(types.WorkerPoolEnrichment),
		types.WorkerPoolMaintenance: types.QueueWeightsForPool(types.WorkerPoolMaintenance),
		types.WorkerPoolShared:      types.QueueWeightsForSharedPool(),
		types.WorkerPoolWiki:        types.QueueWeightsForPool(types.WorkerPoolWiki),
	}
	indexes := make(map[string]int, len(pools))
	for i := range pools {
		indexes[pools[i].Name] = i
	}
	for _, server := range servers {
		if server.Status != "active" {
			continue
		}
		for poolName, expectedQueues := range queueConfigs {
			if !sameQueueWeights(server.Queues, expectedQueues) {
				continue
			}
			idx, ok := indexes[poolName]
			if !ok {
				break
			}
			pools[idx].Instances++
			pools[idx].ClusterCapacity += server.Concurrency
			pools[idx].Active += server.Active
			break
		}
	}
	for i := range pools {
		if pools[i].ClusterCapacity > 0 {
			pools[i].Utilization = float64(pools[i].Active) / float64(pools[i].ClusterCapacity)
		}
	}
}

func sameQueueWeights(left, right map[string]int) bool {
	if len(left) != len(right) {
		return false
	}
	for name, weight := range left {
		if right[name] != weight {
			return false
		}
	}
	return true
}

// GetRuntimeQueues godoc
// @Summary      获取解析任务队列运行时状态
// @Description  返回各 asynq 队列的实时深度（pending/active/scheduled/retry 等）与 worker 并发配置，仅系统管理员可见
// @Tags         系统管理
// @Produce      json
// @Success      200  {object}  RuntimeQueuesResponse
// @x-agent {"id":"system_admin.get_queues","risk":"read","system_admin":true}
// @x-agent-hint "Reads platform-wide queue depth, worker pools and model concurrency. available false means unsupported, not healthy or empty. Check queues_error, workers_error and models_error before interpreting counts. failed is today's counter; retry/archived are current depths. Pool concurrency is per instance; cluster_capacity and active reflect live worker heartbeats. Does not control workers or retry tasks."
// @Router       /system/admin/runtime/queues [get]
func (h *SystemHandler) GetRuntimeQueues(c *gin.Context) {
	ctx := logger.CloneContext(c.Request.Context())
	var allocation types.WorkerPoolConcurrency
	if h.systemSettingSvc != nil {
		allocation = types.ResolveWorkerPoolConcurrency(func(key, env string, fallback int) int {
			configured := h.systemSettingSvc.GetInt(ctx, key, env, int64(fallback))
			return int(configured)
		})
	} else {
		allocation = types.DefaultWorkerPoolConcurrency()
	}
	queueCounts := make(map[string]int)
	for _, definition := range types.QueueDefinitions() {
		queueCounts[definition.Pool]++
	}
	resp := RuntimeQueuesResponse{
		UpstreamConcurrency: allocation.UpstreamTotal(),
		ParseConcurrency:    allocation.UpstreamTotal(),
		WikiConcurrency:     allocation.Wiki,
		Pools: []RuntimeWorkerPool{
			{
				Name:        types.WorkerPoolCore,
				Concurrency: allocation.Core,
				QueueCount:  queueCounts[types.WorkerPoolCore],
			},
			{
				Name:        types.WorkerPoolPostProcess,
				Concurrency: allocation.PostProcess,
				QueueCount:  queueCounts[types.WorkerPoolPostProcess],
			},
			{
				Name:        types.WorkerPoolEnrichment,
				Concurrency: allocation.Enrichment,
				QueueCount:  queueCounts[types.WorkerPoolEnrichment],
			},
			{
				Name:        types.WorkerPoolMaintenance,
				Concurrency: allocation.Maintenance,
				QueueCount:  queueCounts[types.WorkerPoolMaintenance],
			},
			{
				Name:        types.WorkerPoolShared,
				Concurrency: allocation.Shared,
				QueueCount:  len(types.QueueWeightsForSharedPool()),
			},
			{
				Name:        types.WorkerPoolWiki,
				Concurrency: allocation.Wiki,
				QueueCount:  queueCounts[types.WorkerPoolWiki],
			},
		},
		Timestamp: time.Now().Unix(),
	}

	if h.taskInspector != nil {
		stats, supported, err := h.taskInspector.QueueStats(ctx)
		if err != nil {
			logger.Errorf(ctx, "get queue stats failed: %v", err)
		}
		resp.QueuesError = err != nil
		resp.Available = supported
		if stats != nil {
			resp.Queues = stats
		}
		serverStats, _, serverErr := h.taskInspector.WorkerServerStats(ctx)
		resp.WorkersError = serverErr != nil
		if serverErr != nil {
			logger.Errorf(ctx, "get worker server stats failed: %v", serverErr)
		} else {
			aggregateRuntimeWorkerPools(resp.Pools, serverStats)
		}
	}
	if resp.Queues == nil {
		resp.Queues = []types.QueueStat{}
	}
	modelStats, modelSupported, modelErr := modellimiter.RuntimeStats(ctx)
	if modelErr != nil {
		logger.Errorf(ctx, "get model concurrency stats failed: %v", modelErr)
	}
	resp.ModelsError = modelErr != nil
	resp.ModelLimiterAvailable = modelSupported
	resp.Models = modelStats

	c.JSON(http.StatusOK, resp)
}

// RuntimeTasksResponse represents RuntimeTasksResponse.
type RuntimeTasksResponse struct {
	Available  bool                    `json:"available" extensions:"x-agent"`
	Tasks      []types.RuntimeTaskInfo `json:"tasks"`
	PageSize   int                     `json:"page_size" extensions:"x-agent"`
	HasMore    bool                    `json:"has_more" extensions:"x-agent"`
	NextCursor string                  `json:"next_cursor,omitempty" extensions:"x-agent"`
}

func isKnownRuntimeQueue(name string) bool {
	for _, definition := range types.QueueDefinitions() {
		if definition.Name == name {
			return true
		}
	}
	return false
}

func runtimeTaskPageSize(c *gin.Context) int {
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	if pageSize < 1 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}
	return pageSize
}

func (h *SystemHandler) listRuntimeTasks(c *gin.Context, state types.RuntimeTaskState) {
	queue := c.Param("queue")
	if !isKnownRuntimeQueue(queue) {
		writeAppError(c, apperrors.NewBadRequestError("Unknown task queue"))
		return
	}
	if !state.Valid() {
		writeAppError(c, apperrors.NewBadRequestError("Unknown task state"))
		return
	}
	pageSize := runtimeTaskPageSize(c)
	cursor := c.Query("cursor")
	inspector, ok := h.taskInspector.(interfaces.RuntimeTaskInspector)
	if !ok {
		c.JSON(http.StatusOK, RuntimeTasksResponse{
			Available: false,
			Tasks:     []types.RuntimeTaskInfo{},
			PageSize:  pageSize,
		})
		return
	}
	page, supported, err := inspector.ListRuntimeTasks(c.Request.Context(), queue, state, cursor, pageSize)
	if err != nil {
		if errors.Is(err, types.ErrInvalidRuntimeTaskCursor) {
			appErr := apperrors.NewBadRequestError("Invalid task list cursor").WithDetails(
				map[string]string{"code": "runtime_task_cursor_invalid"},
			)
			writeAppErrorWithCode(c, appErr, "runtime_task_cursor_invalid")
			return
		}
		if errors.Is(err, types.ErrExpiredRuntimeTaskCursor) {
			appErr := apperrors.NewConflictError("Task list changed; refresh from the beginning").WithDetails(
				map[string]string{"code": "runtime_task_cursor_expired"},
			)
			writeAppErrorWithCode(c, appErr, "runtime_task_cursor_expired")
			return
		}
		logger.Errorf(c.Request.Context(), "list runtime queue tasks queue=%s state=%s: %v", queue, state, err)
		writeAppError(c, apperrors.NewInternalServerError("Failed to list queue tasks"))
		return
	}
	if page.Tasks == nil {
		page.Tasks = []types.RuntimeTaskInfo{}
	}
	for i := range page.Tasks {
		page.Tasks[i].ErrorCategory = runtimeErrorCategory(page.Tasks[i].LastError)
	}
	c.JSON(http.StatusOK, RuntimeTasksResponse{
		Available:  supported,
		Tasks:      page.Tasks,
		PageSize:   pageSize,
		HasMore:    page.HasMore,
		NextCursor: page.NextCursor,
	})
}

// ListRuntimeTasks returns one live task-state page for the unified operator drawer.
// @Summary      查询队列任务与失败记录（List runtime queue tasks）
// @Tags         System Admin
// @Produce      json
// @Param        queue path string true "Queue name" extensions(x-agent)
// @Param        state query string true "Task state" Enums(pending,active,scheduled,retry,archived,completed) extensions(x-agent)
// @Param        cursor query string false "Opaque continuation cursor" extensions(x-agent)
// @Param        page_size query int false "Page size" default(20) minimum(1) maximum(100) extensions(x-agent,x-agent-maximum=50)
// @Success      200 {object} RuntimeTasksResponse
// @x-agent {"id":"system_admin.list_tasks","risk":"read","system_admin":true,"root":"tasks","max_items":50}
// @x-agent-hint "Lists platform-wide tasks in one queue and state. Choose queue from system_admin.get_queues, never guess. For failures inspect retry and archived; active/pending help diagnose backlog. available false means unsupported. Continue with next_cursor while has_more; on expired cursor restart from the first page. error_category is a heuristic, not the full cause; unknown needs the operator task page/logs. Raw errors, payloads, results and worker addresses are omitted. This operation cannot retry, cancel or delete tasks."
// @Router       /system/admin/runtime/queues/{queue}/tasks [get]
func (h *SystemHandler) ListRuntimeTasks(c *gin.Context) {
	h.listRuntimeTasks(c, types.RuntimeTaskState(c.Query("state")))
}

func (h *SystemHandler) emitQueueTaskAudit(
	ctx context.Context,
	action types.AuditAction,
	queue, taskID string,
	extra map[string]string,
) {
	if h.auditSvc == nil {
		return
	}
	actorID, _ := types.UserIDFromContext(ctx)
	detailMap := map[string]string{"queue": queue, "task_id": taskID}
	for key, value := range extra {
		detailMap[key] = value
	}
	details, _ := json.Marshal(detailMap)
	targetType := "queue_task"
	targetID := taskID
	if targetID == "" {
		targetType = "task_queue"
		targetID = queue
	}
	_ = h.auditSvc.Log(ctx, &types.AuditLog{
		TenantID:    0,
		ActorUserID: actorID,
		ActorRole:   systemAuditActorRole(ctx),
		Action:      action,
		TargetType:  targetType,
		TargetID:    targetID,
		Outcome:     types.AuditOutcomeSuccess,
		Details:     types.JSON(details),
	})
}

func runtimeTaskParams(c *gin.Context) (string, string, bool) {
	queue := c.Param("queue")
	taskID := c.Param("task_id")
	if !isKnownRuntimeQueue(queue) || taskID == "" {
		writeAppError(c, apperrors.NewBadRequestError("Invalid queue or task ID"))
		return "", "", false
	}
	return queue, taskID, true
}

func (h *SystemHandler) mutateRuntimeTask(c *gin.Context, action types.RuntimeTaskAction) {
	queue, taskID, ok := runtimeTaskParams(c)
	if !ok {
		return
	}
	inspector, supported := h.taskInspector.(interfaces.RuntimeTaskInspector)
	if !supported {
		writeAppError(c, apperrors.NewServiceUnavailableError("Task queue is unavailable"))
		return
	}
	task, available, err := inspector.GetRuntimeTask(c.Request.Context(), queue, taskID)
	if !available {
		writeAppError(c, apperrors.NewServiceUnavailableError("Task queue is unavailable"))
		return
	}
	if err != nil || task == nil {
		writeAppError(c, apperrors.NewConflictError("Task is no longer available"))
		return
	}
	if !task.Allows(action) {
		writeAppError(c, apperrors.NewConflictError("Action is not allowed for the current task state"))
		return
	}

	var auditAction types.AuditAction
	auditDetails := map[string]string{
		"task_type":  task.Type,
		"from_state": string(task.State),
		"action":     string(action),
	}
	switch action {
	case types.RuntimeTaskActionCancel:
		if h.knowledgeSvc == nil || task.TenantID == 0 || task.KnowledgeID == "" {
			writeAppError(c, apperrors.NewServiceUnavailableError("Business cancellation is unavailable"))
			return
		}
		cancelCtx := context.WithValue(c.Request.Context(), types.TenantIDContextKey, task.TenantID)
		if _, err = h.knowledgeSvc.CancelKnowledgeParse(cancelCtx, task.KnowledgeID); err != nil {
			if isKnowledgeGone(err) {
				if h.purgeOrphanRuntimeTask(c.Request.Context(), inspector, queue, taskID, task.KnowledgeID) {
					logger.Warnf(c.Request.Context(),
						"cancel runtime queue task queue=%s task=%s: knowledge gone, purged queue record",
						queue, taskID)
					auditDetails["orphan_purge"] = "true"
					auditAction = types.AuditActionSystemQueueTaskCancelled
					break
				}
			}
			logger.Errorf(c.Request.Context(), "cancel runtime queue task queue=%s task=%s: %v", queue, taskID, err)
			writeAppError(c, apperrors.NewConflictError("Task can no longer be cancelled"))
			return
		}
		auditAction = types.AuditActionSystemQueueTaskCancelled
	case types.RuntimeTaskActionRunNow:
		available, err = inspector.RunRuntimeTask(c.Request.Context(), queue, taskID)
		if !available {
			writeAppError(c, apperrors.NewServiceUnavailableError("Task queue is unavailable"))
			return
		}
		if err != nil {
			logger.Errorf(c.Request.Context(), "run queue task now queue=%s task=%s: %v", queue, taskID, err)
			writeAppError(c, apperrors.NewConflictError("Task is no longer available to run"))
			return
		}
		auditAction = types.AuditActionSystemQueueTaskRunNow
	case types.RuntimeTaskActionDelete:
		available, err = inspector.DeleteRuntimeTask(c.Request.Context(), queue, taskID)
		if !available {
			writeAppError(c, apperrors.NewServiceUnavailableError("Task queue is unavailable"))
			return
		}
		if err != nil {
			logger.Errorf(c.Request.Context(), "delete queue task queue=%s task=%s: %v", queue, taskID, err)
			writeAppError(c, apperrors.NewConflictError("Task is no longer available for deletion"))
			return
		}
		auditAction = types.AuditActionSystemQueueTaskDeleted
	default:
		writeAppError(c, apperrors.NewBadRequestError("Unknown task action"))
		return
	}
	h.emitQueueTaskAudit(c.Request.Context(), auditAction, queue, taskID, auditDetails)
	c.JSON(http.StatusOK, gin.H{"success": true})
}

func isKnowledgeGone(err error) bool {
	if errors.Is(err, types.ErrKnowledgeNotFound) {
		return true
	}
	var appErr *apperrors.AppError
	return errors.As(err, &appErr) && appErr.Code == apperrors.ErrNotFound
}

func (h *SystemHandler) purgeOrphanRuntimeTask(
	ctx context.Context,
	inspector interfaces.RuntimeTaskInspector,
	queue, taskID, knowledgeID string,
) bool {
	if h.taskInspector != nil && knowledgeID != "" {
		if _, _, err := h.taskInspector.CancelTasksForKnowledge(ctx, knowledgeID); err != nil {
			logger.Warnf(ctx, "purge orphan tasks for knowledge %s: %v", knowledgeID, err)
		}
	}
	supported, err := inspector.ForceDeleteRuntimeTask(ctx, queue, taskID)
	if !supported {
		return false
	}
	if err == nil {
		return true
	}
	if _, available, getErr := inspector.GetRuntimeTask(ctx, queue, taskID); available && getErr != nil {
		return true
	}
	logger.Warnf(ctx, "force delete orphan task queue=%s task=%s: %v", queue, taskID, err)
	return false
}

// MutateRuntimeTask executes a backend-advertised action after re-reading the current task state.
// @Summary      Run a safe runtime task action
// @Tags         System Admin
// @Produce      json
// @Param        queue path string true "Queue name"
// @Param        task_id path string true "Task ID"
// @Param        action path string true "Action" Enums(cancel,run_now,delete)
// @Success      200 {object} map[string]bool
// @Router       /system/admin/runtime/queues/{queue}/tasks/{task_id}/actions/{action} [post]
func (h *SystemHandler) MutateRuntimeTask(c *gin.Context) {
	h.mutateRuntimeTask(c, types.RuntimeTaskAction(c.Param("action")))
}

// PurgeArchivedRuntimeTasks clears every archived (finally-failed) task in one queue.
// @Summary      Purge all archived tasks in a queue
// @Tags         System Admin
// @Produce      json
// @Param        queue path string true "Queue name"
// @Success      200 {object} handler.PurgeArchivedRuntimeTasksResponse
// @Router       /system/admin/runtime/queues/{queue}/archived [delete]
func (h *SystemHandler) PurgeArchivedRuntimeTasks(c *gin.Context) {
	queue := c.Param("queue")
	if !isKnownRuntimeQueue(queue) {
		writeAppError(c, apperrors.NewBadRequestError("Invalid queue"))
		return
	}
	inspector, supported := h.taskInspector.(interfaces.RuntimeTaskInspector)
	if !supported {
		writeAppError(c, apperrors.NewServiceUnavailableError("Task queue is unavailable"))
		return
	}
	deleted, available, err := inspector.PurgeArchivedRuntimeTasks(c.Request.Context(), queue)
	if !available {
		writeAppError(c, apperrors.NewServiceUnavailableError("Task queue is unavailable"))
		return
	}
	if err != nil {
		logger.Errorf(c.Request.Context(), "purge archived queue tasks queue=%s: %v", queue, err)
		writeAppError(c, apperrors.NewConflictError("Archived tasks could not be purged"))
		return
	}
	h.emitQueueTaskAudit(c.Request.Context(), types.AuditActionSystemQueueArchivedPurged, queue, "",
		map[string]string{"deleted": strconv.Itoa(deleted)})
	c.JSON(http.StatusOK, gin.H{"success": true, "deleted": deleted})
}

// ListSystemSettings godoc
// @Summary      List all system settings
// @Description  Return every row in the system_settings table (system-scope, not workspace-scoped). SystemAdmin only.
// @Tags         System Admin
// @Produce      json
// @Success      200 {array} types.SystemSetting "list of settings"
// @Failure      403 {object} apperrors.AppError "Forbidden: not a system admin"
// @Router       /system/admin/settings [get]
func (h *SystemHandler) ListSystemSettings(c *gin.Context) {
	ctx := logger.CloneContext(c.Request.Context())
	rows, err := h.systemSettingSvc.List(ctx)
	if err != nil {
		logger.Errorf(ctx, "list system settings failed: %v", err)
		writeAppError(c, apperrors.NewInternalServerError("Failed to list system settings"))
		return
	}
	if rows == nil {
		rows = []*types.SystemSetting{}
	}
	h.enrichSettingsModifiedBy(ctx, rows)
	c.JSON(http.StatusOK, rows)
}

func (h *SystemHandler) enrichSettingsModifiedBy(ctx context.Context, rows []*types.SystemSetting) {
	if len(rows) == 0 || h.userSvc == nil {
		return
	}
	idSet := make(map[string]struct{}, len(rows))
	for _, r := range rows {
		if r != nil && strings.TrimSpace(r.LastModifiedBy) != "" {
			idSet[r.LastModifiedBy] = struct{}{}
		}
	}
	if len(idSet) == 0 {
		return
	}
	ids := make([]string, 0, len(idSet))
	for id := range idSet {
		ids = append(ids, id)
	}
	users, err := h.userSvc.GetUsersByIDs(ctx, ids)
	if err != nil {
		logger.Warnf(ctx, "enrichSettingsModifiedBy: GetUsersByIDs failed: %v", err)
		return
	}
	for _, r := range rows {
		if r == nil {
			continue
		}
		u, ok := users[r.LastModifiedBy]
		if !ok || u == nil {
			continue
		}
		switch {
		case strings.TrimSpace(u.Username) != "":
			r.LastModifiedByName = u.Username
		case strings.TrimSpace(u.Email) != "":
			r.LastModifiedByName = u.Email
		}
	}
}

// GetSystemSetting godoc
// @Summary      Get a single system setting by key
// @Description  Returns the row matching :key. 404 when the key is unknown to the registry; 200 with the row when
// @Description  known.
// @Tags         System Admin
// @Produce      json
// @Param        key path string true "Setting key (e.g. file.max_size_mb)"
// @Success      200 {object} types.SystemSetting "the setting row"
// @Failure      400 {object} apperrors.AppError "Unknown key"
// @Failure      404 {object} apperrors.AppError "Key registered but DB row absent"
// @Router       /system/admin/settings/{key} [get]
func (h *SystemHandler) GetSystemSetting(c *gin.Context) {
	ctx := logger.CloneContext(c.Request.Context())
	key := c.Param("key")
	row, err := h.systemSettingSvc.Get(ctx, key)
	if err != nil {
		writeAppError(c, apperrors.NewBadRequestError(err.Error()))
		return
	}
	if row == nil {
		writeAppError(c, apperrors.NewNotFoundError("setting not yet persisted"))
		return
	}
	c.JSON(http.StatusOK, row)
}

// UpdateSystemSettingRequest is the body for PUT /system/admin/settings/:key.
type UpdateSystemSettingRequest struct {
	Value any `json:"value"`
}

// UpdateSystemSetting godoc
// @Summary      Update a system setting value
// @Description  Persist a new value for :key. SystemAdmin only. Emits an audit row on success.
// @Tags         System Admin
// @Accept       json
// @Produce      json
// @Param        key     path string                       true "Setting key"
// @Param        request body UpdateSystemSettingRequest   true "New value"
// @Success      200 {object} types.SystemSetting "the updated row"
// @Failure      400 {object} apperrors.AppError "Bad request / unknown key / type mismatch"
// @Failure      403 {object} apperrors.AppError "Forbidden: not a system admin"
// @Router       /system/admin/settings/{key} [put]
func (h *SystemHandler) UpdateSystemSetting(c *gin.Context) {
	ctx := logger.CloneContext(c.Request.Context())
	key := c.Param("key")

	var req UpdateSystemSettingRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeAppError(c, apperrors.NewBadRequestError("Invalid request: "+err.Error()))
		return
	}
	if req.Value == nil {
		writeAppError(c, apperrors.NewBadRequestError("value is required"))
		return
	}

	row, err := h.systemSettingSvc.Update(ctx, key, req.Value)
	if err != nil {
		writeAppError(c, apperrors.NewBadRequestError(err.Error()))
		return
	}
	h.enrichSettingsModifiedBy(ctx, []*types.SystemSetting{row})
	c.JSON(http.StatusOK, row)
}

// ApplyDefaultStorageQuotaToAllTenants godoc
// @Summary      Apply the default storage quota to every existing workspace
// @Description  Resolves the default storage quota and writes the effective byte quota to every existing workspace.
// @Tags         System Admin
// @Produce      json
// @Success      200 {object} handler.ApplyDefaultStorageQuotaToAllTenantsResponse "{ affected: int64, quota_bytes: int64 }"
// @Failure      500 {object} apperrors.AppError "DB write failed"
// @Router       /system/admin/tenants/apply-default-storage-quota [post]
func (h *SystemHandler) ApplyDefaultStorageQuotaToAllTenants(c *gin.Context) {
	ctx := logger.CloneContext(c.Request.Context())

	quotaBytes := service.ResolveDefaultTenantStorageQuota(ctx, h.systemSettingSvc)
	gb := float64(quotaBytes) / float64(1024*1024*1024)

	affected, err := h.tenantSvc.BulkSetStorageQuota(ctx, quotaBytes)
	if err != nil {
		logger.Errorf(ctx, "BulkSetStorageQuota failed: %v", err)
		writeAppError(c, apperrors.NewInternalServerError("Failed to apply storage quota"))
		return
	}

	if h.auditSvc != nil {
		actorID, _ := types.UserIDFromContext(ctx)
		details, _ := json.Marshal(map[string]any{
			"quota_bytes": quotaBytes,
			"quota_gb":    gb,
			"affected":    affected,
		})
		_ = h.auditSvc.Log(ctx, &types.AuditLog{
			TenantID:    0,
			ActorUserID: actorID,
			ActorRole:   systemAuditActorRole(ctx),
			Action:      types.AuditActionSystemSettingChanged,
			TargetType:  "tenant_storage_quota",
			TargetID:    "all",
			Outcome:     types.AuditOutcomeSuccess,
			Details:     types.JSON(details),
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"affected":    affected,
		"quota_bytes": quotaBytes,
		"quota_gb":    gb,
	})
}

// ResetSystemSetting godoc
// @Summary      Reset a system setting to ENV / built-in default
// @Description  Removes the DB override for :key so the 3-tier resolver falls back to ENV or built-in default.
// @Tags         System Admin
// @Param        key path string true "Setting key"
// @Success      200 {object} handler.SuccessResponse "Reset acknowledged"
// @Failure      400 {object} apperrors.AppError "Unknown key"
// @Failure      500 {object} apperrors.AppError "DB write failed"
// @Router       /system/admin/settings/{key} [delete]
func (h *SystemHandler) ResetSystemSetting(c *gin.Context) {
	ctx := logger.CloneContext(c.Request.Context())
	key := c.Param("key")
	if err := h.systemSettingSvc.Reset(ctx, key); err != nil {
		writeAppError(c, apperrors.NewBadRequestError(err.Error()))
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}
