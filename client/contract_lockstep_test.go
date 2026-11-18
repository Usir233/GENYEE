package client

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestExtractServerCode_CanonicalAppErrorAndLegacyFallback(t *testing.T) {
	tests := []struct {
		name string
		body string
		want int
	}{
		{
			name: "canonical nested AppError envelope",
			body: `{"success":false,"error":{"code":2300,"message":"duplicate","details":{"id":"kb-1"}}}`,
			want: 2300,
		},
		{
			name: "canonical nested not-found code",
			body: `{"success":false,"error":{"code":1003,"message":"knowledge not found"}}`,
			want: ServerErrNotFound,
		},
		{
			name: "nested error code wins over zero top-level code",
			body: `{"code":0,"success":false,"error":{"code":2006,"message":"tenant required"}}`,
			want: 2006,
		},
		{
			name: "fallback to top-level code when nested error absent",
			body: `{"code":1005,"message":"conflict"}`,
			want: ServerErrConflict,
		},
		{
			name: "string error body without code returns zero",
			body: `{"success":false,"error":"plain string error"}`,
			want: 0,
		},
		{
			name: "non-JSON body returns zero",
			body: `502 Bad Gateway`,
			want: 0,
		},
		{
			name: "empty body returns zero",
			body: ``,
			want: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := extractServerCode([]byte(tc.body)); got != tc.want {
				t.Fatalf("extractServerCode(%s) = %d, want %d", tc.body, got, tc.want)
			}
			apiErr := newAPIError(http.StatusBadRequest, []byte(tc.body))
			if apiErr.Code != tc.want {
				t.Fatalf("newAPIError.Code = %d, want %d", apiErr.Code, tc.want)
			}
		})
	}
}

func TestClientSurfacesCanonicalAppErrorCodeOnHTTPFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"success":false,"error":{"code":1003,"message":"session not found"}}`)
	}))
	defer srv.Close()

	_, err := NewClient(srv.URL).GetSession(context.Background(), "sess-missing")
	if err == nil {
		t.Fatal("expected error on 404 response")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %T: %v", err, err)
	}
	if apiErr.StatusCode != http.StatusNotFound || apiErr.Code != ServerErrNotFound {
		t.Fatalf("got status=%d code=%d, want 404 and %d", apiErr.StatusCode, apiErr.Code, ServerErrNotFound)
	}
}

func TestMultipartEndpointsValidateBaseURL(t *testing.T) {
	dir := t.TempDir()
	docPath := filepath.Join(dir, "sample.txt")
	if err := os.WriteFile(docPath, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	imgPath := filepath.Join(dir, "sample.png")
	if err := os.WriteFile(imgPath, []byte("png"), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, badBase := range []string{"ftp://example.com", "http://", "not-a-url"} {
		c := NewClient(badBase)
		if _, err := c.CreateKnowledgeFromFile(
			context.Background(), "kb-1", docPath, nil, nil, "", "", nil,
		); err == nil {
			t.Errorf("CreateKnowledgeFromFile(%q) unexpectedly succeeded", badBase)
		}
		if _, err := c.TestMultimodalFunction(
			context.Background(), map[string]string{"vlm_model": "m"}, imgPath,
		); err == nil {
			t.Errorf("TestMultimodalFunction(%q) unexpectedly succeeded", badBase)
		}
		if _, err := c.UploadSandboxSkill(
			context.Background(), "cfg-1", "skill.zip", []byte("PK"),
		); err == nil {
			t.Errorf("UploadSandboxSkill(%q) unexpectedly succeeded", badBase)
		}
	}
}

func TestResolveDownloadPath_RejectsSymlinkCycle(t *testing.T) {
	dir := t.TempDir()
	linkA := filepath.Join(dir, "a")
	linkB := filepath.Join(dir, "b")
	if err := os.Symlink(linkB, linkA); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(linkA, linkB); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveDownloadPath(linkA); err == nil {
		t.Fatal("expected symlink cycle to fail")
	}
}

func TestContractLockstep_KnowledgeMultipartAndURLWireFields(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "local.pdf")
	if err := os.WriteFile(filePath, []byte("%PDF-1.4"), 0o600); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/knowledge-bases/kb-1/knowledge/file":
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Errorf("ParseMultipartForm: %v", err)
				return
			}
			defer func() { _ = r.MultipartForm.RemoveAll() }()
			if got := r.FormValue("enable_multimodal"); got != "true" {
				t.Errorf("enable_multimodal = %q, want \"true\"", got)
			}
			if legacy := r.FormValue("enable_multimodel"); legacy != "" {
				t.Errorf("legacy enable_multimodel sent: %q", legacy)
			}
			if got := r.FormValue("file_name"); got != "folder/custom.pdf" {
				t.Errorf("file_name = %q, want \"folder/custom.pdf\"", got)
			}
			if legacy := r.FormValue("fileName"); legacy != "" {
				t.Errorf("legacy fileName sent: %q", legacy)
			}
			var proc map[string]any
			if err := json.Unmarshal([]byte(r.FormValue("process_config")), &proc); err != nil {
				t.Errorf("decode process_config: %v", err)
			} else if proc["enable_multimodal"] != true {
				t.Errorf("process_config.enable_multimodal = %v, want true", proc["enable_multimodal"])
			}
		case "/api/v1/knowledge-bases/kb-1/knowledge/url":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode url body: %v", err)
			}
			if body["enable_multimodal"] != false {
				t.Errorf("url body enable_multimodal = %v, want false", body["enable_multimodal"])
			}
			if _, hasLegacy := body["enable_multimodel"]; hasLegacy {
				t.Errorf("legacy enable_multimodel present in url body: %v", body)
			}
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"success":true,"data":{"id":"doc-1","file_name":"custom.pdf"}}`)
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	tr := true
	if _, err := c.CreateKnowledgeFromFile(
		context.Background(), "kb-1", filePath, nil, &tr, "folder/custom.pdf", "api",
		&KnowledgeProcessOverrides{EnableMultimodal: &tr},
	); err != nil {
		t.Fatalf("CreateKnowledgeFromFile: %v", err)
	}

	fl := false
	if _, err := c.CreateKnowledgeFromURL(context.Background(), "kb-1", CreateKnowledgeFromURLRequest{
		URL:              "https://example.com/doc.pdf",
		EnableMultimodal: &fl,
	}); err != nil {
		t.Fatalf("CreateKnowledgeFromURL: %v", err)
	}
}

