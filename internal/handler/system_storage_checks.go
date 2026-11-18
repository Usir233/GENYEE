package handler

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"

	"github.com/Usir233/GENYEE/internal/application/service/file"
	apperrors "github.com/Usir233/GENYEE/internal/errors"
	"github.com/Usir233/GENYEE/internal/logger"
	"github.com/Usir233/GENYEE/internal/types"
	secutils "github.com/Usir233/GENYEE/internal/utils"
	"github.com/gin-gonic/gin"
)

// getMinioConfig resolves MinIO connection parameters from tenant config (if mode=remote) or env vars.
func (h *SystemHandler) getMinioConfig(c *gin.Context) (endpoint, accessKeyID, secretAccessKey string) {
	if v, exists := c.Get(types.TenantInfoContextKey.String()); exists {
		if tenant, ok := v.(*types.Tenant); ok && tenant != nil &&
			tenant.StorageEngineConfig != nil && tenant.StorageEngineConfig.MinIO != nil {
			m := tenant.StorageEngineConfig.MinIO
			if m.Mode == "remote" {
				return m.Endpoint, m.AccessKeyID, m.SecretAccessKey
			}
		}
	}
	endpoint = os.Getenv("MINIO_ENDPOINT")
	accessKeyID = os.Getenv("MINIO_ACCESS_KEY_ID")
	secretAccessKey = os.Getenv("MINIO_SECRET_ACCESS_KEY")
	return
}

// activeBackendProviders returns the set of storage providers that have at
// least one active multi-instance backend registered for the caller's
// workspace. Used to keep GetStorageEngineStatus in sync with the new Storage
// settings UI, which writes to storage_backends rather than the legacy
// tenant.StorageEngineConfig singleton.
func (h *SystemHandler) activeBackendProviders(c *gin.Context) map[string]bool {
	result := map[string]bool{}
	if h.storageBackendSvc == nil {
		return result
	}
	v, exists := c.Get(types.TenantInfoContextKey.String())
	if !exists {
		return result
	}
	tenant, ok := v.(*types.Tenant)
	if !ok || tenant == nil {
		return result
	}
	backends, err := h.storageBackendSvc.List(c.Request.Context(), tenant.ID)
	if err != nil {
		logger.Warnf(
			c.Request.Context(),
			"[storage] list backends for status failed: tenant=%d err=%v",
			tenant.ID,
			err,
		)
		return result
	}
	for _, backend := range backends {
		if backend == nil || backend.Status != types.StorageBackendStatusActive {
			continue
		}
		result[strings.ToLower(strings.TrimSpace(backend.Provider))] = true
	}
	return result
}

// isMinioConfigured checks whether MinIO connection info is available (from tenant config or env).
func (h *SystemHandler) isMinioConfigured(c *gin.Context) bool {
	endpoint, accessKeyID, secretAccessKey := h.getMinioConfig(c)
	return endpoint != "" && accessKeyID != "" && secretAccessKey != ""
}

// isMinioEnvAvailable checks whether MinIO env vars (MINIO_ENDPOINT etc.) are set.
func (h *SystemHandler) isMinioEnvAvailable() bool {
	return os.Getenv("MINIO_ENDPOINT") != "" &&
		os.Getenv("MINIO_ACCESS_KEY_ID") != "" &&
		os.Getenv("MINIO_SECRET_ACCESS_KEY") != ""
}

// isCOSConfigured checks whether COS connection info is available from tenant config.
func (h *SystemHandler) isCOSConfigured(c *gin.Context) bool {
	if v, exists := c.Get(types.TenantInfoContextKey.String()); exists {
		if tenant, ok := v.(*types.Tenant); ok && tenant != nil &&
			tenant.StorageEngineConfig != nil && tenant.StorageEngineConfig.COS != nil {
			cosConf := tenant.StorageEngineConfig.COS
			return cosConf.SecretID != "" && cosConf.SecretKey != "" &&
				cosConf.Region != "" && cosConf.BucketName != ""
		}
	}
	return false
}

