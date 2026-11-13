package router

import (
	"net/http"
	"net/http/httptest"
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
