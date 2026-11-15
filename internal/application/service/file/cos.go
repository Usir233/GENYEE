package file

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/Usir233/GENYEE/internal/logger"
	"github.com/Usir233/GENYEE/internal/types/interfaces"
	"github.com/Usir233/GENYEE/internal/utils"
	"github.com/google/uuid"
	"github.com/tencentyun/cos-go-sdk-v5"
)

// cosFileService implements the FileService interface for Tencent Cloud COS
type cosFileService struct {
	client         *cos.Client
	bucketURL      string
	cosPathPrefix  string
	tempClient     *cos.Client
	tempBucketURL  string
	tempBucketName string
	tempRegion     string
	bucketName     string
	region         string
}

const cosScheme = "cos://"

func newCOSHTTPClient(secretID, secretKey string) *http.Client {
	client := objectStorageHTTPClient()
	// COS GetCredential expects AuthorizationTransport to remain the outermost
	// transport. The per-request SSRF guard the client came with, over the
	// bounded transport, goes immediately underneath it; the client keeps its
	// redirect policy.
	client.Transport = &cos.AuthorizationTransport{
		SecretID:  secretID,
		SecretKey: secretKey,
		Transport: client.Transport,
	}
	return client
}

func isValidCOSLabel(s string) bool {
	if len(s) == 0 || len(s) > 63 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		isAlphaNum := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
		if !isAlphaNum && c != '-' {
			return false
		}
		if (i == 0 || i == len(s)-1) && c == '-' {
			return false
		}
	}
	return true
}

// newCosClient creates a bare cosFileService with just the SDK client initialised.
// Shared by NewCosFileService* constructors and CheckCosConnectivity.
func newCosClient(bucketName, region, secretID, secretKey string) (*cosFileService, error) {
	if !isValidCOSLabel(bucketName) || !isValidCOSLabel(region) {
		return nil, fmt.Errorf("invalid COS bucket or region")
	}
	bucketURL := fmt.Sprintf("https://%s.cos.%s.myqcloud.com/", bucketName, region)
	u, err := url.Parse(bucketURL)
	logger.Infof(context.Background(), "newCosClient: bucketURL: %s", bucketURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse bucketURL: %w", err)
	}
	client := cos.NewClient(&cos.BaseURL{BucketURL: u}, newCOSHTTPClient(secretID, secretKey))
	return &cosFileService{client: client, bucketURL: bucketURL, bucketName: bucketName, region: region}, nil
}

// NewCosFileService creates a new COS file service instance
func NewCosFileService(bucketName, region, secretId, secretKey, cosPathPrefix string) (interfaces.FileService, error) {
	return NewCosFileServiceWithTempBucket(bucketName, region, secretId, secretKey, cosPathPrefix, "", "")
}

// NewCosFileServiceWithTempBucket creates a new COS file service instance with optional temp bucket
func NewCosFileServiceWithTempBucket(bucketName, region, secretId, secretKey, cosPathPrefix, tempBucketName, tempRegion string) (interfaces.FileService, error) {
	svc, err := newCosClient(bucketName, region, secretId, secretKey)
	if err != nil {
		return nil, err
	}
	svc.cosPathPrefix = cosPathPrefix

	if tempBucketName != "" {
		if tempRegion == "" {
			tempRegion = region
		}
		if !isValidCOSLabel(tempBucketName) || !isValidCOSLabel(tempRegion) {
			return nil, fmt.Errorf("invalid COS temp bucket or region")
		}
		tempBucketURL := fmt.Sprintf("https://%s.cos.%s.myqcloud.com/", tempBucketName, tempRegion)
		tempU, err := url.Parse(tempBucketURL)
		if err != nil {
			return nil, fmt.Errorf("failed to parse temp bucketURL: %w", err)
		}
		svc.tempClient = cos.NewClient(&cos.BaseURL{BucketURL: tempU}, newCOSHTTPClient(secretId, secretKey))
		svc.tempBucketURL = tempBucketURL
		svc.tempBucketName = tempBucketName
		svc.tempRegion = tempRegion
	}

	return svc, nil
}

// CheckConnectivity verifies COS is reachable by performing a HEAD request on the bucket.
func (s *cosFileService) CheckConnectivity(ctx context.Context) error {
	checkCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	_, err := s.client.Bucket.Head(checkCtx)
	return err
}

// CheckCosConnectivity tests COS connectivity using the provided credentials.
// It creates a temporary service instance internally and delegates to CheckConnectivity.
func CheckCosConnectivity(ctx context.Context, bucketName, region, secretID, secretKey string) error {
	svc, err := newCosClient(bucketName, region, secretID, secretKey)
	if err != nil {
		return err
	}
	return svc.CheckConnectivity(ctx)
}

