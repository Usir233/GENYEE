package utils

import (
	"strings"
	"testing"
)

func TestValidateAndSecureSQLColumnAllowlist(t *testing.T) {
	scopes := []SearchScope{
		{TenantID: 7, KnowledgeBaseID: "kb-safe-1"},
	}
	opts := []SQLValidationOption{
		WithSecurityDefaults(7),
		WithSearchScopes(scopes),
	}

	safeQueries := []string{
		"SELECT id, name, description, type FROM knowledge_bases",
		"SELECT COUNT(*) FROM knowledges",
		"SELECT id, title, parse_status, file_type FROM knowledges ORDER BY created_at DESC LIMIT 10",
		"SELECT id, content, chunk_index FROM chunks WHERE chunk_type = 'text' LIMIT 5",
	}
	for _, q := range safeQueries {
		secured, validation, err := ValidateAndSecureSQL(q, opts...)
		if err != nil || validation == nil || !validation.Valid {
			t.Fatalf("expected safe query %q to pass, got validation=%+v err=%v", q, validation, err)
		}
		if secured == "" {
			t.Fatalf("expected non-empty secured SQL for %q", q)
		}
	}

	blockedQueries := []string{
		"SELECT * FROM knowledge_bases",
		"SELECT kb.* FROM knowledge_bases kb",
		"SELECT id, cos_config FROM knowledge_bases",
		"SELECT id, vlm_config FROM knowledge_bases",
		"SELECT id, extract_config FROM knowledge_bases",
		"SELECT id, storage_provider_config FROM knowledge_bases",
		"SELECT id, file_path FROM knowledges",
		"SELECT id, meta_data FROM knowledges",
		"SELECT id FROM knowledge_bases WHERE cos_config LIKE '%minio%'",
		"SELECT id FROM knowledge_bases ORDER BY vlm_config",
		"SELECT id FROM knowledges GROUP BY id, file_path",
	}
	for _, q := range blockedQueries {
		secured, validation, err := ValidateAndSecureSQL(q, opts...)
		if err == nil && validation != nil && validation.Valid {
			t.Fatalf("expected sensitive query %q to be blocked, but got valid secured=%q", q, secured)
		}
	}
}

func TestParseTenantIDFromStoragePathAnchoredPrefix(t *testing.T) {
	validCases := []struct {
		path string
		want uint64
	}{
		{"minio://genyee/42/kb-1/doc.pdf", 42},
		{"cos://my-bucket-1250000000/ap-shanghai/tenants/99/images/pic.png", 99},
		{"local://7/exports/out.md", 7},
		{"s3://bucket/exports/15/report.csv", 15},
		{"oss://bucket/tenants/88/doc.txt", 88},
	}
	for _, tc := range validCases {
		got := ParseTenantIDFromStoragePath(tc.path)
		if got != tc.want {
			t.Fatalf("ParseTenantIDFromStoragePath(%q) = %d, want %d", tc.path, got, tc.want)
		}
	}

	bypassCases := []string{
		"minio://12345/unmanaged/file.pdf",
		"local://tenants/42/doc.pdf",
		"s3://bucket/unmanaged/sub/42/doc.pdf",
		"cos://bucket/ap-shanghai/tenants/not-a-number/42/doc.pdf",
		"local://0/doc.pdf",
		"local://42/../../99/doc.pdf",
	}
	for _, raw := range bypassCases {
		if got := ParseTenantIDFromStoragePath(raw); got != 0 {
			t.Fatalf("ParseTenantIDFromStoragePath(%q) = %d, expected 0", raw, got)
		}
	}
}

func TestCryptoRejectsPlaceholderKeysAndValidatesProductionSecrets(t *testing.T) {
	t.Setenv("SYSTEM_AES_KEY", "your-32-byte-long-encryption-key!")
	if key := GetAESKey(); key != nil {
		t.Fatalf("GetAESKey() accepted placeholder key: %q", string(key))
	}
	if err := ValidateProductionAESKey(); err == nil {
		t.Fatal("ValidateProductionAESKey() should reject placeholder key")
	}

	t.Setenv("SYSTEM_AES_KEY", "0123456789abcdef0123456789abcdef")
	if err := ValidateProductionAESKey(); err != nil {
		t.Fatalf("ValidateProductionAESKey() rejected valid 32-byte key: %v", err)
	}

	if err := ValidateProductionJWTSecret("short-secret"); err == nil {
		t.Fatal("ValidateProductionJWTSecret() should reject <32 byte secret")
	}
	if err := ValidateProductionJWTSecret(strings.Repeat("k", 32)); err != nil {
		t.Fatalf("ValidateProductionJWTSecret() rejected valid 32-byte secret: %v", err)
	}
}
