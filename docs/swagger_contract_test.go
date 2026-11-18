package docs_test

import (
	"encoding/json"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"gopkg.in/yaml.v3"

	docs "github.com/Usir233/GENYEE/docs"
	"github.com/Usir233/GENYEE/internal/agent/operations"
	"github.com/Usir233/GENYEE/internal/config"
	"github.com/Usir233/GENYEE/internal/handler"
	"github.com/Usir233/GENYEE/internal/handler/session"
	"github.com/Usir233/GENYEE/internal/router"
	"github.com/Usir233/GENYEE/internal/types"
	"github.com/Usir233/GENYEE/internal/types/interfaces"
)

type swaggerDocumentCase struct {
	name     string
	loadSpec func(t *testing.T) []byte
	parse    func([]byte, any) error
}

type swaggerParameter struct {
	Name     string          `json:"name" yaml:"name"`
	In       string          `json:"in" yaml:"in"`
	Type     string          `json:"type" yaml:"type"`
	Required bool            `json:"required" yaml:"required"`
	Schema   swaggerParamRef `json:"schema" yaml:"schema"`
}

type swaggerParamRef struct {
	Ref        string                     `json:"$ref" yaml:"$ref"`
	Properties map[string]swaggerPropSpec `json:"properties" yaml:"properties"`
}

type swaggerPropSpec struct {
	Type string `json:"type" yaml:"type"`
}

type swaggerOperation struct {
	Parameters []swaggerParameter `json:"parameters" yaml:"parameters"`
}

type stubEmbedChannelService struct {
	interfaces.EmbedChannelService
}

// ginNonSwaggerAllowlist enumerates the exact /api/v1 transport, WebSocket,
// public embed-widget, BrowserSkill extension, and signed/scoped binary
// file-proxy routes intentionally omitted from the REST OpenAPI spec.
// Every other Gin /api/v1 route MUST be documented in docs/swagger.json,
// and every Swagger operation MUST exist in Gin (0 phantom routes).
var ginNonSwaggerAllowlist = map[string]bool{
	// Public embed widget & framing policy routes (publish-token / anonymous transport)
	"GET /embed-frame-policy":                                                                  true,
	"POST /embed/{channel_id}/exchange":                                                        true,
	"GET /embed/{channel_id}/config":                                                           true,
	"GET /embed/{channel_id}/suggested-questions":                                              true,
	"GET /embed/{channel_id}/chunks/{chunk_id}":                                                true,
	"POST /embed/{channel_id}/sessions":                                                        true,
	"POST /embed/{channel_id}/knowledge-chat/{session_id}":                                     true,
	"POST /embed/{channel_id}/agent-chat/{session_id}":                                         true,
	"GET /embed/{channel_id}/messages/{session_id}/load":                                       true,
	"POST /embed/{channel_id}/sessions/{session_id}/stop":                                      true,
	"GET /embed/{channel_id}/sessions/{session_id}/messages/{message_id}/suggestions":          true,
	"POST /embed/{channel_id}/sessions/{session_id}/messages/{message_id}/suggestions":         true,
	"POST /embed/{channel_id}/sessions/{session_id}/suggestion-events":                         true,
	"POST /embed/{channel_id}/sessions/{session_id}/events":                                    true,
	"POST /embed/{channel_id}/sessions/{session_id}/mcp-oauth-resolutions/{pending_id}":        true,
	"POST /embed/{channel_id}/sessions/{session_id}/mcp-oauth-resolutions/{pending_id}/cancel": true,
	"POST /embed/{channel_id}/sessions/{session_id}/mcp-services/{id}/oauth/authorize-url":     true,
	"GET /embed/{channel_id}/sessions/{session_id}/mcp-services/{id}/oauth/status":             true,
	"POST /embed/{channel_id}/sessions/{session_id}/tool-approvals/{pending_id}":               true,
	"POST /embed/{channel_id}/sessions/{session_id}/prompts/{prompt_id}":                       true,
	"GET /embed/{channel_id}/files":                                                            true,

	// WebSocket upgrade endpoints (ticket-authenticated binary/WS upgrades)
	"GET /sessions/{id}/sandbox/desktop":  true,
	"GET /sessions/{id}/sandbox/terminal": true,

	// BrowserSkill extension bridge & internal transport endpoints
	"GET /me/browser":                         true,
	"POST /me/browser":                        true,
	"GET /me/browser/extension":               true,
	"GET /local-browser/extension":            true,
	"POST /local-browser/extension/authorize": true,
	"POST /local-browser/internal":            true,

	// Binary file proxies & presigned URL transport endpoints
	"GET /files/presigned":                           true,
	"HEAD /files/presigned":                          true,
	"GET /files/presigned-preview":                   true,
	"GET /knowledge-bases/{id}/files":                true,
	"GET /sessions/{id}/messages/{message_id}/files": true,
}

