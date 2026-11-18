package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Usir233/GENYEE/internal/application/service"
	"github.com/Usir233/GENYEE/internal/config"
	"github.com/Usir233/GENYEE/internal/database"
	apperrors "github.com/Usir233/GENYEE/internal/errors"
	"github.com/Usir233/GENYEE/internal/infrastructure/docparser"
	"github.com/Usir233/GENYEE/internal/logger"
	"github.com/Usir233/GENYEE/internal/runtime"
	"github.com/Usir233/GENYEE/internal/types"
	"github.com/Usir233/GENYEE/internal/types/interfaces"
	secutils "github.com/Usir233/GENYEE/internal/utils"
	"github.com/gin-gonic/gin"
	"github.com/neo4j/neo4j-go-driver/v6/neo4j"
)

type runtimeKnowledgeCanceller interface {
	CancelKnowledgeParse(ctx context.Context, knowledgeID string) (*types.Knowledge, error)
}

// SystemHandler handles system-related requests
type SystemHandler struct {
	documentProgress *service.DocumentProgressService
	cfg              *config.Config
	neo4jDriver      neo4j.Driver
	documentReader   interfaces.DocumentReader
	tenantSvc        interfaces.TenantService
	userSvc          interfaces.UserService
	modelCatalogSvc  *service.ModelCatalogService
	systemSettingSvc interfaces.SystemSettingService
	apiKeySvc        interfaces.TenantAPIKeyService
	// auditSvc is optional — when nil, emitAdminAudit no-ops so unit
	// tests that wire a partial container still compile. In production
	// the dig graph always provides one.
	auditSvc interfaces.AuditLogService
	// taskInspector backs the SystemAdmin runtime queue dashboard. Always
	// provided by the container (asynq-backed in Redis mode, a no-op in
	// Lite mode), so GetRuntimeQueues can distinguish "no queues in this
	// deployment" from "queues are empty".
	taskInspector interfaces.TaskInspector
	// knowledgeSvc supplies the domain-level cancellation path used by the
	// runtime task console. It updates business state and tracing before queue
	// records are removed, unlike a raw Redis deletion.
	knowledgeSvc runtimeKnowledgeCanceller
	// storageBackendSvc lets GetStorageEngineStatus report multi-instance
	// storage backends (Settings → Storage) as "available", not just the legacy
	// singleton tenant.StorageEngineConfig. Optional — nil in partially-wired
	// unit tests, in which case only the legacy config is consulted.
	storageBackendSvc interfaces.StorageBackendService
	sandboxConfigSvc  sandboxConfigService
	// startup snapshot for GET /system/capabilities; bound in router.NewRouter.
	deploymentCapabilities DeploymentCapabilitiesData
}

// NewSystemHandler creates a new system handler
func NewSystemHandler(
	cfg *config.Config,
	neo4jDriver neo4j.Driver,
	documentReader interfaces.DocumentReader,
	tenantSvc interfaces.TenantService,
	userSvc interfaces.UserService,
	systemSettingSvc interfaces.SystemSettingService,
	modelCatalogSvc *service.ModelCatalogService,
	apiKeySvc interfaces.TenantAPIKeyService,
	auditSvc interfaces.AuditLogService,
	taskInspector interfaces.TaskInspector,
	knowledgeSvc interfaces.KnowledgeService,
	storageBackendSvc interfaces.StorageBackendService,
	sandboxConfigSvc *service.TenantSandboxConfigService,
	documentProgress *service.DocumentProgressService,
) *SystemHandler {
	return &SystemHandler{
		documentProgress:  documentProgress,
		cfg:               cfg,
		neo4jDriver:       neo4jDriver,
		documentReader:    documentReader,
		tenantSvc:         tenantSvc,
		userSvc:           userSvc,
		systemSettingSvc:  systemSettingSvc,
		modelCatalogSvc:   modelCatalogSvc,
		apiKeySvc:         apiKeySvc,
		auditSvc:          auditSvc,
		taskInspector:     taskInspector,
		knowledgeSvc:      knowledgeSvc,
		storageBackendSvc: storageBackendSvc,
		sandboxConfigSvc:  sandboxConfigSvc,
	}
}

