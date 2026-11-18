package file

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Usir233/GENYEE/internal/utils"
	"github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss"
	"github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss/credentials"
)

func TestParseOssFilePath(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		wantBucket  string
		wantKey     string
		wantErr     bool
		errContains string
	}{
		{
			name:       "valid path with nested key",
			input:      "oss://my-bucket/123/exports/abc123.csv",
			wantBucket: "my-bucket",
			wantKey:    "123/exports/abc123.csv",
		},
		{
			name:       "valid path with simple key",
			input:      "oss://test-bucket/key",
			wantBucket: "test-bucket",
			wantKey:    "key",
		},
		{
			name:       "valid path with deep nesting",
			input:      "oss://bucket/prefix/tenant/exports/uuid.png",
			wantBucket: "bucket",
			wantKey:    "prefix/tenant/exports/uuid.png",
		},
		{
			name:        "invalid scheme",
			input:       "s3://bucket/key",
			wantErr:     true,
			errContains: "invalid OSS file path",
		},
		{
			name:        "empty path",
			input:       "",
			wantErr:     true,
			errContains: "invalid OSS file path",
		},
		{
			name:        "bucket only no key",
			input:       "oss://bucket/",
			wantErr:     true,
			errContains: "invalid OSS file path",
		},
		{
			name:        "scheme only",
			input:       "oss://",
			wantErr:     true,
			errContains: "invalid OSS file path",
		},
		{
			name:        "no slash after bucket",
			input:       "oss://bucket",
			wantErr:     true,
			errContains: "invalid OSS file path",
		},
		{
			name:        "empty bucket name",
			input:       "oss:///some-key",
			wantErr:     true,
			errContains: "invalid OSS file path",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bucket, key, err := parseOssFilePath(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Errorf("parseOssFilePath(%q) expected error, got bucket=%q key=%q", tt.input, bucket, key)
				}
				if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("parseOssFilePath(%q) error = %v, want containing %q", tt.input, err, tt.errContains)
				}
				return
			}
			if err != nil {
				t.Errorf("parseOssFilePath(%q) unexpected error: %v", tt.input, err)
				return
			}
			if bucket != tt.wantBucket {
				t.Errorf("parseOssFilePath(%q) bucket = %q, want %q", tt.input, bucket, tt.wantBucket)
			}
			if key != tt.wantKey {
				t.Errorf("parseOssFilePath(%q) key = %q, want %q", tt.input, key, tt.wantKey)
			}
		})
	}
}

func TestNewOSSClient(t *testing.T) {
	tests := []struct {
		name      string
		endpoint  string
		region    string
		accessKey string
		secretKey string
		wantErr   bool
	}{
		{
			name:      "valid parameters create client",
			endpoint:  "https://oss-cn-hangzhou.aliyuncs.com",
			region:    "cn-hangzhou",
			accessKey: "test-access-key",
			secretKey: "test-secret-key",
			wantErr:   false,
		},
		{
			name:      "custom endpoint",
			endpoint:  "https://example.com",
			region:    "cn-shanghai",
			accessKey: "ak",
			secretKey: "sk",
			wantErr:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, err := newOSSClient(tt.endpoint, tt.region, tt.accessKey, tt.secretKey)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error but got nil")
				}
				return
			}
			if err != nil {
				t.Errorf("newOSSClient() unexpected error: %v", err)
				return
			}
			if client == nil {
				t.Error("expected non-nil client")
			}
		})
	}
}

func TestNewOSSClientRejectsUnsafeEndpoint(t *testing.T) {
	if _, err := newOSSClient("http://127.0.0.1:9000", "cn-hangzhou", "ak", "sk"); err == nil {
		t.Fatal("expected loopback OSS endpoint to be rejected")
	}
}

func TestCheckOssConnectivity_InvalidEndpoint(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Should fail with an invalid/unreachable endpoint
	err := CheckOssConnectivity(ctx,
		"https://invalid-oss-endpoint-that-does-not-exist.local",
		"cn-hangzhou",
		"invalid-access-key",
		"invalid-secret-key",
		"nonexistent-bucket",
	)

	if err == nil {
		t.Error("CheckOssConnectivity with invalid endpoint should return an error")
	}
}