// isTOSConfigured checks whether TOS connection info is available from tenant config or env.
func (h *SystemHandler) isTOSConfigured(c *gin.Context) bool {
	if v, exists := c.Get(types.TenantInfoContextKey.String()); exists {
		if tenant, ok := v.(*types.Tenant); ok && tenant != nil &&
			tenant.StorageEngineConfig != nil && tenant.StorageEngineConfig.TOS != nil {
			tosConf := tenant.StorageEngineConfig.TOS
			return tosConf.Endpoint != "" && tosConf.Region != "" &&
				tosConf.AccessKey != "" && tosConf.SecretKey != "" && tosConf.BucketName != ""
		}
	}
	return h.isTOSEnvAvailable()
}

// isOSSConfigured checks whether OSS connection info is available from tenant config.
func (h *SystemHandler) isOSSConfigured(c *gin.Context) bool {
	if v, exists := c.Get(types.TenantInfoContextKey.String()); exists {
		if tenant, ok := v.(*types.Tenant); ok && tenant != nil &&
			tenant.StorageEngineConfig != nil && tenant.StorageEngineConfig.OSS != nil {
			ossConf := tenant.StorageEngineConfig.OSS
			return ossConf.Endpoint != "" && ossConf.Region != "" &&
				ossConf.AccessKey != "" && ossConf.SecretKey != "" && ossConf.BucketName != ""
		}
	}
	return false
}

// isKS3Configured checks whether KS3 connection info is available from tenant config.
func (h *SystemHandler) isKS3Configured(c *gin.Context) bool {
	if v, exists := c.Get(types.TenantInfoContextKey.String()); exists {
		if tenant, ok := v.(*types.Tenant); ok && tenant != nil &&
			tenant.StorageEngineConfig != nil && tenant.StorageEngineConfig.KS3 != nil {
			ks3Conf := tenant.StorageEngineConfig.KS3
			return ks3Conf.Endpoint != "" && ks3Conf.Region != "" &&
				ks3Conf.AccessKey != "" && ks3Conf.SecretKey != "" && ks3Conf.BucketName != ""
		}
	}
	return false
}

// isOBSConfigured checks whether OBS connection info is available from tenant config or env.
func (h *SystemHandler) isOBSConfigured(c *gin.Context) bool {
	if v, exists := c.Get(types.TenantInfoContextKey.String()); exists {
		if tenant, ok := v.(*types.Tenant); ok && tenant != nil &&
			tenant.StorageEngineConfig != nil && tenant.StorageEngineConfig.OBS != nil {
			obsConf := tenant.StorageEngineConfig.OBS
			return obsConf.Endpoint != "" && obsConf.Region != "" &&
				obsConf.AccessKey != "" && obsConf.SecretKey != "" && obsConf.BucketName != ""
		}
	}
	return h.isOBSEnvAvailable()
}

// isTOSEnvAvailable checks whether TOS env vars are set.
func (h *SystemHandler) isTOSEnvAvailable() bool {
	return os.Getenv("TOS_ENDPOINT") != "" &&
		os.Getenv("TOS_REGION") != "" &&
		os.Getenv("TOS_ACCESS_KEY") != "" &&
		os.Getenv("TOS_SECRET_KEY") != "" &&
		os.Getenv("TOS_BUCKET_NAME") != ""
}

// isOBSEnvAvailable checks whether OBS env vars are set.
func (h *SystemHandler) isOBSEnvAvailable() bool {
	return os.Getenv("OBS_ENDPOINT") != "" &&
		os.Getenv("OBS_REGION") != "" &&
		os.Getenv("OBS_ACCESS_KEY") != "" &&
		os.Getenv("OBS_SECRET_KEY") != "" &&
		os.Getenv("OBS_BUCKET_NAME") != ""
}

