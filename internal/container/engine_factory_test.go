package container

import (
	"testing"

	"github.com/Usir233/GENYEE/internal/types"
)

func TestBuildMilvusClientConfig_UsesDatabaseName(t *testing.T) {
	cfg := buildMilvusClientConfig(types.ConnectionConfig{
		Addr:     "milvus.example.com:19530",
		Username: "tester",
		Password: "secret",
		Database: "regdi_ram_haom1",
	})

	if cfg.Address != "milvus.example.com:19530" {
		t.Fatalf("expected address to be preserved, got %q", cfg.Address)
	}
	if cfg.Username != "tester" {
		t.Fatalf("expected username to be preserved, got %q", cfg.Username)
	}
	if cfg.Password != "secret" {
		t.Fatalf("expected password to be preserved, got %q", cfg.Password)
	}
	if cfg.DBName != "regdi_ram_haom1" {
		t.Fatalf("expected DBName to be propagated, got %q", cfg.DBName)
	}
	if len(cfg.DialOptions) != 2 {
		t.Fatalf("expected timeout and SSRF-safe dial options, got %d", len(cfg.DialOptions))
	}
}

func TestBuildMilvusClientConfig_DefaultsAddressWhenMissing(t *testing.T) {
	cfg := buildMilvusClientConfig(types.ConnectionConfig{})

	if cfg.Address != "localhost:19530" {
		t.Fatalf("expected default address localhost:19530, got %q", cfg.Address)
	}
	if cfg.DBName != "" {
		t.Fatalf("expected empty DBName by default, got %q", cfg.DBName)
	}
	if len(cfg.DialOptions) != 2 {
		t.Fatalf("expected timeout and SSRF-safe dial options, got %d", len(cfg.DialOptions))
	}
}

func TestBuildWeaviateClientConfig_DisablesUnguardedGRPCForExternalStores(t *testing.T) {
	cfg, err := buildWeaviateClientConfig(types.VectorStore{
		ID:         "store-tenant-custom-1",
		EngineType: types.WeaviateRetrieverEngineType,
		ConnectionConfig: types.ConnectionConfig{
			Host:        "weaviate.customer.example.com:8080",
			GrpcAddress: "weaviate.customer.example.com:50051",
			Scheme:      "https",
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.GrpcConfig != nil {
		t.Fatalf("expected GrpcConfig to be nil for non-whitelisted external tenant store, got %+v", cfg.GrpcConfig)
	}
	if cfg.ConnectionClient == nil {
		t.Fatal("expected SSRF-safe HTTP ConnectionClient to be configured")
	}
}

func TestBuildWeaviateClientConfig_AllowsGRPCForEnvStoreAndRejectsEmptyExternalHost(t *testing.T) {
	envCfg, err := buildWeaviateClientConfig(types.VectorStore{
		ID:         types.EnvStoreIDPrefix + "weaviate",
		EngineType: types.WeaviateRetrieverEngineType,
	})
	if err != nil {
		t.Fatalf("unexpected error for env store: %v", err)
	}
	if envCfg.GrpcConfig == nil || envCfg.GrpcConfig.Host != "weaviate:50051" {
		t.Fatalf("expected env store to retain default GrpcConfig weaviate:50051, got %+v", envCfg.GrpcConfig)
	}

	if _, err := buildWeaviateClientConfig(types.VectorStore{
		ID:         "store-tenant-empty-host",
		EngineType: types.WeaviateRetrieverEngineType,
	}); err == nil {
		t.Fatal("expected empty host on external tenant store to be rejected rather than defaulting to internal weaviate:8080")
	}
}

func TestHostFromAddrHandlesIPv4HostnameAndIPv6(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"127.0.0.1:9030", "127.0.0.1"},
		{"doris-fe", "doris-fe"},
		{"doris-fe:9030", "doris-fe"},
		{"[::1]:9030", "[::1]"},
		{"::1", "[::1]"},
		{"2001:db8::1", "[2001:db8::1]"},
		{"[2001:db8::1]", "[2001:db8::1]"},
	}
	for _, tc := range cases {
		if got := hostFromAddr(tc.in); got != tc.want {
			t.Errorf("hostFromAddr(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
