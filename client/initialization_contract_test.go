package client

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestInitializationReadWireContracts(t *testing.T) {
	for _, tc := range []struct {
		name, path, response string
		call                 func(*Client) error
	}{
		{"ollama list", "/api/v1/initialization/ollama/models", `{"success":true,"data":{"models":[{"name":"local-model","size":42,"modified_at":"2026-11-13T00:00:00Z"}]}}`, func(c *Client) error {
			got, err := c.ListOllamaModels(context.Background())
			if err == nil && (len(got) != 1 || got[0].Name != "local-model") {
				t.Errorf("lost model list: %+v", got)
			}
			return err
		}},
		{"ollama check", "/api/v1/initialization/ollama/models/check", `{"success":true,"data":{"models":{"present":true,"absent":false}}}`, func(c *Client) error {
			got, err := c.CheckOllamaModels(context.Background(), []string{"present", "absent"})
			if err == nil && (len(got) != 2 || !got["present"] || got["absent"]) {
				t.Errorf("lost model availability: %+v", got)
			}
			return err
		}},
		{"remote check", "/api/v1/initialization/remote/check", `{"success":true,"data":{"available":true,"message":"connected"}}`, func(c *Client) error {
			got, err := c.CheckRemoteModel(context.Background(), nil)
			if err == nil && (got == nil || !got.Success) {
				t.Errorf("available model reported failure: %+v", got)
			}
			return err
		}},
		{"embedding test", "/api/v1/initialization/embedding/test", `{"success":true,"data":{"available":true,"dimension":1024,"message":"connected"}}`, func(c *Client) error {
			got, err := c.TestEmbeddingModel(context.Background(), nil)
			if err == nil && (got == nil || !got.Success) {
				t.Errorf("available embedding reported failure: %+v", got)
			}
			return err
		}},
		{"rerank check", "/api/v1/initialization/rerank/check", `{"success":true,"data":{"available":true,"message":"connected"}}`, func(c *Client) error {
			got, err := c.CheckRerankModel(context.Background(), nil)
			if err == nil && (got == nil || !got.Success) {
				t.Errorf("available reranker reported failure: %+v", got)
			}
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != tc.path {
					t.Errorf("path = %s, want %s", r.URL.Path, tc.path)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, tc.response)
			}))
			defer s.Close()
			if err := tc.call(NewClient(s.URL)); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestOllamaDownloadWireContract(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var got map[string]string
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Error(err)
		}
		if got["modelName"] != "local-model" {
			t.Errorf("modelName = %q; handler requires it", got["modelName"])
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"success":true,"data":{"taskId":"download-1","modelName":"local-model","status":"pending","progress":0}}`)
	}))
	defer s.Close()
	got, err := NewClient(s.URL).DownloadOllamaModel(context.Background(), "local-model")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.ID != "download-1" {
		t.Errorf("lost polling task ID: %+v", got)
	}
}

func TestInitializationWriteWireContracts(t *testing.T) {
	for _, initialize := range []bool{true, false} {
		t.Run(map[bool]string{true: "initialize", false: "update"}[initialize], func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var got map[string]any
				if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
					t.Error(err)
				}
				if initialize {
					for _, key := range []string{"llm", "embedding", "documentSplitting"} {
						if got[key] == nil {
							t.Errorf("missing required initialization object %s: %+v", key, got)
						}
					}
				} else if got["llmModelId"] != "chat" || got["embeddingModelId"] != "embedding" {
					t.Errorf("wrong model binding payload: %+v", got)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"success":true}`)
			}))
			defer s.Close()
			c := NewClient(s.URL)
			var err error
			if initialize {
				var config InitializationConfig
				if err := json.Unmarshal([]byte(`{"llm":{"source":"remote","modelName":"chat"},"embedding":{"source":"remote","modelName":"embed"},"documentSplitting":{"chunkSize":512,"separators":["\n"]}}`), &config); err != nil {
					t.Fatal(err)
				}
				err = c.InitializeByKB(context.Background(), "kb", &config)
			} else {
				err = c.UpdateKBConfig(context.Background(), "kb", &InitializationConfig{ChatModelID: "chat", EmbeddingModelID: "embedding"})
			}
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestReconnectDocReaderReportsApplicationFailure(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"code":1,"msg":"connection failed"}`)
	}))
	defer s.Close()
	if err := NewClient(s.URL).ReconnectDocReader(context.Background(), "local-parser:50051"); err == nil {
		t.Fatal("HTTP 200 connection failure was reported as success")
	}
}

func TestMultimodalRequiresImageBeforeRequest(t *testing.T) {
	called := false
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"success":true,"data":{"success":true}}`)
	}))
	defer s.Close()
	_, err := NewClient(s.URL).TestMultimodalFunction(context.Background(), map[string]string{"vlm_model": "test-model"})
	if err == nil || called {
		t.Fatalf("image-less request must fail before HTTP, got err=%v called=%v", err, called)
	}
}