type platformAPIKeyCreateRequest struct {
	Name         string   `json:"name"`
	Capabilities []string `json:"capabilities"`
	ExpiresAt    *int64   `json:"expires_at_unix"`
}

// ListPlatformAPIKeys godoc
// @Summary      List platform API keys
// @Description  Lists non-workspace API keys. Full tokens are always masked. Human SystemAdmin only.
// @Tags         System Admin
// @Produce      json
// @Success      200 {object} handler.DataResponse{data=[]handler.tenantAPIKeyResponse}
// @Security     Bearer
// @Router       /system/admin/api-keys [get]
func (h *SystemHandler) ListPlatformAPIKeys(c *gin.Context) {
	keys, err := h.apiKeySvc.ListPlatformAPIKeys(c.Request.Context())
	if err != nil {
		writeAppError(c, apperrors.NewInternalServerError("Failed to list platform API keys").WithDetails(err.Error()))
		return
	}
	response := make([]tenantAPIKeyResponse, 0, len(keys))
	for _, key := range keys {
		item := tenantAPIKeyForResponse(key)
		item.APIKey = maskManagedAPIKey(key.APIKey)
		response = append(response, item)
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": response})
}

// CreatePlatformAPIKey godoc
// @Summary      Create a platform API key
// @Description  Creates a capability-scoped key that may target any workspace through X-Tenant-ID. The plaintext
// @Description  token is returned once. Human SystemAdmin only.
// @Tags         System Admin
// @Accept       json
// @Produce      json
// @Param        request body platformAPIKeyCreateRequest true "Platform API key"
// @Success      201 {object} handler.DataResponse{data=handler.tenantAPIKeyCreateResponse}
// @Security     Bearer
// @Router       /system/admin/api-keys [post]
func (h *SystemHandler) CreatePlatformAPIKey(c *gin.Context) {
	var req platformAPIKeyCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeAppError(c, apperrors.NewValidationError("Invalid request data").WithDetails(err.Error()))
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		writeAppError(c, apperrors.NewValidationError("name is required"))
		return
	}
	capabilities := types.NormalizeAPIKeyCapabilities(types.StringArray(req.Capabilities))
	if len(capabilities) == 0 || len(capabilities) != len(req.Capabilities) {
		writeAppError(c, apperrors.NewValidationError("valid capabilities are required"))
		return
	}
	var expiresAt *time.Time
	if req.ExpiresAt != nil {
		value := time.Unix(*req.ExpiresAt, 0).UTC()
		if !value.After(time.Now().UTC()) {
			writeAppError(c, apperrors.NewValidationError("expires_at_unix must be in the future"))
			return
		}
		expiresAt = &value
	}
	result, err := h.apiKeySvc.CreateAPIKey(c.Request.Context(), interfaces.TenantAPIKeyCreateRequest{
		ScopeType:    types.APIKeyScopePlatform,
		Name:         req.Name,
		Capabilities: []string(capabilities),
		ExpiresAt:    expiresAt,
	})
	if err != nil {
		writeAppError(
			c,
			apperrors.NewInternalServerError("Failed to create platform API key").WithDetails(err.Error()),
		)
		return
	}
	item := tenantAPIKeyForResponse(result.APIKey)
	item.APIKey = maskManagedAPIKey(result.Token)
	h.emitAPIKeyAudit(c.Request.Context(), types.AuditActionSystemAPIKeyCreated, result.APIKey)
	c.JSON(http.StatusCreated, gin.H{
		"success": true,
		"data":    tenantAPIKeyCreateResponse{tenantAPIKeyResponse: item, Token: result.Token},
	})
}