// StorageEngineStatusItem describes one storage engine's availability and description.
type StorageEngineStatusItem struct {
	Name        string `json:"name"` // "local", "minio", "cos", "tos", "s3", "oss", "ks3", "obs"
	Allowed     bool   `json:"allowed"`
	Available   bool   `json:"available"`   // whether the engine can be used
	Description string `json:"description"` // short description for UI
}

// GetStorageEngineStatusResponse is the response for GET /system/storage-engine-status.
type GetStorageEngineStatusResponse struct {
	Engines           []StorageEngineStatusItem `json:"engines"`
	AllowedProviders  []string                  `json:"allowed_providers"`
	MinioEnvAvailable bool                      `json:"minio_env_available"`
}

// GetStorageEngineStatus godoc
// @Summary      获取存储引擎状态
// @Description  返回 Local、MinIO、COS 各存储引擎的可用状态及说明，供全局设置与知识库选择使用
// @Tags         系统
// @Produce      json
// @Success      200  {object}  GetStorageEngineStatusResponse
// @Router       /system/storage-engine-status [get]
func (h *SystemHandler) GetStorageEngineStatus(c *gin.Context) {
	activeBackend := h.activeBackendProviders(c)
	minioConfigured := h.isMinioConfigured(c) || activeBackend["minio"]
	minioEnvAvailable := h.isMinioEnvAvailable()
	cosConfigured := h.isCOSConfigured(c) || activeBackend["cos"]
	tosConfigured := h.isTOSConfigured(c) || activeBackend["tos"]
	s3Configured := h.isS3Configured(c) || activeBackend["s3"]
	ossConfigured := h.isOSSConfigured(c) || activeBackend["oss"]
	ks3Configured := h.isKS3Configured(c) || activeBackend["ks3"]
	obsConfigured := h.isOBSConfigured(c) || activeBackend["obs"]
	allowed := getAllowedStorageProviders()
	allowedProviders := make([]string, 0, len(getSupportedStorageProviders()))
	for _, provider := range getSupportedStorageProviders() {
		if allowed[provider] {
			allowedProviders = append(allowedProviders, provider)
		}
	}
	engines := []StorageEngineStatusItem{
		{
			Name:        "local",
			Allowed:     allowed["local"],
			Available:   true,
			Description: "本地文件系统存储，仅适合单机部署",
		},
		{
			Name:        "minio",
			Allowed:     allowed["minio"],
			Available:   minioConfigured || minioEnvAvailable,
			Description: "S3 兼容的自托管对象存储，适合内网和私有云部署",
		},
		{
			Name:        "cos",
			Allowed:     allowed["cos"],
			Available:   cosConfigured,
			Description: "腾讯云对象存储服务，适合公有云部署，支持 CDN 加速",
		},
		{
			Name:        "tos",
			Allowed:     allowed["tos"],
			Available:   tosConfigured,
			Description: "火山引擎对象存储服务，适合公有云部署",
		},
		{
			Name:        "s3",
			Allowed:     allowed["s3"],
			Available:   s3Configured,
			Description: "AWS S3 与兼容对象存储服务，适合公有云与混合云部署",
		},
		{
			Name:        "oss",
			Allowed:     allowed["oss"],
			Available:   ossConfigured,
			Description: "阿里云对象存储服务，适合公有云部署，支持 S3 兼容协议",
		},
		{
			Name:        "ks3",
			Allowed:     allowed["ks3"],
			Available:   ks3Configured,
			Description: "金山云对象存储服务，适合公有云部署",
		},
		{
			Name:        "obs",
			Allowed:     allowed["obs"],
			Available:   obsConfigured,
			Description: "华为云对象存储服务，适合公有云部署",
		},
	}
	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"msg":  "success",
		"data": GetStorageEngineStatusResponse{
			Engines:           engines,
			AllowedProviders:  allowedProviders,
			MinioEnvAvailable: minioEnvAvailable,
		},
	})
}

// cosFieldPattern validates COS region and bucket name format to prevent URL injection.
var cosFieldPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,62}$`)

// ossFieldPattern validates OSS region and bucket name format to prevent URL injection.
var ossFieldPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,62}$`)