// SaveFile saves a file to COS storage
// It generates a unique name for the file and organizes it by tenant and knowledge ID
func (s *cosFileService) SaveFile(ctx context.Context,
	file *multipart.FileHeader, tenantID uint64, knowledgeID string,
) (string, error) {
	if err := validateSaveTarget(tenantID, knowledgeID, file); err != nil {
		return "", err
	}
	ext := filepath.Ext(file.Filename)
	objectName := fmt.Sprintf("%s/%d/%s/%s%s", s.cosPathPrefix, tenantID, knowledgeID, uuid.New().String(), ext)
	if err := utils.SafeObjectKey(objectName); err != nil {
		return "", fmt.Errorf("invalid COS object key: %w", err)
	}
	src, err := file.Open()
	if err != nil {
		return "", fmt.Errorf("failed to open file: %w", err)
	}
	defer src.Close()
	ctx, cancel := objectStorageTransferContext(ctx)
	defer cancel()
	_, err = s.client.Object.Put(ctx, objectName, src, nil)
	if err != nil {
		return "", fmt.Errorf("failed to upload file to COS: %w", err)
	}
	return fmt.Sprintf("cos://%s/%s/%s", s.bucketName, s.region, objectName), nil
}

// GetFile retrieves a file from COS storage by its path URL
func (s *cosFileService) GetFile(ctx context.Context, filePathUrl string) (io.ReadCloser, error) {
	client, _, _, objectName, err := s.resolveCosTarget(filePathUrl)
	if err != nil {
		return nil, err
	}
	if err := utils.SafeObjectKey(objectName); err != nil {
		return nil, fmt.Errorf("invalid file path: %w", err)
	}
	ctx, cancel := objectStorageTransferContext(ctx)
	resp, err := client.Object.Get(ctx, objectName, nil)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to get file from COS: %w", err)
	}
	return objectStorageBoundReader(resp.Body, cancel), nil
}

// DeleteFile removes a file from COS storage
func (s *cosFileService) DeleteFile(ctx context.Context, filePath string) error {
	client, _, _, objectName, err := s.resolveCosTarget(filePath)
	if err != nil {
		return err
	}
	if err := utils.SafeObjectKey(objectName); err != nil {
		return fmt.Errorf("invalid file path: %w", err)
	}
	ctx, cancel := objectStorageTransferContext(ctx)
	defer cancel()
	_, err = client.Object.Delete(ctx, objectName)
	if err != nil {
		return fmt.Errorf("failed to delete file: %w", err)
	}
	return nil
}

// parseCosObjectName extracts the object name from:
// - provider scheme: cos://{bucket}/{region}/{objectKey}
// - legacy URL: https://bucket.cos.region.myqcloud.com/{objectKey}
// Canonical storage://<backend-id>/cos://... paths are accepted too: the
// wrapper is stripped first, otherwise the whole canonical form silently falls
// through to the legacy-URL branch and is returned unchanged (#3151).
func (s *cosFileService) parseCosObjectName(filePath string) (string, error) {
	filePath = storageBackendInnerPath(filePath)
	for _, other := range []string{"local://", "minio://", "s3://", "tos://", "oss://", "ks3://", "obs://", "resource://", "storage://"} {
		if strings.HasPrefix(filePath, other) {
			return "", fmt.Errorf("cos file service cannot resolve %s path", strings.Split(other, "://")[0])
		}
	}
	// Provider scheme format: cos://{bucket}/{region}/{objectKey}
	if strings.HasPrefix(filePath, cosScheme) {
		rest := strings.TrimPrefix(filePath, cosScheme)
		parts := strings.SplitN(rest, "/", 3)
		if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
			return "", fmt.Errorf("invalid COS file path: %s", filePath)
		}
		if s.bucketName != "" && parts[0] != s.bucketName {
			return "", fmt.Errorf("bucket mismatch in path: got %s, want %s", parts[0], s.bucketName)
		}
		if s.region != "" && parts[1] != s.region {
			return "", fmt.Errorf("region mismatch in path: got %s, want %s", parts[1], s.region)
		}
		if err := utils.SafeObjectKey(parts[2]); err != nil {
			return "", fmt.Errorf("invalid COS object key: %w", err)
		}
		return parts[2], nil
	}
	// Legacy format: https://bucket.cos.region.myqcloud.com/{objectKey}
	if s.bucketURL != "" && strings.HasPrefix(filePath, s.bucketURL) {
		key := strings.TrimPrefix(filePath, s.bucketURL)
		if err := utils.SafeObjectKey(key); err != nil {
			return "", fmt.Errorf("invalid COS object key: %w", err)
		}
		return key, nil
	}
	return "", fmt.Errorf("invalid COS file path: %s", filePath)
}