func swaggerDocuments() []swaggerDocumentCase {
	return []swaggerDocumentCase{
		{
			name: "registered document",
			loadSpec: func(t *testing.T) []byte {
				t.Helper()
				return []byte(docs.SwaggerInfo.ReadDoc())
			},
			parse: json.Unmarshal,
		},
		{
			name: "swagger.json",
			loadSpec: func(t *testing.T) []byte {
				t.Helper()
				return readSwaggerFile(t, "swagger.json")
			},
			parse: json.Unmarshal,
		},
		{
			name: "swagger.yaml",
			loadSpec: func(t *testing.T) []byte {
				t.Helper()
				return readSwaggerFile(t, "swagger.yaml")
			},
			parse: yaml.Unmarshal,
		},
	}
}

func newContractTestRouter(t *testing.T) *gin.Engine {
	t.Helper()
	t.Setenv("LOCAL_STORAGE_BASE_DIR", t.TempDir())
	gin.SetMode(gin.TestMode)
	return router.NewRouter(router.Params{
		Config:                       &config.Config{},
		KBHandler:                    &handler.KnowledgeBaseHandler{},
		GraphSearchHandler:           &handler.GraphSearchHandler{},
		KnowledgeHandler:             &handler.KnowledgeHandler{},
		TenantHandler:                &handler.TenantHandler{},
		TenantMemberHandler:          &handler.TenantMemberHandler{},
		TenantInvitationHandler:      &handler.TenantInvitationHandler{},
		AuditLogHandler:              &handler.AuditLogHandler{},
		ChunkHandler:                 &handler.ChunkHandler{},
		SessionHandler:               &session.Handler{},
		MessageHandler:               &handler.MessageHandler{},
		MessageSuggestionHandler:     &handler.MessageSuggestionHandler{},
		ModelHandler:                 &handler.ModelHandler{},
		ModelCredentialsHandler:      &handler.ModelCredentialsHandler{},
		SandboxConfigHandler:         &handler.SandboxConfigHandler{},
		SandboxSkillHandler:          &handler.SandboxSkillHandler{},
		MeEnvVarHandler:              &handler.MeEnvVarHandler{},
		CredentialVaultHandler:       &handler.CredentialVaultHandler{},
		EvaluationHandler:            &handler.EvaluationHandler{},
		AuthHandler:                  &handler.AuthHandler{},
		InitializationHandler:        &handler.InitializationHandler{},
		SystemHandler:                &handler.SystemHandler{},
		MCPServiceHandler:            &handler.MCPServiceHandler{},
		MCPCredentialsHandler:        &handler.MCPCredentialsHandler{},
		MCPOAuthHandler:              &handler.MCPOAuthHandler{},
		WebSearchProviderHandler:     &handler.WebSearchProviderHandler{},
		WebSearchCredentialsHandler:  &handler.WebSearchProviderCredentialsHandler{},
		VectorStoreHandler:           &handler.VectorStoreHandler{},
		StorageBackendHandler:        &handler.StorageBackendHandler{},
		FAQHandler:                   &handler.FAQHandler{},
		TagHandler:                   &handler.TagHandler{},
		CustomAgentHandler:           &handler.CustomAgentHandler{},
		UserFavoriteHandler:          &handler.UserResourceFavoriteHandler{},
		SkillHandler:                 &handler.SkillHandler{},
		OrganizationHandler:          &handler.OrganizationHandler{},
		IMHandler:                    &handler.IMHandler{},
		EmbedChannelHandler:          &handler.EmbedChannelHandler{},
		EmbedChannelService:          stubEmbedChannelService{},
		MCPEndpointHandler:           &handler.MCPEndpointHandler{},
		DataSourceHandler:            &handler.DataSourceHandler{},
		DataSourceCredentialsHandler: &handler.DataSourceCredentialsHandler{},
		WikiPageHandler:              &handler.WikiPageHandler{},
		MemoryHandler:                &handler.MemoryHandler{},
		AutomationHandler:            &handler.AutomationHandler{},
		OperationDispatcher:          operations.NewHandlerDispatcher(),
	})
}