// DeletePlatformAPIKey godoc
// @Summary      Revoke a platform API key
// @Description  Immediately revokes a platform API key. Human SystemAdmin only.
// @Tags         System Admin
// @Produce      json
// @Param        key_id path int true "API key ID"
// @Success      200 {object} handler.SuccessResponse
// @Security     Bearer
// @Router       /system/admin/api-keys/{key_id} [delete]
func (h *SystemHandler) DeletePlatformAPIKey(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("key_id"), 10, 64)
	if err != nil || id == 0 {
		writeAppError(c, apperrors.NewBadRequestError("Invalid API key ID"))
		return
	}
	if err := h.apiKeySvc.RevokePlatformAPIKey(c.Request.Context(), id); err != nil {
		writeAppError(c, apperrors.NewNotFoundError("Platform API key not found"))
		return
	}
	h.emitAPIKeyAudit(c.Request.Context(), types.AuditActionSystemAPIKeyRevoked, &types.TenantAPIKey{ID: id})
	c.JSON(http.StatusOK, gin.H{"success": true})
}

func maskManagedAPIKey(token string) string {
	token = strings.TrimSpace(token)
	if len(token) <= 12 {
		return "***"
	}
	return token[:7] + "..." + token[len(token)-4:]
}

func (h *SystemHandler) emitAPIKeyAudit(ctx context.Context, action types.AuditAction, key *types.TenantAPIKey) {
	if h.auditSvc == nil || key == nil {
		return
	}
	actorID, _ := types.UserIDFromContext(ctx)
	details, _ := json.Marshal(map[string]any{
		"scope_type":   types.APIKeyScopePlatform,
		"capabilities": key.Capabilities,
	})
	_ = h.auditSvc.Log(ctx, &types.AuditLog{
		TenantID: 0, ActorUserID: actorID, ActorRole: systemAuditActorRole(ctx),
		Action: action, TargetType: "api_key", TargetID: strconv.FormatUint(key.ID, 10),
		Outcome: types.AuditOutcomeSuccess, Details: types.JSON(details),
	})
}

func systemAuditActorRole(ctx context.Context) string {
	if scope, ok := types.TenantAPIKeyScopeFromContext(ctx); ok && scope.IsPlatform() {
		return "platform_api_key"
	}
	return "system_admin"
}

// emitAdminAudit writes one audit row for a system-admin lifecycle event
// (promote / revoke). Best-effort — a nil audit service or a write
// failure does not bubble up to the caller. Mirrors the failure
// semantics of tenantMemberService.emitAudit and the system settings
// audit hook.
//
// `details` may be nil; the JSON `{}` default applies.
func (h *SystemHandler) emitAdminAudit(
	ctx context.Context,
	action types.AuditAction,
	target *types.User,
	details map[string]any,
) {
	if h.auditSvc == nil {
		return
	}
	actorID, _ := types.UserIDFromContext(ctx)
	var detailsJSON types.JSON
	if details != nil {
		if b, err := json.Marshal(details); err == nil {
			detailsJSON = types.JSON(b)
		}
	}
	entry := &types.AuditLog{
		// tenant_id=0 marks the row as system-scope. The audit_logs
		// table is tenant-scoped; 0 is the convention for platform-wide
		// events (matches AuditActionSystemSettingChanged).
		TenantID:    0,
		ActorUserID: actorID,
		ActorRole:   systemAuditActorRole(ctx),
		Action:      action,
		TargetType:  "user",
		Outcome:     types.AuditOutcomeSuccess,
		Details:     detailsJSON,
	}
	if target != nil {
		entry.TargetID = target.ID
		entry.TargetUserID = target.ID
	}
	_ = h.auditSvc.Log(ctx, entry)
}