// CopyFile copies an existing COS object to a new knowledge-owned object using a
// server-side Object.Copy (no data leaves COS). The destination uses the same
// layout as SaveFile. Returns ErrCrossBackendCopy when srcPath is not a cos:// path.
func (s *cosFileService) CopyFile(ctx context.Context,
	srcPath string, tenantID uint64, knowledgeID string,
) (string, error) {
	_, sourceBucket, sourceRegion, srcObjectKey, err := s.resolveCosTarget(srcPath)
	if err != nil {
		return "", fmt.Errorf("cos copy rejected source %q: %w", srcPath, ErrCrossBackendCopy)
	}
	if err := utils.SafeObjectKey(srcObjectKey); err != nil {
		return "", fmt.Errorf("invalid source path: %w", err)
	}
	if err := validateCopyTarget(tenantID, knowledgeID); err != nil {
		return "", err
	}

	ext := filepath.Ext(srcPath)
	destKey := fmt.Sprintf("%s/%d/%s/%s%s", s.cosPathPrefix, tenantID, knowledgeID, uuid.New().String(), ext)
	if err := utils.SafeObjectKey(destKey); err != nil {
		return "", fmt.Errorf("invalid destination path: %w", err)
	}

	// sourceURL is the host + object key WITHOUT a scheme, per the COS SDK contract.
	ctx, cancel := objectStorageTransferContext(ctx)
	defer cancel()

	sourceURL := fmt.Sprintf("%s.cos.%s.myqcloud.com/%s", sourceBucket, sourceRegion, srcObjectKey)
	_, _, err = s.client.Object.Copy(ctx, destKey, sourceURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to copy file in COS: %w", err)
	}

	newPath := fmt.Sprintf("cos://%s/%s/%s", s.bucketName, s.region, destKey)
	logger.Infof(ctx, "Copied COS object %s to %s", srcPath, newPath)
	return newPath, nil
}

// SaveBytes saves bytes data to COS
// If temp is true and temp bucket is configured, saves to temp bucket (with lifecycle auto-expiration)
// Otherwise saves to main bucket
func (s *cosFileService) SaveBytes(ctx context.Context, data []byte, tenantID uint64, fileName string, temp bool) (string, error) {
	if err := validateTenantID(tenantID); err != nil {
		return "", err
	}
	safeName, err := utils.SafeFileName(fileName)
	if err != nil {
		return "", fmt.Errorf("invalid file name: %w", err)
	}
	ext := filepath.Ext(safeName)
	ctx, cancel := objectStorageTransferContext(ctx)
	defer cancel()
	reader := bytes.NewReader(data)

	// ????????????????
	if temp && s.tempClient != nil {
		objectName := fmt.Sprintf("exports/%d/%s%s", tenantID, uuid.New().String(), ext)
		_, err := s.tempClient.Object.Put(ctx, objectName, reader, nil)
		if err != nil {
			return "", fmt.Errorf("failed to upload bytes to COS temp bucket: %w", err)
		}
		return fmt.Sprintf("cos://%s/%s/%s", s.tempBucketName, s.tempRegion, objectName), nil
	}

	// ????
	objectName := fmt.Sprintf("%s/%d/exports/%s%s", s.cosPathPrefix, tenantID, uuid.New().String(), ext)
	_, err = s.client.Object.Put(ctx, objectName, reader, nil)
	if err != nil {
		return "", fmt.Errorf("failed to upload bytes to COS: %w", err)
	}

	return fmt.Sprintf("cos://%s/%s/%s", s.bucketName, s.region, objectName), nil
}

// GetFileURL returns a presigned download URL for the file
func (s *cosFileService) GetFileURL(ctx context.Context, filePath string) (string, error) {
	client, _, _, objectName, err := s.resolveCosTarget(filePath)
	if err != nil {
		return "", err
	}
	presignedURL, err := client.Object.GetPresignedURL(ctx, http.MethodGet, objectName, client.GetCredential().SecretID, client.GetCredential().SecretKey, 24*time.Hour, nil)
	if err != nil {
		return "", fmt.Errorf("failed to generate presigned URL: %w", err)
	}
	return presignedURL.String(), nil
}

// resolveCosTarget keeps temporary objects on their actual bucket for reads,
// signing, copies and cleanup. Persisted writes always use canonical COS paths.
func (s *cosFileService) resolveCosTarget(filePath string) (*cos.Client, string, string, string, error) {
	filePath = storageBackendInnerPath(filePath)
	if s.tempClient != nil {
		canonical := fmt.Sprintf("cos://%s/%s/", s.tempBucketName, s.tempRegion)
		for _, prefix := range []string{canonical, s.tempBucketURL} {
			if prefix != "" && strings.HasPrefix(filePath, prefix) {
				key := strings.TrimPrefix(filePath, prefix)
				if err := utils.SafeObjectKey(key); err != nil {
					return nil, "", "", "", fmt.Errorf("invalid COS object key: %w", err)
				}
				return s.tempClient, s.tempBucketName, s.tempRegion, key, nil
			}
		}
	}
	key, err := s.parseCosObjectName(filePath)
	return s.client, s.bucketName, s.region, key, err
}