func sanitizeStorageCheckError(err error) string {
	return secutils.SanitizeStorageConnectivityError(err)
}

func storageEndpointHost(endpoint string) string {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return ""
	}
	if strings.Contains(endpoint, "://") {
		if u, err := url.Parse(endpoint); err == nil {
			if h := u.Hostname(); h != "" {
				return h
			}
		}
	}
	if host, _, err := net.SplitHostPort(endpoint); err == nil {
		return host
	}
	return endpoint
}

func isBlockedStorageEndpoint(endpoint string) (bool, string) {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return true, "无效的地址"
	}
	if err := secutils.ValidateURLForSSRF(endpoint); err != nil {
		return true, secutils.FormatSSRFError("存储 Endpoint", endpoint, err)
	}
	return false, ""
}

// StorageCheckRequest is the body for POST /system/storage-engine-check.
type StorageCheckRequest struct {
	Provider string                   `json:"provider"` // "minio", "cos", "tos", "s3", "oss", "ks3", "obs"
	MinIO    *types.MinIOEngineConfig `json:"minio,omitempty"`
	COS      *types.COSEngineConfig   `json:"cos,omitempty"`
	TOS      *types.TOSEngineConfig   `json:"tos,omitempty"`
	S3       *types.S3EngineConfig    `json:"s3,omitempty"`
	OSS      *types.OSSEngineConfig   `json:"oss,omitempty"`
	KS3      *types.KS3EngineConfig   `json:"ks3,omitempty"`
	OBS      *types.OBSEngineConfig   `json:"obs,omitempty"`
}

// StorageCheckResponse is the response for a single-engine connectivity check.
type StorageCheckResponse struct {
	OK            bool   `json:"ok"`
	Message       string `json:"message"`
	BucketCreated bool   `json:"bucket_created,omitempty"`
}

// CheckStorageEngine tests connectivity for a single storage engine using the provided config.
// @Summary      测试存储引擎连通性
// @Description  使用当前填写的参数测试 MinIO/COS 连通性，不保存配置
// @Tags         系统
// @Accept       json
// @Produce      json
// @Param        body  body  StorageCheckRequest  true  "存储引擎配置"
// @Success      200   {object}  StorageCheckResponse
// @Router       /system/storage-engine-check [post]
func (h *SystemHandler) CheckStorageEngine(c *gin.Context) {
	ctx := logger.CloneContext(c.Request.Context())

	var req StorageCheckRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeAppError(c, apperrors.NewBadRequestError("请求体格式错误"))
		return
	}
	if !isStorageProviderAllowed(req.Provider) {
		writeAppError(c, apperrors.NewForbiddenError("该存储引擎已被禁用"))
		return
	}

	switch req.Provider {
	case "minio":
		h.checkMinio(ctx, c, req.MinIO)
	case "cos":
		h.checkCOS(ctx, c, req.COS)
	case "tos":
		h.checkTOS(ctx, c, req.TOS)
	case "s3":
		h.checkS3(ctx, c, req.S3)
	case "oss":
		h.checkOSS(ctx, c, req.OSS)
	case "ks3":
		h.checkKS3(ctx, c, req.KS3)
	case "obs":
		h.checkOBS(ctx, c, req.OBS)
	default:
		c.JSON(http.StatusOK, gin.H{"code": 0, "data": StorageCheckResponse{OK: true, Message: "本地存储无需检测"}})
	}
}

func (h *SystemHandler) isS3Configured(c *gin.Context) bool {
	if v, exists := c.Get(types.TenantInfoContextKey.String()); exists {
		if tenant, ok := v.(*types.Tenant); ok && tenant != nil &&
			tenant.StorageEngineConfig != nil && tenant.StorageEngineConfig.S3 != nil {
			s3Conf := tenant.StorageEngineConfig.S3
			return s3Conf.Region != "" && s3Conf.BucketName != "" &&
				(s3Conf.AccessKey == "") == (s3Conf.SecretKey == "")
		}
	}
	return false
}