func normalizeGinRouteToOpenAPI(ginPath string) string {
	const prefix = "/api/v1"
	trimmed := strings.TrimPrefix(ginPath, prefix)
	if trimmed == "" {
		return "/"
	}
	segments := strings.Split(trimmed, "/")
	for i, seg := range segments {
		if len(seg) > 1 && (seg[0] == ':' || seg[0] == '*') {
			segments[i] = "{" + seg[1:] + "}"
		}
	}
	return strings.Join(segments, "/")
}

func TestGinToSwaggerRouteParity(t *testing.T) {
	engine := newContractTestRouter(t)

	ginOps := make(map[string]bool)
	for _, rt := range engine.Routes() {
		if rt.Path != "/api/v1" && !strings.HasPrefix(rt.Path, "/api/v1/") {
			continue
		}
		key := strings.ToUpper(rt.Method) + " " + normalizeGinRouteToOpenAPI(rt.Path)
		ginOps[key] = true
	}
	if len(ginOps) == 0 {
		t.Fatal("router registered 0 /api/v1 routes")
	}

	// Ensure allowlist contains zero stale entries not actually mounted in Gin.
	for allowKey := range ginNonSwaggerAllowlist {
		if !ginOps[allowKey] {
			t.Errorf("ginNonSwaggerAllowlist contains stale route not mounted in Gin: %s", allowKey)
		}
	}

	httpVerbs := map[string]bool{
		"get": true, "post": true, "put": true, "patch": true,
		"delete": true, "head": true, "options": true,
	}

	for _, tt := range swaggerDocuments() {
		t.Run(tt.name, func(t *testing.T) {
			var spec struct {
				BasePath string                    `json:"basePath" yaml:"basePath"`
				Paths    map[string]map[string]any `json:"paths" yaml:"paths"`
			}
			if err := tt.parse(tt.loadSpec(t), &spec); err != nil {
				t.Fatalf("parse %s: %v", tt.name, err)
			}
			if spec.BasePath != "/api/v1" {
				t.Fatalf("unexpected basePath %q, want /api/v1", spec.BasePath)
			}

			swaggerOps := make(map[string]bool)
			for path, methods := range spec.Paths {
				for method := range methods {
					lower := strings.ToLower(method)
					if !httpVerbs[lower] {
						continue
					}
					swaggerOps[strings.ToUpper(lower)+" "+path] = true
				}
			}

			var phantomInSwagger []string
			for op := range swaggerOps {
				if !ginOps[op] {
					phantomInSwagger = append(phantomInSwagger, op)
				}
			}
			sort.Strings(phantomInSwagger)

			var undocumentedInGin []string
			var allowlistedButDocumented []string
			for op := range ginOps {
				if swaggerOps[op] {
					if ginNonSwaggerAllowlist[op] {
						allowlistedButDocumented = append(allowlistedButDocumented, op)
					}
					continue
				}
				if !ginNonSwaggerAllowlist[op] {
					undocumentedInGin = append(undocumentedInGin, op)
				}
			}
			sort.Strings(undocumentedInGin)
			sort.Strings(allowlistedButDocumented)

			if len(phantomInSwagger) > 0 {
				t.Errorf(
					"Swagger exposes %d phantom route(s) not mounted in Gin:\n  - %s",
					len(phantomInSwagger),
					strings.Join(phantomInSwagger, "\n  - "),
				)
			}
			if len(undocumentedInGin) > 0 {
				t.Errorf(
					"Gin mounts %d undocumented REST route(s) missing from Swagger:\n  - %s",
					len(undocumentedInGin),
					strings.Join(undocumentedInGin, "\n  - "),
				)
			}
			if len(allowlistedButDocumented) > 0 {
				t.Errorf(
					"ginNonSwaggerAllowlist lists %d route(s) that are already documented in Swagger:\n  - %s",
					len(allowlistedButDocumented),
					strings.Join(allowlistedButDocumented, "\n  - "),
				)
			}
		})
	}
}