// GetSystemInfoResponse defines the response structure for system info
type GetSystemInfoResponse struct {
	Version             string `json:"version" extensions:"x-agent"`
	Edition             string `json:"edition" extensions:"x-agent"`
	CommitID            string `json:"commit_id,omitempty" extensions:"x-agent"`
	BuildTime           string `json:"build_time,omitempty" extensions:"x-agent"`
	GoVersion           string `json:"go_version,omitempty" extensions:"x-agent"`
	DocReaderVersion    string `json:"docreader_version,omitempty" extensions:"x-agent"`
	DocReaderCommitID   string `json:"docreader_commit_id,omitempty" extensions:"x-agent"`
	KeywordIndexEngine  string `json:"keyword_index_engine,omitempty" extensions:"x-agent"`
	VectorStoreEngine   string `json:"vector_store_engine,omitempty" extensions:"x-agent"`
	GraphDatabaseEngine string `json:"graph_database_engine,omitempty" extensions:"x-agent"`
	MinioEnabled        bool   `json:"minio_enabled,omitempty" extensions:"x-agent"`
	DBVersion           string `json:"db_version,omitempty" extensions:"x-agent"`
	DBMigrationFailed   bool   `json:"db_migration_failed" extensions:"x-agent"`
	// DBMigrationError carries the human-readable error message recorded when
	// the most recent startup migration attempt failed. Empty when migrations
	// succeeded; non-empty values let the frontend surface a troubleshooting
	// banner instead of silently hiding the DB version row (see issue #1319).
	DBMigrationError string `json:"db_migration_error,omitempty"`
	// StartedAt is the server process boot time (RFC3339, UTC).
	StartedAt string `json:"started_at,omitempty" extensions:"x-agent"`
	// UptimeSeconds is seconds elapsed since process start.
	UptimeSeconds int64 `json:"uptime_seconds,omitempty" extensions:"x-agent"`
}

// 编译时注入的版本信息
var (
	Version   = "unknown"
	Edition   = "standard"
	CommitID  = "unknown"
	BuildTime = "unknown"
	GoVersion = "unknown"
)

// GetSystemInfo godoc
// @Summary      获取系统信息
// @Description  获取系统版本、构建信息和引擎配置
// @Tags         系统
// @Accept       json
// @Produce      json
// @Success      200  {object}  DataResponse{data=GetSystemInfoResponse}  "系统信息"
// @x-agent {"id":"system.get_info","risk":"read"}
// @x-agent-hint "Reads the backend version, edition, build, runtime, database migration status and configured engines. Docreader build information is omitted when its service is unavailable or does not support GetInfo. Empty or unknown means unavailable information. Engine configuration does not prove connectivity. Raw migration errors are omitted. Frontend build/version is separate and is not reported by this API; do not infer frontend/backend version agreement or available upgrades."
// @Router       /system/info [get]
func (h *SystemHandler) GetSystemInfo(c *gin.Context) {
	ctx := logger.CloneContext(c.Request.Context())

	keywordIndexEngine := h.getKeywordIndexEngine()
	vectorStoreEngine := h.getVectorStoreEngine()
	graphDatabaseEngine := h.getGraphDatabaseEngine()
	minioEnabled := h.isMinioConfigured(c)

	dbMigrationErr := database.CachedMigrationError()
	var dbVersion string
	if ver, dirty, ok := database.CachedMigrationVersion(); ok {
		dbVersion = fmt.Sprintf("%d", ver)
		if dirty {
			dbVersion += " (dirty)"
		}
		if dbMigrationErr != "" {
			dbVersion += " (failed)"
		}
	} else if dbMigrationErr != "" {
		// Failure happened before m.Version() could be read (e.g. could not
		// open the database). Still emit a placeholder so the frontend renders
		// the row and shows the troubleshooting banner.
		dbVersion = "unknown"
	}

	var startedAt string
	var uptimeSec int64
	if boot := runtime.ServerStartedAt(); !boot.IsZero() {
		startedAt = boot.UTC().Format(time.RFC3339)
		uptimeSec = int64(runtime.ServerUptime().Seconds())
	}

	var docReaderVersion, docReaderCommitID string
	if reader, ok := h.documentReader.(interface {
		GetVersionInfo(context.Context) (string, string, error)
	}); ok {
		// A disconnected or older docreader must not fail system/info.
		versionCtx, cancel := context.WithTimeout(ctx, time.Second)
		version, commitID, err := reader.GetVersionInfo(versionCtx)
		cancel()
		if err == nil {
			docReaderVersion, docReaderCommitID = version, commitID
		}
	}

	response := GetSystemInfoResponse{
		Version:             Version,
		Edition:             Edition,
		CommitID:            CommitID,
		BuildTime:           BuildTime,
		GoVersion:           GoVersion,
		DocReaderVersion:    docReaderVersion,
		DocReaderCommitID:   docReaderCommitID,
		KeywordIndexEngine:  keywordIndexEngine,
		VectorStoreEngine:   vectorStoreEngine,
		GraphDatabaseEngine: graphDatabaseEngine,
		MinioEnabled:        minioEnabled,
		DBVersion:           dbVersion,
		DBMigrationError:    dbMigrationErr,
		DBMigrationFailed:   dbMigrationErr != "",
		StartedAt:           startedAt,
		UptimeSeconds:       uptimeSec,
	}

	logger.Info(ctx, "System info retrieved successfully")
	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"msg":  "success",
		"data": response,
	})
}