func writeStorageCheck(c *gin.Context, resp StorageCheckResponse) {
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": resp})
}

func writeStorageCheckErr(c *gin.Context, msg string) {
	writeStorageCheck(c, StorageCheckResponse{OK: false, Message: msg})
}

func writeStorageCheckOK(c *gin.Context, bucket string) {
	writeStorageCheck(c, StorageCheckResponse{
		OK:      true,
		Message: fmt.Sprintf("连接成功，Bucket「%s」已确认存在", bucket),
	})
}

func (h *SystemHandler) checkMinio(ctx context.Context, c *gin.Context, cfg *types.MinIOEngineConfig) {
	if cfg == nil {
		writeStorageCheckErr(c, "未提供 MinIO 配置")
		return
	}
	if cfg.BucketName != "" && !cosFieldPattern.MatchString(cfg.BucketName) {
		writeStorageCheckErr(c, "Bucket 名称格式不正确，仅允许字母、数字、点、连字符")
		return
	}

	endpoint, accessKeyID, secretAccessKey := cfg.Endpoint, cfg.AccessKeyID, cfg.SecretAccessKey
	if cfg.Mode != "remote" {
		endpoint = os.Getenv("MINIO_ENDPOINT")
		accessKeyID = os.Getenv("MINIO_ACCESS_KEY_ID")
		secretAccessKey = os.Getenv("MINIO_SECRET_ACCESS_KEY")
	}
	if endpoint == "" || accessKeyID == "" || secretAccessKey == "" {
		writeStorageCheckErr(c, "Endpoint、Access Key、Secret Key 不能为空")
		return
	}
	if cfg.Mode == "remote" {
		if blocked, reason := isBlockedStorageEndpoint(endpoint); blocked {
			logger.Warnf(ctx, "Storage check: MinIO endpoint blocked by SSRF protection, endpoint: %s", endpoint)
			writeStorageCheckErr(c, reason)
			return
		}
	}

	err := file.CheckMinioConnectivity(ctx, endpoint, accessKeyID, secretAccessKey, cfg.BucketName, cfg.UseSSL)
	if err != nil {
		if strings.Contains(err.Error(), "does not exist") && cfg.BucketName != "" {
			logger.Info(ctx, "Storage check: bucket does not exist, attempting auto-creation", "bucket", cfg.BucketName)
			_, mkErr := file.NewMinioFileService(endpoint, accessKeyID, secretAccessKey, cfg.BucketName, cfg.UseSSL)
			if mkErr != nil {
				logger.Error(ctx, "Storage check: failed to create bucket", "bucket", cfg.BucketName, "error", mkErr)
				msg := fmt.Sprintf(
					"Failed to auto-create Bucket '%s': %s", cfg.BucketName, sanitizeStorageCheckError(mkErr),
				)
				writeStorageCheckErr(c, msg)
				return
			}
			logger.Info(ctx, "Storage check: bucket created", "bucket", cfg.BucketName)
			msg := fmt.Sprintf("Bucket '%s' does not exist, and has been automatically created", cfg.BucketName)
			writeStorageCheck(c, StorageCheckResponse{OK: true, BucketCreated: true, Message: msg})
			return
		}
		logger.Error(ctx, "Storage check: MinIO connectivity failed", "error", err)
		writeStorageCheckErr(c, sanitizeStorageCheckError(err))
		return
	}

	if cfg.BucketName != "" {
		writeStorageCheckOK(c, cfg.BucketName)
		return
	}
	writeStorageCheck(c, StorageCheckResponse{OK: true, Message: "连接成功"})
}