func TestCanonicalContractRenamesAndSnakeCaseDTOs(t *testing.T) {
	for _, tt := range swaggerDocuments() {
		t.Run(tt.name, func(t *testing.T) {
			raw := tt.loadSpec(t)
			rawText := string(raw)

			if strings.Contains(rawText, "github_com_Tencent_") {
				t.Fatal("generated OpenAPI document contains legacy github_com_Tencent_ definition names")
			}
			if strings.Contains(rawText, "enable_multimodel") {
				t.Fatal("generated OpenAPI document still contains typo field enable_multimodel")
			}

			var spec struct {
				Paths       map[string]map[string]swaggerOperation `json:"paths" yaml:"paths"`
				Definitions map[string]struct {
					Properties map[string]any `json:"properties" yaml:"properties"`
				} `json:"definitions" yaml:"definitions"`
			}
			if err := tt.parse(raw, &spec); err != nil {
				t.Fatalf("parse %s: %v", tt.name, err)
			}

			// Canonical routes MUST exist
			requiredRoutes := []struct {
				path    string
				methods []string
			}{
				{path: "/knowledge-bases/{id}/wiki/pages", methods: []string{"get"}},
				{path: "/knowledge-bases/{id}/wiki/pages/{slug}", methods: []string{"get", "put", "delete"}},
				{path: "/knowledge-bases/{id}/faq/entries", methods: []string{"get", "post"}},
				{path: "/knowledge-bases/{id}/faq/entries/batch", methods: []string{"post"}},
				{path: "/data-sources", methods: []string{"get", "post"}},
				{path: "/data-sources/{id}", methods: []string{"get", "put", "delete"}},
				{path: "/sessions/{id}/generate-title", methods: []string{"post"}},
				{path: "/me/favorites", methods: []string{"get", "post"}},
				{path: "/web-search-providers/types", methods: []string{"get"}},
				{path: "/evaluation", methods: []string{"get", "post"}},
				{path: "/initialization/config/{kb_id}", methods: []string{"get", "put"}},
				{path: "/initialization/ollama/download/progress/{task_id}", methods: []string{"get"}},
				{path: "/sandbox-configs/{id}/skills/{skill_id}", methods: []string{"delete"}},
				{path: "/im-channels", methods: []string{"get"}},
			}
			for _, rt := range requiredRoutes {
				for _, m := range rt.methods {
					if _, ok := spec.Paths[rt.path][m]; !ok {
						t.Errorf("missing canonical route %s %s", strings.ToUpper(m), rt.path)
					}
				}
			}

			// Retired / phantom routes MUST NOT exist
			retiredPaths := []string{
				"/evaluation/",
				"/tenants/kv/web-search-config",
				"/tenants/kv/prompt-templates",
				"/web-search/providers",
				"/user/favorites",
				"/knowledge-bases/{id}/faq/entry",
				"/sessions/{session_id}/generate_title",
				"/datasource",
				"/datasource/{id}",
			}
			for _, p := range retiredPaths {
				if _, ok := spec.Paths[p]; ok {
					t.Errorf("retired/phantom route %s remains documented", p)
				}
			}
			for p := range spec.Paths {
				if strings.HasPrefix(p, "/knowledgebase/") || strings.HasPrefix(p, "/datasource/") {
					t.Errorf("legacy non-canonical route prefix remains documented: %s", p)
				}
			}

			// POST /auth/refresh must document snake_case refresh_token
			refreshOp, ok := spec.Paths["/auth/refresh"]["post"]
			if !ok {
				t.Fatal("missing POST /auth/refresh")
			}
			foundRefreshToken := false
			for _, param := range refreshOp.Parameters {
				if param.In == "body" {
					if _, bad := param.Schema.Properties["refreshToken"]; bad {
						t.Fatal("POST /auth/refresh still documents camelCase refreshToken")
					}
					if prop, good := param.Schema.Properties["refresh_token"]; good && prop.Type == "string" {
						foundRefreshToken = true
					}
				}
			}
			if !foundRefreshToken {
				t.Fatal("POST /auth/refresh must document snake_case body property refresh_token")
			}

			// DTO snake_case property assertions
			assertDTOProperties(
				t,
				spec.Definitions,
				"github_com_Usir233_GENYEE_internal_types.ParserEngineInfo",
				[]string{"name", "description", "file_types", "available", "unavailable_reason"},
				[]string{"Name", "Description", "FileTypes", "Available", "UnavailableReason"},
			)
			assertDTOProperties(
				t,
				spec.Definitions,
				"internal_handler.ModelProviderDTO",
				[]string{
					"value", "label", "requires_auth", "default_urls",
					"model_types", "extra_fields", "credential_labels",
				},
				[]string{"requiresAuth", "defaultUrls", "modelTypes", "extraFields", "credentialLabels"},
			)
			assertDTOProperties(
				t,
				spec.Definitions,
				"internal_handler.InitializationRequest",
				[]string{
					"llm", "embedding", "rerank", "multimodal",
					"document_splitting", "node_extract", "question_generation",
				},
				[]string{"documentSplitting", "nodeExtract", "questionGeneration"},
			)
			assertDTOProperties(
				t,
				spec.Definitions,
				"internal_handler.KBModelConfigRequest",
				[]string{
					"llm_model_id", "embedding_model_id", "vlm_config", "asr_config",
					"document_splitting", "multimodal", "storage_provider", "storage_backend_id",
					"node_extract", "question_generation",
				},
				[]string{
					"llmModelId", "embeddingModelId", "vlmConfig", "asrConfig",
					"documentSplitting", "storageProvider", "storageBackendId",
					"nodeExtract", "questionGeneration",
				},
			)
			assertDTOProperties(
				t,
				spec.Definitions,
				"internal_handler.ModelTestRequest",
				[]string{
					"model_name", "base_url", "api_key", "provider", "interface_type",
					"supports_dimension_override", "custom_headers", "extra_config", "app_secret", "model_id",
				},
				[]string{
					"modelName", "baseUrl", "apiKey", "interfaceType",
					"supportsDimensionOverride", "customHeaders", "extraConfig", "appSecret", "modelId",
				},
			)
			assertDTOProperties(
				t,
				spec.Definitions,
				"internal_handler.DownloadTask",
				[]string{"id", "model_name", "status", "progress", "message", "start_time", "end_time"},
				[]string{"modelName", "startTime", "endTime"},
			)
		})
	}
}