func (h *SystemHandler) getDocReaderConnInfo() (addr, transport string) {
	addr = strings.TrimSpace(os.Getenv("DOCREADER_ADDR"))
	transport = strings.TrimSpace(os.Getenv("DOCREADER_TRANSPORT"))
	if transport == "" {
		transport = "grpc"
	}
	transport = strings.ToLower(transport)
	return addr, transport
}

// ListParserEngines returns available document parser engines.
// Merges Go-native static engines with engines discovered from the remote
// docreader service, so newly added Python engines are auto-discovered.
// @Summary      列出可用的文档解析引擎
// @Tags         系统
// @Produce      json
// @Success      200  {object}  handler.ListParserEnginesResponse  "解析引擎列表"
// @x-agent {"id":"knowledge_base.list_parser_engines","risk":"read"}
// @x-agent-hint "Lists parser engine names, supported file extensions and availability. Choose an available engine supporting every requested file type before setting parser_engine in knowledge_base.update. Never guess an engine name."
// @Router       /system/parser-engines [get]
func (h *SystemHandler) ListParserEngines(c *gin.Context) {
	var overrides map[string]string
	if v, exists := c.Get(types.TenantInfoContextKey.String()); exists {
		if tenant, ok := v.(*types.Tenant); ok && tenant != nil && tenant.ParserEngineConfig != nil {
			overrides = tenant.ParserEngineConfig.ToOverridesMap()
		}
	}

	reader, docreaderAddr, docreaderTransport := h.resolveDocReader()
	connected := reader != nil && reader.IsConnected()
	remoteEngines := h.fetchRemoteEngines(c.Request.Context(), reader, overrides)
	engines := docparser.ListAllEngines(connected, overrides, remoteEngines)
	c.JSON(http.StatusOK, gin.H{
		"code":                0,
		"msg":                 "success",
		"data":                engines,
		"docreader_addr":      docreaderAddr,
		"docreader_transport": docreaderTransport,
		"connected":           connected,
	})
}