func TestOssEnsureBucket_AccessDenied(t *testing.T) {
	methods := make(chan string, 4)
	client := ossBucketTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		methods <- r.Method
		w.Header().Set("x-oss-request-id", "fixture-request")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("<Error><Code>AccessDenied</Code><Message>denied</Message></Error>"))
	}))
	err := ossEnsureBucket(client, "private-bucket")
	if err == nil || !strings.Contains(err.Error(), "failed to check OSS bucket") {
		t.Fatalf("expected bucket ACL permission error, got %v", err)
	}
	if len(methods) != 1 || <-methods != http.MethodGet {
		t.Fatal("expected exactly one ACL request, without bucket creation")
	}
}

func TestOssEnsureBucket_CreateFails(t *testing.T) {
	methods := make(chan string, 4)
	client := ossBucketTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		methods <- r.Method
		w.Header().Set("x-oss-request-id", "fixture-request")
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte("<Error><Code>NoSuchBucket</Code><Message>missing</Message></Error>"))
			return
		}
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("<Error><Code>AccessDenied</Code><Message>denied</Message></Error>"))
	}))
	err := ossEnsureBucket(client, "new-bucket")
	if err == nil || !strings.Contains(err.Error(), "failed to create OSS bucket") {
		t.Fatalf("expected bucket creation error, got %v", err)
	}
	if len(methods) != 2 || <-methods != http.MethodGet || <-methods != http.MethodPut {
		t.Fatal("expected missing bucket ACL request followed by creation")
	}
}

func TestOssConnectivityRejectsInvalidCredentials(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("<Error><Code>InvalidAccessKeyId</Code><Message>invalid key</Message></Error>"))
	}))
	defer server.Close()
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	utils.ResetSSRFWhitelistForTest()
	t.Cleanup(utils.ResetSSRFWhitelistForTest)
	if err := CheckOssConnectivity(
		context.Background(), server.URL, "cn-hangzhou", "bad-key", "bad-secret", "bucket",
	); err == nil {
		t.Fatal("invalid credentials must fail the configuration connectivity check")
	}
	client := ossBucketTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("<Error><Code>AccessDenied</Code><Message>denied</Message></Error>"))
	}))
	service := &ossFileService{client: client, bucketName: "bucket"}
	if err := service.CheckConnectivity(context.Background()); err == nil {
		t.Fatal("permission error must fail the existing service connectivity check")
	}
}

func TestOssEnsureBucketConflictRequiresAccess(t *testing.T) {
	for _, accessible := range []bool{false, true} {
		t.Run(map[bool]string{false: "another_owner", true: "concurrent_creation"}[accessible], func(t *testing.T) {
			methods := make(chan string, 4)
			requests := 0
			client := ossBucketTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				methods <- r.Method
				switch requests {
				case 1:
					w.WriteHeader(http.StatusNotFound)
					_, _ = w.Write([]byte("<Error><Code>NoSuchBucket</Code></Error>"))
				case 2:
					w.WriteHeader(http.StatusConflict)
					_, _ = w.Write([]byte("<Error><Code>BucketAlreadyExists</Code></Error>"))
				default:
					if accessible {
						_, _ = w.Write(
							[]byte(
								"<AccessControlPolicy><AccessControlList><Grant>private</Grant></" +
									"AccessControlList></AccessControlPolicy>",
							),
						)
					} else {
						w.WriteHeader(http.StatusForbidden)
						_, _ = w.Write([]byte("<Error><Code>AccessDenied</Code></Error>"))
					}
				}
			}))
			err := ossEnsureBucket(client, "bucket")
			if (err == nil) != accessible {
				t.Fatalf("accessible=%v, got error %v", accessible, err)
			}
			if len(methods) != 3 || <-methods != http.MethodGet || <-methods != http.MethodPut || <-methods !=
				http.MethodGet {
				t.Fatal("expected ACL check, create, then ACL recheck")
			}
		})
	}
}

// Exercise the real SDK protocol without external credentials or network calls.
// Endpoint SSRF validation is covered by TestNewOSSClientRejectsUnsafeEndpoint.
func ossBucketTestClient(t *testing.T, handler http.Handler) *oss.Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return oss.NewClient(oss.LoadDefaultConfig().
		WithEndpoint(server.URL).
		WithRegion("cn-hangzhou").
		WithCredentialsProvider(credentials.NewStaticCredentialsProvider("fixture-key", "fixture-secret", "")).
		WithUsePathStyle(true).
		WithRetryMaxAttempts(1).
		WithHttpClient(server.Client()))
}