func assertDTOProperties(
	t *testing.T,
	defs map[string]struct {
		Properties map[string]any `json:"properties" yaml:"properties"`
	},
	defName string,
	wantPresent []string,
	wantAbsent []string,
) {
	t.Helper()
	def, ok := defs[defName]
	if !ok {
		t.Fatalf("missing Swagger definition %s", defName)
	}
	for _, prop := range wantPresent {
		if _, exists := def.Properties[prop]; !exists {
			t.Errorf("%s missing expected snake_case property %q", defName, prop)
		}
	}
	for _, prop := range wantAbsent {
		if _, exists := def.Properties[prop]; exists {
			t.Errorf("%s still exposes non-snake_case property %q", defName, prop)
		}
	}
}

func TestKnowledgeSearchRouteContract(t *testing.T) {
	for _, tt := range swaggerDocuments() {
		t.Run(tt.name, func(t *testing.T) {
			assertKnowledgeSearchRouteContract(t, tt.loadSpec(t), tt.parse)
		})
	}
}

func TestImagePipelineAgentKeysContract(t *testing.T) {
	const want = "default.caption_enabled|default.ocr_enabled"
	field, ok := reflect.TypeOf(types.ImageProcessingConfig{}).FieldByName("ImagePipelineParams")
	if !ok {
		t.Fatal("ImagePipelineParams field is missing")
	}
	if !strings.Contains(
		","+field.Tag.Get("extensions")+",",
		",x-agent-keys="+want+",",
	) {
		t.Fatalf("ImagePipelineParams must expose the active switch slots: %s", field.Tag)
	}
	for _, tt := range swaggerDocuments() {
		t.Run(tt.name, func(t *testing.T) {
			var spec struct {
				Definitions map[string]struct {
					Properties map[string]struct {
						AgentKeys string `json:"x-agent-keys" yaml:"x-agent-keys"`
					} `json:"properties" yaml:"properties"`
				} `json:"definitions" yaml:"definitions"`
			}
			if err := tt.parse(tt.loadSpec(t), &spec); err != nil {
				t.Fatal(err)
			}
			got := spec.Definitions["github_com_Usir233_GENYEE_internal_types.ImageProcessingConfig"].
				Properties["image_pipeline_params"].AgentKeys
			if got != want {
				t.Errorf("image_pipeline_params agent keys = %q, want %q", got, want)
			}
		})
	}
}