// ReconnectDocReader reconnects the document converter to a new (or same) DocReader address.
// @Summary      重连文档解析服务
// @Tags         系统
// @Accept       json
// @Produce      json
// @Param        request  body  object{addr string} true "DocReader 地址"
// @Success      200
// @Router       /system/docreader/reconnect [post]
func (h *SystemHandler) ReconnectDocReader(c *gin.Context) {
	var req struct {
		Addr string `json:"addr" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		writeAppError(c, apperrors.NewBadRequestError("请提供 addr 参数"))
		return
	}
	addr := strings.TrimSpace(req.Addr)
	if addr == "" {
		writeAppError(c, apperrors.NewBadRequestError("addr 不能为空"))
		return
	}

	configuredAddr, docreaderTransport := h.getDocReaderConnInfo()
	isConfiguredTarget := configuredAddr != "" && strings.EqualFold(addr, configuredAddr)

	// SSRF validation for docreader address
	if !isConfiguredTarget {
		if err := secutils.ValidateURLForSSRF(addr); err != nil {
			logger.Warnf(c.Request.Context(), "SSRF validation failed for docreader addr: %v", err)
			writeAppError(c, apperrors.NewBadRequestError(secutils.FormatSSRFError("DocReader 地址", addr, err)))
			return
		}
	}

	var overrides map[string]string
	if v, exists := c.Get(types.TenantInfoContextKey.String()); exists {
		if tenant, ok := v.(*types.Tenant); ok && tenant != nil && tenant.ParserEngineConfig != nil {
			overrides = tenant.ParserEngineConfig.ToOverridesMap()
		}
	}

	// Probe connectivity with an isolated temporary client that enforces
	// connection-level SSRF dialing and never attaches global GRPC_AUTH_TOKEN to
	// non-preconfigured external hosts, keeping the shared singleton untouched.
	probeReader, err := docparser.NewProbeGRPCDocumentReader(addr)
	if err != nil {
		logger.Errorf(c.Request.Context(), "Failed to create probe docreader for %s: %v", addr, err)
		c.JSON(http.StatusOK, gin.H{"code": 1, "msg": fmt.Sprintf("连接失败: %v", err)})
		return
	}
	defer func() { _ = probeReader.Close() }()

	probeCtx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	remoteEngines, err := probeReader.ListEngines(probeCtx, overrides)
	if err != nil {
		logger.Warnf(c.Request.Context(), "DocReader probe failed for %s: %v", addr, err)
		c.JSON(http.StatusOK, gin.H{"code": 1, "msg": fmt.Sprintf("连接失败: %v", err)})
		return
	}

	if isConfiguredTarget && h.documentReader != nil {
		if err := h.documentReader.Reconnect(addr); err != nil {
			logger.Errorf(c.Request.Context(), "Failed to reconnect docreader to %s: %v", addr, err)
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": fmt.Sprintf("连接失败: %v", err)})
			return
		}
	}

	engines := docparser.ListAllEngines(true, overrides, remoteEngines)
	c.JSON(http.StatusOK, gin.H{
		"code":                0,
		"msg":                 "连接成功",
		"data":                engines,
		"docreader_addr":      addr,
		"docreader_transport": docreaderTransport,
		"connected":           true,
	})
}

// CheckParserEngines runs availability check with the given config overrides (e.g. current form values).
// Used to test engine availability without saving; body shape matches ParserEngineConfig.
// @Summary      使用当前参数检测解析引擎可用性
// @Tags         系统
// @Accept       json
// @Produce      json
// @Param        body  body  object  true  "解析引擎配置（与保存接口同结构）"
// @Success      200
// @Router       /system/parser-engines/check [post]
func (h *SystemHandler) CheckParserEngines(c *gin.Context) {
	var body types.ParserEngineConfig
	if err := c.ShouldBindJSON(&body); err != nil {
		writeAppError(c, apperrors.NewBadRequestError("请求体格式错误"))
		return
	}
	var existing *types.ParserEngineConfig
	if v, exists := c.Get(types.TenantInfoContextKey.String()); exists {
		if t, ok := v.(*types.Tenant); ok && t != nil {
			existing = t.ParserEngineConfig
		}
	}
	merged := types.MergeParserEngineConfigForUpdate(&body, existing)
	overrides := merged.ToOverridesMap()

	reader, docreaderAddr, docreaderTransport := h.resolveDocReader()
	connected := reader != nil && reader.IsConnected()
	remoteEngines := h.fetchRemoteEngines(c.Request.Context(), reader, overrides)
	engines := docparser.ListAllEngines(connected, overrides, remoteEngines)
	c.JSON(http.StatusOK, gin.H{
		"code":                0,
		"msg":                 "success",
		"data":                engines,
		"docreader_addr":      docreaderAddr,
		"docreader_transport": docreaderTransport,
		"connected":           connected,
	})
}

func (h *SystemHandler) resolveDocReader() (interfaces.DocumentReader, string, string) {
	addr, transport := h.getDocReaderConnInfo()
	return h.documentReader, addr, transport
}

// ResolveDocumentReader returns the configured DocumentReader or creates an HTTP reader for addr.
func (h *SystemHandler) ResolveDocumentReader(_ context.Context, addr string) interfaces.DocumentReader {
	if addr == "" {
		return h.documentReader
	}
	reader, _ := docparser.NewHTTPDocumentReader(addr)
	return reader
}

// fetchRemoteEngines queries the remote docreader for its engine list.
// Returns nil on any error (e.g. not connected), letting the caller
// fall back to Go's static registry only.
func (h *SystemHandler) fetchRemoteEngines(
	ctx context.Context,
	reader interfaces.DocumentReader,
	overrides map[string]string,
) []types.ParserEngineInfo {
	if reader == nil || !reader.IsConnected() {
		return nil
	}
	engines, err := reader.ListEngines(ctx, overrides)
	if err != nil {
		logger.Warnf(ctx, "Failed to fetch remote engines from docreader: %v", err)
		return nil
	}
	return engines
}

// getKeywordIndexEngine returns the keyword index engine name
func (h *SystemHandler) getKeywordIndexEngine() string {
	retrieveDriver := os.Getenv("RETRIEVE_DRIVER")
	if retrieveDriver == "" {
		return "未配置"
	}

	drivers := strings.Split(retrieveDriver, ",")
	keywordEngines := []string{}
	for _, driver := range drivers {
		driver = strings.TrimSpace(driver)
		if h.supportsRetrieverType(driver, types.KeywordsRetrieverType) {
			keywordEngines = append(keywordEngines, driver)
		}
	}

	if len(keywordEngines) == 0 {
		return "未配置"
	}
	return strings.Join(keywordEngines, ", ")
}

// getVectorStoreEngine returns the vector store engine name
func (h *SystemHandler) getVectorStoreEngine() string {
	if h.cfg != nil && h.cfg.VectorDatabase != nil && h.cfg.VectorDatabase.Driver != "" {
		return h.cfg.VectorDatabase.Driver
	}

	retrieveDriver := os.Getenv("RETRIEVE_DRIVER")
	if retrieveDriver == "" {
		return "未配置"
	}

	drivers := strings.Split(retrieveDriver, ",")
	vectorEngines := []string{}
	for _, driver := range drivers {
		driver = strings.TrimSpace(driver)
		if h.supportsRetrieverType(driver, types.VectorRetrieverType) {
			vectorEngines = append(vectorEngines, driver)
		}
	}

	if len(vectorEngines) == 0 {
		return "未配置"
	}
	return strings.Join(vectorEngines, ", ")
}

// getGraphDatabaseEngine returns the graph database engine name
func (h *SystemHandler) getGraphDatabaseEngine() string {
	if h.neo4jDriver == nil {
		return "Not Enabled"
	}
	return "Neo4j"
}

// supportsRetrieverType checks if a driver supports a specific retriever type
// by looking up the retrieverEngineMapping from types package
func (h *SystemHandler) supportsRetrieverType(driver string, retrieverType types.RetrieverType) bool {
	mapping := types.GetRetrieverEngineMapping()
	engines, exists := mapping[driver]
	if !exists {
		return false
	}
	for _, engine := range engines {
		if engine.RetrieverType == retrieverType {
			return true
		}
	}
	return false
}
