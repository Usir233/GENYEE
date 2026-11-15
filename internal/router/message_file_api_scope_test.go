package router

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Usir233/GENYEE/internal/types"
)

// A key keeps its session identity when an administrator narrows its KB scope.
// Historical retrieval evidence must not bypass the key's current allow-list.
func TestMessageSharedKBFilesRecheckCurrentAPIKeyScope(t *testing.T) {
	for _, tc := range []struct {
		name string
		ids  types.StringArray
		want int
	}{
		{"tenant-wide key", nil, http.StatusOK},
		{"allowed shared KB", types.StringArray{"kb-1"}, http.StatusOK},
		{"removed shared KB", types.StringArray{"another-kb"}, http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			engine, requestedPath := newOrgSharedKBTestEngine(t, true,
				types.References{{KnowledgeBaseID: "kb-1", Content: "resource://ShArEdKbHaNdLe00000000"}},
				[]*types.KnowledgeBase{{ID: "kb-1", TenantID: 10005}},
			)
			req := orgSharedKBFileRequest()
			req = req.WithContext(types.WithTenantAPIKeyScope(req.Context(), types.TenantAPIKeyScope{
				KeyID: 99, KnowledgeBaseIDs: tc.ids,
				Capabilities: types.StringArray{string(types.APIKeyCapabilityChat)},
			}))
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, req)
			if w.Code != tc.want {
				t.Fatalf("status=%d want=%d body=%q", w.Code, tc.want, w.Body.String())
			}
			if tc.want == http.StatusForbidden && *requestedPath != "" {
				t.Fatalf("out-of-scope file was opened: %s", *requestedPath)
			}
		})
	}
}

func TestMessageOwnKBFilesRecheckCurrentAPIKeyScope(t *testing.T) {
	const (
		callerTenantID = uint64(10006)
		handle         = "OwNKbHaNdLe00000000000"
		resourceRef    = "resource://" + handle
		physical       = "local://10006/images/own-kb.jpg"
	)
	for _, tc := range []struct {
		name     string
		filePath string
		resource *types.StoredResource
		bindings *types.MessageFileBindings
		artifacts types.MessageArtifacts
		ids      types.StringArray
		want     int
	}{
		{
			name:     "tenant-wide key serves same-workspace KB file",
			filePath: resourceRef,
			resource: &types.StoredResource{Handle: handle, TenantID: callerTenantID, PhysicalPath: physical, OriginalName: "own-kb.jpg"},
			bindings: &types.MessageFileBindings{KnowledgeBaseIDs: []string{"kb-own-1"}},
			ids:      nil,
			want:     http.StatusOK,
		},
		{
			name:     "KB-restricted key with allowed same-workspace KB serves file",
			filePath: resourceRef,
			resource: &types.StoredResource{Handle: handle, TenantID: callerTenantID, PhysicalPath: physical, OriginalName: "own-kb.jpg"},
			bindings: &types.MessageFileBindings{KnowledgeBaseIDs: []string{"kb-own-1"}},
			ids:      types.StringArray{"kb-own-1"},
			want:     http.StatusOK,
		},
		{
			name:     "KB-restricted key after removing same-workspace KB is denied",
			filePath: resourceRef,
			resource: &types.StoredResource{Handle: handle, TenantID: callerTenantID, PhysicalPath: physical, OriginalName: "own-kb.jpg"},
			bindings: &types.MessageFileBindings{KnowledgeBaseIDs: []string{"kb-own-1"}},
			ids:      types.StringArray{"kb-own-2"},
			want:     http.StatusForbidden,
		},
		{
			name:     "KB-restricted key rejects raw path without StoredResource",
			filePath: physical,
			resource: nil,
			bindings: nil,
			ids:      types.StringArray{"kb-own-1"},
			want:     http.StatusForbidden,
		},
		{
			name:      "KB-restricted key still serves live MessageArtifact",
			filePath:  resourceRef,
			resource:  &types.StoredResource{Handle: handle, TenantID: callerTenantID, PhysicalPath: physical, OriginalName: "report.pdf"},
			bindings:  &types.MessageFileBindings{MessageArtifact: true},
			artifacts: types.MessageArtifacts{{URL: resourceRef, FileName: "report.pdf"}},
			ids:       types.StringArray{"kb-own-2"},
			want:      http.StatusOK,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("STORAGE_TYPE", "local")
			var requestedPath string
			msg := &types.Message{
				ID:            "message-own-1",
				AgentID:       "own-agent-1",
				AgentTenantID: callerTenantID,
				Role:          "assistant",
				Content:       "Result: " + tc.filePath,
				Artifacts:     tc.artifacts,
				KnowledgeReferences: types.References{{
					KnowledgeBaseID: "kb-own-1",
					Content:         tc.filePath,
				}},
			}
			engine := newMessageScopedFilesTestEngine(
				callerTenantID,
				&stubMessageFileLookup{get: func(context.Context, string, string) (*types.Message, error) {
					return msg, nil
				}},
				&stubSharedAgentFileLookup{get: func(
					context.Context, uint64, types.TenantRole, string, ...uint64,
				) (*types.CustomAgent, error) {
					t.Fatal("shared-agent lookup must not run for same-workspace message")
					return nil, nil
				}},
				&stubTenantService{get: func(_ context.Context, id uint64) (*types.Tenant, error) {
					return &types.Tenant{ID: id}, nil
				}},
				&stubFileService{getFile: func(_ context.Context, path string) (io.ReadCloser, error) {
					requestedPath = path
					return io.NopCloser(strings.NewReader("own-kb-bytes")), nil
				}},
				&stubResourceCatalog{
					resource:     tc.resource,
					fileBindings: tc.bindings,
					bound: func(_ context.Context, tenant uint64, kb, _ string) (bool, error) {
						return tenant == callerTenantID && kb == "kb-own-1", nil
					},
				},
				messageKBShareAuthorizer{
					KBs: &stubKBTenantLookup{kbs: []*types.KnowledgeBase{{ID: "kb-own-1", TenantID: callerTenantID}}},
				},
			)
			req := httptest.NewRequest(http.MethodGet,
				"/sessions/session-1/messages/message-own-1/files?file_path="+url.QueryEscape(tc.filePath), nil)
			req = req.WithContext(types.WithTenantAPIKeyScope(req.Context(), types.TenantAPIKeyScope{
				KeyID:            101,
				KnowledgeBaseIDs: tc.ids,
				Capabilities:     types.StringArray{string(types.APIKeyCapabilityChat)},
			}))
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, req)
			if w.Code != tc.want {
				t.Fatalf("status=%d want=%d body=%q", w.Code, tc.want, w.Body.String())
			}
			if tc.want == http.StatusForbidden && requestedPath != "" {
				t.Fatalf("out-of-scope file was opened: %s", requestedPath)
			}
		})
	}
}