func TestAutomationAssistantContract(t *testing.T) {
	for _, tt := range swaggerDocuments() {
		t.Run(tt.name, func(t *testing.T) {
			var spec struct {
				Definitions map[string]struct {
					Properties map[string]any `json:"properties" yaml:"properties"`
				} `json:"definitions" yaml:"definitions"`
			}
			if err := tt.parse(tt.loadSpec(t), &spec); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{
				"internal_handler.CreateAutomationRequest",
				"github_com_Usir233_GENYEE_internal_types.AutomationPatch",
				"github_com_Usir233_GENYEE_internal_types.Automation",
			} {
				properties := spec.Definitions[name].Properties
				if _, ok := properties["agent_id"]; ok {
					t.Errorf("%s must not expose executor selection", name)
				}
				for _, field := range []string{"instruction", "model_id"} {
					if _, ok := properties[field]; !ok {
						t.Errorf("%s must expose %s", name, field)
					}
				}
			}
		})
	}
}

func TestReleaseAPIContracts(t *testing.T) {
	for _, tt := range swaggerDocuments() {
		t.Run(tt.name, func(t *testing.T) {
			var spec struct {
				BasePath    string                    `json:"basePath" yaml:"basePath"`
				Paths       map[string]map[string]any `json:"paths" yaml:"paths"`
				Definitions map[string]struct {
					Properties map[string]struct {
						Type string `json:"type" yaml:"type"`
					} `json:"properties" yaml:"properties"`
				} `json:"definitions" yaml:"definitions"`
			}
			if err := tt.parse(tt.loadSpec(t), &spec); err != nil {
				t.Fatal(err)
			}
			if spec.BasePath != "/api/v1" {
				t.Fatalf("unexpected API base path %q", spec.BasePath)
			}
			for path := range spec.Paths {
				if strings.HasPrefix(path, spec.BasePath+"/") {
					t.Errorf("route %s repeats the API base path", path)
				}
			}
			for path, methods := range map[string][]string{
				"/knowledge/{id}/spans":               {"get"},
				"/knowledge-bases/{id}/images":        {"get", "post"},
				"/wiki-search":                        {"post"},
				"/agents/tools":                       {"get"},
				"/system/admin/users":                 {"get"},
				"/system/admin/users/status":          {"post"},
				"/system/admin/model-catalog":         {"get", "put"},
				"/system/admin/model-catalog/preview": {"post"},
			} {
				for _, method := range methods {
					if _, ok := spec.Paths[path][method]; !ok {
						t.Errorf("missing %s %s", method, path)
					}
				}
			}
			for _, name := range []string{"CatalogUpdate", "CatalogState", "CatalogRevision"} {
				definition := "github_com_Usir233_GENYEE_internal_application_service." + name
				if spec.Definitions[definition].Properties["overlay"].Type != "object" {
					t.Errorf("%s.overlay must be a JSON object", name)
				}
			}
		})
	}
}

func TestGraphSearchContract(t *testing.T) {
	for _, tt := range swaggerDocuments() {
		t.Run(tt.name, func(t *testing.T) {
			var spec struct {
				Paths       map[string]map[string]any `json:"paths" yaml:"paths"`
				Definitions map[string]struct {
					Properties map[string]any `json:"properties" yaml:"properties"`
				} `json:"definitions" yaml:"definitions"`
			}
			if err := tt.parse(tt.loadSpec(t), &spec); err != nil {
				t.Fatal(err)
			}
			methods := spec.Paths["/knowledge-bases/{id}/graph-search"]
			if methods["post"] == nil || len(methods) != 1 {
				t.Fatal("graph-search must be documented as a POST-only endpoint")
			}
			const prefix = "github_com_Usir233_GENYEE_internal_types."
			for definition, fields := range map[string][]string{
				"GraphSearchResult":      {"entities", "relationships", "source_chunks", "meta"},
				"GraphSearchEntity":      {"id", "knowledge_id", "source_chunk_ids"},
				"GraphSearchRelation":    {"source_id", "target_id", "source_chunk_ids"},
				"GraphSearchSourceChunk": {"content_truncated", "title_truncated"},
				"GraphSearchMeta":        {"truncated", "truncation_reasons"},
			} {
				properties := spec.Definitions[prefix+definition].Properties
				for _, field := range fields {
					if properties[field] == nil {
						t.Errorf("%s.%s must be documented", definition, field)
					}
				}
				for _, hidden := range []string{"tenant_id", "knowledge_base_id", "element_id"} {
					if definition != "GraphSearchResult" && properties[hidden] != nil {
						t.Errorf("%s leaks internal field %s", definition, hidden)
					}
				}
			}
		})
	}
}