func (h *SystemHandler) checkCOS(ctx context.Context, c *gin.Context, cfg *types.COSEngineConfig) {
	if cfg == nil {
		writeStorageCheckErr(c, "未提供 COS 配置")
		return
	}
	if cfg.SecretID == "" || cfg.SecretKey == "" || cfg.Region == "" || cfg.BucketName == "" {
		writeStorageCheckErr(c, "Secret ID、Secret Key、Region、Bucket 名称不能为空")
		return
	}
	if !cosFieldPattern.MatchString(cfg.Region) {
		writeStorageCheckErr(c, "Region 格式不正确，仅允许字母、数字、点、连字符")
		return
	}
	if !cosFieldPattern.MatchString(cfg.BucketName) {
		writeStorageCheckErr(c, "Bucket 名称格式不正确，仅允许字母、数字、点、连字符")
		return
	}

	err := file.CheckCosConnectivity(ctx, cfg.BucketName, cfg.Region, cfg.SecretID, cfg.SecretKey)
	if err != nil {
		logger.Errorf(ctx, "Storage check: COS connectivity failed, bucket: %s, error: %v", cfg.BucketName, err)
		errMsg := err.Error()
		if strings.Contains(errMsg, "403") {
			writeStorageCheckErr(c, "认证失败，请检查 Secret ID / Secret Key 是否正确")
			return
		}
		if strings.Contains(errMsg, "404") || strings.Contains(errMsg, "NoSuchBucket") {
			writeStorageCheckErr(c, fmt.Sprintf("Bucket「%s」不存在，请检查名称和 Region", cfg.BucketName))
			return
		}
		writeStorageCheckErr(c, sanitizeStorageCheckError(err))
		return
	}
	writeStorageCheckOK(c, cfg.BucketName)
}

func (h *SystemHandler) checkTOS(ctx context.Context, c *gin.Context, cfg *types.TOSEngineConfig) {
	if cfg == nil {
		writeStorageCheckErr(c, "未提供 TOS 配置")
		return
	}
	if cfg.Endpoint == "" || cfg.Region == "" || cfg.AccessKey == "" || cfg.SecretKey == "" || cfg.BucketName == "" {
		writeStorageCheckErr(c, "Endpoint、Region、Access Key、Secret Key、Bucket 名称不能为空")
		return
	}
	if blocked, reason := isBlockedStorageEndpoint(cfg.Endpoint); blocked {
		logger.Warnf(ctx, "Storage check: TOS endpoint blocked by SSRF protection, endpoint: %s", cfg.Endpoint)
		writeStorageCheckErr(c, reason)
		return
	}

	err := file.CheckTosConnectivity(ctx, cfg.Endpoint, cfg.Region, cfg.AccessKey, cfg.SecretKey, cfg.BucketName)
	if err != nil {
		logger.Errorf(ctx, "Storage check: TOS connectivity failed, bucket: %s, error: %v", cfg.BucketName, err)
		errMsg := err.Error()
		if strings.Contains(errMsg, "403") {
			writeStorageCheckErr(c, "认证失败，请检查 Access Key / Secret Key 是否正确")
			return
		}
		if strings.Contains(errMsg, "404") {
			writeStorageCheckErr(c, fmt.Sprintf("Bucket「%s」不存在，请检查名称和 Region", cfg.BucketName))
			return
		}
		writeStorageCheckErr(c, sanitizeStorageCheckError(err))
		return
	}
	writeStorageCheckOK(c, cfg.BucketName)
}