func TestMultimodalUploadWireContract(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test-image.png")
	if err := os.WriteFile(path, []byte("image-fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-API-Key") != "test-api-key" || r.Header.Get("X-Tenant-ID") != "7" {
			t.Errorf("multipart request lost authentication/tenant headers")
		}
		if err := r.ParseMultipartForm(1024); err != nil {
			t.Error(err)
			return
		}
		defer r.MultipartForm.RemoveAll()
		image, header, err := r.FormFile("image")
		if err != nil {
			t.Error(err)
			return
		}
		defer image.Close()
		if ct := header.Header.Get("Content-Type"); ct != "image/png" {
			t.Errorf("part Content-Type = %q, want image/png", ct)
		}
		data, err := io.ReadAll(image)
		if err != nil || string(data) != "image-fixture" || header.Filename != "test-image.png" {
			t.Errorf("unexpected upload: %s %q %v", header.Filename, data, err)
		}
		if r.FormValue("vlm_model") != "test-model" || r.FormValue("storage_type") != "minio" {
			t.Error("model/storage fields not sent as form data")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"success":true,"data":{"success":true,"caption":"caption","ocr":"text","processing_time":19}}`)
	}))
	defer s.Close()
	got, err := NewClient(s.URL, WithAPIKey("test-api-key"), WithTenantID(7)).TestMultimodalFunction(context.Background(), map[string]string{"vlm_model": "test-model", "storage_type": "minio"}, path)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || !got.Success || got.Caption != "caption" || got.OCR != "text" || got.ProcessingTime != 19 {
		t.Errorf("lost multimodal result: %+v", got)
	}
}

func TestModelCheckOutcomeDoesNotUseEnvelopeSuccess(t *testing.T) {
	for _, tc := range []struct {
		body      string
		success   bool
		dimension int
	}{
		{`{"available":false,"message":"connection refused"}`, false, 0},
		{`{"available":true,"dimension":1024}`, true, 1024},
		{`{"success":false,"caption":""}`, false, 0},
		{`{"success":true,"caption":"cat"}`, true, 0},
	} {
		var got ModelCheckResult
		if err := json.Unmarshal([]byte(tc.body), &got); err != nil {
			t.Fatal(err)
		}
		if got.Success != tc.success || got.Available != tc.success || got.Dimension != tc.dimension {
			t.Errorf("body=%s got=%+v", tc.body, got)
		}
		encoded, err := json.Marshal(got)
		if err != nil {
			t.Fatal(err)
		}
		var roundTrip ModelCheckResult
		if err := json.Unmarshal(encoded, &roundTrip); err != nil {
			t.Fatal(err)
		}
		if roundTrip.Success != tc.success || roundTrip.Available != tc.success || roundTrip.Dimension != tc.dimension {
			t.Errorf("round-trip lost outcome: body=%s encoded=%s roundTrip=%+v", tc.body, string(encoded), roundTrip)
		}
	}
}

func TestMultimodalUploadDetectsMagicBytesAndEscapesFilenameQuotes(t *testing.T) {
	jpegHeader := []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 'J', 'F', 'I', 'F', 0x00}
	path := filepath.Join(t.TempDir(), `sample"quote.bin`)
	if err := os.WriteFile(path, jpegHeader, 0o600); err != nil {
		t.Fatal(err)
	}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(4096); err != nil {
			t.Errorf("parse multipart failed: %v", err)
			return
		}
		defer r.MultipartForm.RemoveAll()
		f, header, err := r.FormFile("image")
		if err != nil {
			t.Errorf("FormFile(image) failed: %v", err)
			return
		}
		defer f.Close()
		if ct := header.Header.Get("Content-Type"); ct != "image/jpeg" {
			t.Errorf("Content-Type = %q, want image/jpeg", ct)
		}
		if header.Filename != `sample"quote.bin` {
			t.Errorf("Filename = %q, want sample\"quote.bin", header.Filename)
		}
		raw, err := io.ReadAll(f)
		if err != nil || string(raw) != string(jpegHeader) {
			t.Errorf("uploaded bytes mismatch: got %v (err=%v)", raw, err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"success":true,"data":{"success":true,"caption":"jpeg"}}`)
	}))
	defer s.Close()

	got, err := NewClient(s.URL).TestMultimodalFunction(context.Background(), map[string]string{"vlm_model": "vlm"}, path)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || !got.Success || !got.Available {
		t.Fatalf("expected successful multimodal result, got %+v", got)
	}
}