func TestModelDeleteUsageContract(t *testing.T) {
	for _, tt := range swaggerDocuments() {
		t.Run(tt.name, func(t *testing.T) {
			assertModelDeleteUsageContract(t, tt.loadSpec(t), tt.parse)
		})
	}
}

func TestFAQEnabledFilterContract(t *testing.T) {
	for _, tt := range swaggerDocuments() {
		t.Run(tt.name, func(t *testing.T) {
			assertFAQEnabledFilterContract(t, tt.loadSpec(t), tt.parse)
		})
	}
}

func TestWikiPageBacklinkTitlesContract(t *testing.T) {
	for _, tt := range swaggerDocuments() {
		t.Run(tt.name, func(t *testing.T) {
			var spec struct {
				Paths map[string]map[string]struct {
					Responses map[string]struct {
						Schema struct {
							Ref string `json:"$ref" yaml:"$ref"`
						} `json:"schema" yaml:"schema"`
					} `json:"responses" yaml:"responses"`
				} `json:"paths" yaml:"paths"`
				Definitions map[string]struct {
					Properties map[string]map[string]any `json:"properties" yaml:"properties"`
				} `json:"definitions" yaml:"definitions"`
			}
			if err := tt.parse(tt.loadSpec(t), &spec); err != nil {
				t.Fatal(err)
			}
			const detail = "github_com_Usir233_GENYEE_internal_types.WikiPageDetail"
			get := spec.Paths["/knowledge-bases/{id}/wiki/pages/{slug}"]["get"]
			if get.Responses["200"].Schema.Ref != "#/definitions/"+detail {
				t.Fatal("page detail response must expose backlink titles")
			}
			properties := spec.Definitions[detail].Properties
			titles := properties["in_link_titles"]
			values, ok := titles["additionalProperties"].(map[string]any)
			if titles["type"] != "object" || !ok || values["type"] != "string" {
				t.Fatal("in_link_titles must be a string-valued map")
			}
			for field := range spec.Definitions["github_com_Usir233_GENYEE_internal_types.WikiPage"].Properties {
				if _, ok := properties[field]; !ok {
					t.Errorf("page detail dropped existing field %s", field)
				}
			}
			if _, ok := spec.Paths["/knowledgebase/{kb_id}/wiki/page-titles"]; ok {
				t.Fatal("obsolete standalone title endpoint remains documented")
			}
			if _, ok := spec.Paths["/knowledge-bases/{id}/wiki/page-titles"]; ok {
				t.Fatal("obsolete standalone title endpoint remains documented")
			}
		})
	}
}

func readSwaggerFile(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return data
}

func assertKnowledgeSearchRouteContract(t *testing.T, data []byte, parse func([]byte, any) error) {
	t.Helper()
	var spec struct {
		Paths map[string]map[string]any `json:"paths" yaml:"paths"`
	}
	if err := parse(data, &spec); err != nil {
		t.Fatalf("parse generated Swagger document: %v", err)
	}

	knowledgeSearch, ok := spec.Paths["/knowledge-search"]
	if !ok {
		t.Fatal("generated Swagger document does not expose /knowledge-search")
	}
	if _, ok := knowledgeSearch["post"]; !ok {
		t.Fatal("generated Swagger document does not expose POST /knowledge-search")
	}

	if staleRoute, ok := spec.Paths["/sessions/search"]; ok {
		if _, ok := staleRoute["post"]; ok {
			t.Fatal("generated Swagger document still exposes stale POST /sessions/search")
		}
	}
}