func (h *SystemHandler) checkS3(ctx context.Context, c *gin.Context, cfg *types.S3EngineConfig) {
	if cfg == nil {
		writeStorageCheckErr(c, "未提供 S3 配置")
		return
	}
	if cfg.Region == "" || cfg.BucketName == "" {
		writeStorageCheckErr(c, "Region、Bucket 名称不能为空")
		return
	}
	if (cfg.AccessKey == "") != (cfg.SecretKey == "") {
		writeStorageCheckErr(c, "Access Key 与 Secret Key 必须同时填写或同时留空（使用 AWS 默认凭证链）")
		return
	}
	if cfg.Endpoint != "" {
		if blocked, reason := isBlockedStorageEndpoint(cfg.Endpoint); blocked {
			logger.Warnf(ctx, "Storage check: S3 endpoint blocked by SSRF protection, endpoint: %s", cfg.Endpoint)
			writeStorageCheckErr(c, reason)
			return
		}
	}

	err := file.CheckS3Connectivity(ctx, cfg.Endpoint, cfg.AccessKey, cfg.SecretKey, cfg.BucketName, cfg.Region)
	if err != nil {
		logger.Errorf(ctx, "Storage check: S3 connectivity failed, bucket: %s, error: %v", cfg.BucketName, err)
		errMsg := err.Error()
		if strings.Contains(errMsg, "403") {
			writeStorageCheckErr(c, "认证失败，请检查静态密钥或 AWS IAM Role / 默认凭证链权限")
			return
		}
		if strings.Contains(errMsg, "404") || strings.Contains(errMsg, "NotFound") {
			writeStorageCheckErr(c, fmt.Sprintf("Bucket「%s」不存在，请检查名称和 Region", cfg.BucketName))
			return
		}
		writeStorageCheckErr(c, sanitizeStorageCheckError(err))
		return
	}
	writeStorageCheckOK(c, cfg.BucketName)
}

func (h *SystemHandler) checkOSS(ctx context.Context, c *gin.Context, cfg *types.OSSEngineConfig) {
	if cfg == nil {
		writeStorageCheckErr(c, "未提供 OSS 配置")
		return
	}
	endpoint, accessKey, secretKey := cfg.Endpoint, cfg.AccessKey, cfg.SecretKey
	if endpoint == "" || accessKey == "" || secretKey == "" || cfg.BucketName == "" {
		writeStorageCheckErr(c, "Endpoint、Access Key、Secret Key、Bucket Name 不能为空")
		return
	}
	ssrfEndpoint := strings.TrimPrefix(strings.TrimPrefix(endpoint, "https://"), "http://")
	if blocked, reason := isBlockedStorageEndpoint(ssrfEndpoint); blocked {
		logger.Warnf(ctx, "Storage check: OSS endpoint blocked by SSRF protection, endpoint: %s", endpoint)
		writeStorageCheckErr(c, reason)
		return
	}
	if !ossFieldPattern.MatchString(cfg.Region) {
		writeStorageCheckErr(c, "Region 格式不正确，仅允许字母、数字、点、连字符")
		return
	}
	if !ossFieldPattern.MatchString(cfg.BucketName) {
		writeStorageCheckErr(c, "Bucket 名称格式不正确，仅允许字母、数字、点、连字符")
		return
	}

	err := file.CheckOssConnectivity(ctx, endpoint, cfg.Region, accessKey, secretKey, cfg.BucketName)
	if err != nil {
		errMsg := err.Error()
		if strings.Contains(errMsg, "403") || strings.Contains(errMsg, "AccessDenied") {
			logger.Errorf(ctx, "Storage check: OSS auth failed, endpoint: %s, bucket: %s", endpoint, cfg.BucketName)
			writeStorageCheckErr(c, "认证失败，请检查 Access Key / Secret Key 是否正确")
			return
		}
		if strings.Contains(errMsg, "404") || strings.Contains(errMsg, "NoSuchBucket") {
			logger.Errorf(ctx, "Storage check: OSS bucket not found, bucket: %s", cfg.BucketName)
			writeStorageCheckErr(c, fmt.Sprintf("Bucket「%s」不存在", cfg.BucketName))
			return
		}
		logger.Errorf(
			ctx, "Storage check: OSS failed, endpoint: %s, bucket: %s, err: %v", endpoint, cfg.BucketName, err,
		)
		writeStorageCheckErr(c, fmt.Sprintf("OSS 连通性检测失败: %s", sanitizeStorageCheckError(err)))
		return
	}
	writeStorageCheckOK(c, cfg.BucketName)
}