func TestContractLockstep_RoutesAndSnakeCaseDTOs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/auth/refresh":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode refresh body: %v", err)
			}
			if body["refresh_token"] != "rt-123" {
				t.Errorf("refresh_token = %v, want rt-123", body["refresh_token"])
			}
			if _, ok := body["refreshToken"]; ok {
				t.Errorf("legacy refreshToken key sent: %v", body)
			}
			_, _ = io.WriteString(w, `{"success":true,"access_token":"at-new","refresh_token":"rt-new"}`)

		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/models/providers":
			_, _ = io.WriteString(w, `{"success":true,"data":[{`+
				`"value":"openai","label":"OpenAI","description":"OpenAI API",`+
				`"requires_auth":true,"default_urls":{"chat":"https://api.openai.com/v1"},`+
				`"model_types":["chat","embedding"]}]}`)

		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/system/parser-engines":
			_, _ = io.WriteString(w, `{"code":0,"connected":true,"data":[{`+
				`"name":"docreader","description":"DocReader","file_types":["pdf","docx"],`+
				`"available":false,"unavailable_reason":"offline"}]}`)

		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/sessions/sess-1/generate-title":
			_, _ = io.WriteString(w, `{"success":true,"data":"Generated Title"}`)

		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/knowledge-bases/kb-1/faq/entries/batch":
			_, _ = io.WriteString(w, `{"success":true,"data":{"task_id":"faq-task-1"}}`)

		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/knowledge-bases/kb-1/faq/entries":
			_, _ = io.WriteString(w, `{"success":true,"data":{"id":42,"standard_question":"Q"}}`)

		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/web-search-providers/types":
			_, _ = io.WriteString(w, `{"success":true,"data":[{"id":"duckduckgo"}]}`)

		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(
				w,
				`{"success":false,"error":{"code":1003,"message":"unexpected route `+r.URL.Path+`"}}`,
			)
		}
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	ctx := context.Background()

	refResp, err := c.RefreshToken(ctx, "rt-123")
	if err != nil || refResp.AccessToken != "at-new" {
		t.Fatalf("RefreshToken: resp=%+v err=%v", refResp, err)
	}

	providers, err := c.ListModelProviders(ctx, "chat")
	if err != nil || len(providers) != 1 {
		t.Fatalf("ListModelProviders: %+v err=%v", providers, err)
	}
	if !providers[0].RequiresAuth || providers[0].DefaultURLs["chat"] != "https://api.openai.com/v1" ||
		len(providers[0].ModelTypes) != 2 {
		t.Fatalf("ModelProvider snake_case decode failed: %+v", providers[0])
	}

	engines, err := c.ListParserEngines(ctx)
	if err != nil || len(engines) != 1 {
		t.Fatalf("ListParserEngines: %+v err=%v", engines, err)
	}
	if engines[0].Name != "docreader" || len(engines[0].FileTypes) != 2 ||
		engines[0].UnavailableReason != "offline" {
		t.Fatalf("ParserEngine snake_case decode failed: %+v", engines[0])
	}

	title, err := c.GenerateTitle(ctx, "sess-1", &GenerateTitleRequest{})
	if err != nil || title != "Generated Title" {
		t.Fatalf("GenerateTitle: title=%q err=%v", title, err)
	}

	taskID, err := c.UpsertFAQEntries(ctx, "kb-1", &FAQBatchUpsertPayload{Mode: "append"})
	if err != nil || taskID != "faq-task-1" {
		t.Fatalf("UpsertFAQEntries: taskID=%q err=%v", taskID, err)
	}

	entry, err := c.CreateFAQEntry(ctx, "kb-1", &FAQEntryPayload{
		StandardQuestion: "Q",
		Answers:          []string{"A"},
	})
	if err != nil || entry == nil || entry.ID != 42 {
		t.Fatalf("CreateFAQEntry: entry=%+v err=%v", entry, err)
	}

	wsTypes, err := c.GetWebSearchProviders(ctx)
	if err != nil || len(wsTypes) != 1 {
		t.Fatalf("GetWebSearchProviders: %+v err=%v", wsTypes, err)
	}
}