func assertModelDeleteUsageContract(t *testing.T, data []byte, parse func([]byte, any) error) {
	t.Helper()
	var spec struct {
		Paths map[string]map[string]struct {
			Responses map[string]any `json:"responses" yaml:"responses"`
		} `json:"paths" yaml:"paths"`
		Definitions map[string]map[string]any `json:"definitions" yaml:"definitions"`
	}
	if err := parse(data, &spec); err != nil {
		t.Fatalf("parse generated Swagger document: %v", err)
	}

	models, ok := spec.Paths["/models/{id}"]
	if !ok {
		t.Fatal("generated Swagger document does not expose /models/{id}")
	}
	deleteOperation, ok := models["delete"]
	if !ok {
		t.Fatal("generated Swagger document does not expose DELETE /models/{id}")
	}
	if _, ok := deleteOperation.Responses["400"]; !ok {
		t.Fatal("model DELETE Swagger contract does not document the model-in-use 400 response")
	}

	const errorCodeDefinition = "github_com_Usir233_GENYEE_internal_errors.ErrorCode"
	errorCodes, ok := spec.Definitions[errorCodeDefinition]
	if !ok {
		t.Fatalf("generated Swagger document does not expose %s", errorCodeDefinition)
	}
	enum, ok := errorCodes["enum"].([]any)
	if !ok {
		t.Fatalf("generated Swagger ErrorCode enum has unexpected shape: %#v", errorCodes["enum"])
	}
	enumVarNames, ok := errorCodes["x-enum-varnames"].([]any)
	if !ok {
		t.Fatalf("generated Swagger ErrorCode names have unexpected shape: %#v", errorCodes["x-enum-varnames"])
	}
	for i, value := range enum {
		if swaggerInteger(value) == 2300 {
			if i >= len(enumVarNames) || enumVarNames[i] != "ErrModelInUse" {
				t.Fatalf("error code 2300 must align with ErrModelInUse, got names=%v", enumVarNames)
			}
			return
		}
	}
	t.Fatal("generated Swagger ErrorCode enum does not include model-in-use code 2300")
}

func assertFAQEnabledFilterContract(t *testing.T, data []byte, parse func([]byte, any) error) {
	t.Helper()
	var spec struct {
		Paths map[string]map[string]swaggerOperation `json:"paths" yaml:"paths"`
	}
	if err := parse(data, &spec); err != nil {
		t.Fatalf("parse generated Swagger document: %v", err)
	}

	faqEntries, ok := spec.Paths["/knowledge-bases/{id}/faq/entries"]
	if !ok {
		t.Fatal("generated Swagger document does not expose FAQ entries route")
	}
	getOperation, ok := faqEntries["get"]
	if !ok {
		t.Fatal("generated Swagger document does not expose GET FAQ entries")
	}
	for _, parameter := range getOperation.Parameters {
		if parameter.Name != "is_enabled" {
			continue
		}
		if parameter.In != "query" || parameter.Type != "boolean" || parameter.Required {
			t.Fatalf("is_enabled must be an optional boolean query parameter, got %#v", parameter)
		}
		return
	}
	t.Fatal("GET FAQ entries does not document the is_enabled query parameter")
}

func swaggerInteger(value any) int {
	switch number := value.(type) {
	case int:
		return number
	case float64:
		return int(number)
	default:
		return 0
	}
}

func TestNotificationInboxContract(t *testing.T) {
	for _, tt := range swaggerDocuments() {
		t.Run(tt.name, func(t *testing.T) {
			var spec struct {
				Paths map[string]map[string]swaggerOperation `json:"paths" yaml:"paths"`
			}
			if err := tt.parse(tt.loadSpec(t), &spec); err != nil {
				t.Fatal(err)
			}
			if _, ok := spec.Paths["/notifications/read"]["delete"]; !ok {
				t.Fatal("missing clear-read notification endpoint")
			}
			for _, parameter := range spec.Paths["/notifications"]["get"].Parameters {
				if parameter.Name == "cursor" && parameter.In == "query" && parameter.Type == "string" {
					return
				}
			}
			t.Fatal("notification listing must document its history cursor")
		})
	}
}