func (h *SystemHandler) checkKS3(ctx context.Context, c *gin.Context, cfg *types.KS3EngineConfig) {
	if cfg == nil {
		writeStorageCheckErr(c, "未提供 KS3 配置")
		return
	}
	endpoint, region, accessKey, secretKey := cfg.Endpoint, cfg.Region, cfg.AccessKey, cfg.SecretKey
	if endpoint == "" || region == "" || accessKey == "" || secretKey == "" || cfg.BucketName == "" {
		writeStorageCheckErr(c, "Endpoint、Region、Access Key、Secret Key、Bucket 名称不能为空")
		return
	}
	if blocked, reason := isBlockedStorageEndpoint(endpoint); blocked {
		logger.Warnf(ctx, "Storage check: KS3 endpoint blocked by SSRF protection, endpoint: %s", endpoint)
		writeStorageCheckErr(c, reason)
		return
	}

	err := file.CheckKS3Connectivity(ctx, endpoint, region, accessKey, secretKey, cfg.BucketName)
	if err != nil {
		logger.Errorf(ctx, "Storage check: KS3 connectivity failed, bucket: %s, error: %v", cfg.BucketName, err)
		errMsg := err.Error()
		if strings.Contains(errMsg, "403") || strings.Contains(errMsg, "AccessDenied") {
			writeStorageCheckErr(c, "认证失败，请检查 Access Key / Secret Key 是否正确")
			return
		}
		if strings.Contains(errMsg, "404") || strings.Contains(errMsg, "NoSuchBucket") {
			writeStorageCheckErr(c, fmt.Sprintf("Bucket「%s」不存在，请检查名称和 Region", cfg.BucketName))
			return
		}
		writeStorageCheckErr(c, sanitizeStorageCheckError(err))
		return
	}
	writeStorageCheckOK(c, cfg.BucketName)
}

func (h *SystemHandler) checkOBS(ctx context.Context, c *gin.Context, cfg *types.OBSEngineConfig) {
	if cfg == nil {
		writeStorageCheckErr(c, "未提供 OBS 配置")
		return
	}
	endpoint, region, accessKey, secretKey := cfg.Endpoint, cfg.Region, cfg.AccessKey, cfg.SecretKey
	if endpoint == "" || region == "" || accessKey == "" || secretKey == "" || cfg.BucketName == "" {
		writeStorageCheckErr(c, "Endpoint、Region、Access Key、Secret Key、Bucket 名称不能为空")
		return
	}
	ssrfEndpoint := strings.TrimPrefix(strings.TrimPrefix(endpoint, "https://"), "http://")
	if blocked, reason := isBlockedStorageEndpoint(ssrfEndpoint); blocked {
		logger.Warnf(ctx, "Storage check: OBS endpoint blocked by SSRF protection, endpoint: %s", endpoint)
		writeStorageCheckErr(c, reason)
		return
	}
	if !ossFieldPattern.MatchString(cfg.Region) {
		writeStorageCheckErr(c, "Region 格式不正确，仅允许字母、数字、点、连字符")
		return
	}
	if !ossFieldPattern.MatchString(cfg.BucketName) {
		writeStorageCheckErr(c, "Bucket 名称格式不正确，仅允许字母、数字、点、连字符")
		return
	}

	err := file.CheckObsConnectivity(ctx, endpoint, region, accessKey, secretKey, cfg.BucketName)
	if err != nil {
		logger.Errorf(ctx, "Storage check: OBS connectivity failed, bucket: %s, error: %v", cfg.BucketName, err)
		errMsg := err.Error()
		if strings.Contains(errMsg, "403") || strings.Contains(errMsg, "AccessDenied") {
			writeStorageCheckErr(c, "认证失败，请检查 Access Key / Secret Key 是否正确")
			return
		}
		if strings.Contains(errMsg, "404") || strings.Contains(errMsg, "NoSuchBucket") {
			writeStorageCheckErr(c, fmt.Sprintf("Bucket「%s」不存在，请检查名称和 Region", cfg.BucketName))
			return
		}
		writeStorageCheckErr(c, sanitizeStorageCheckError(err))
		return
	}
	writeStorageCheckOK(c, cfg.BucketName)
}
