package file

import (
	"context"
	"net/http"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/stretchr/testify/require"
)

// TestUpSvcRepoAudit_OBSRejectsBucketAuthorityInjectionAndTraversalObjectKey
// verifies UP-SVC-OBS-001:
// 1. OBS virtual-hosted addressing must reject unsafe bucket names containing
//    userinfo/authority/path/fragment delimiters or IP literals in NewObsFileService,
//    CheckObsConnectivity, and GetFileURL.
// 2. obsFileService.SaveBytes must validate the constructed objectKey with
//    utils.SafeObjectKey just like SaveFile and CopyFile so a traversal pathPrefix
//    cannot upload unvalidated keys.
func TestUpSvcRepoAudit_OBSRejectsBucketAuthorityInjectionAndTraversalObjectKey(t *testing.T) {
	ctx := context.Background()
	const endpoint = "https://obs.cn-south-1.myhuaweicloud.com"

	for _, badBucket := range []string{
		"",
		"169.254.169.254",
		"169.254.169.254/#",
		"evil.com@x",
		"bucket:8080",
		"bucket/../../secret",
		"bucket..double",
		"-leading-hyphen",
	} {
		_, err := NewObsFileService(endpoint, "cn-south-1", "ak", "sk", badBucket, "")
		require.Error(t, err, "NewObsFileService must reject unsafe bucketName %q", badBucket)

		err = CheckObsConnectivity(ctx, endpoint, "cn-south-1", "ak", "sk", badBucket)
		require.Error(t, err, "CheckObsConnectivity must reject unsafe bucketName %q", badBucket)

		if badBucket != "" && badBucket != "bucket/../../secret" {
			svc := &obsFileService{
				bucketName: badBucket,
				endpoint:   endpoint,
			}
			_, err = svc.GetFileURL(ctx, "obs://"+badBucket+"/7/doc.pdf")
			require.Error(t, err, "GetFileURL must reject unsafe virtual-hosted bucketName %q", badBucket)
		}
	}

	transport := &lifecycleTransport{objects: map[string][]byte{}}
	options := obsS3Options("https://obs.example.com", "region", "ak", "sk")
	options.HTTPClient = &http.Client{Transport: transport}
	options.Retryer = aws.NopRetryer{}
	svc := &obsFileService{
		client:     s3.New(options),
		bucketName: "valid-bucket",
		pathPrefix: "tenant-a/../../tenant-b",
	}
	_, err := svc.SaveBytes(ctx, []byte("secret"), 7, "report.txt", false)
	require.Error(t, err, "SaveBytes must reject traversal pathPrefix via SafeObjectKey")
	require.Empty(t, transport.requests, "PutObject must not execute when objectKey fails SafeObjectKey")
}
