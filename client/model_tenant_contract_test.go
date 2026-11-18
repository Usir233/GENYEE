package client

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestModelCredentialsAndDefaultSettingsContracts(t *testing.T) {
	// Ensure CreateModelRequest and UpdateModelRequest no longer carry write-noop IsDefault.
	if _, found := reflect.TypeOf(CreateModelRequest{}).FieldByName("IsDefault"); found {
		t.Fatalf("CreateModelRequest must not carry IsDefault")
	}
	if _, found := reflect.TypeOf(UpdateModelRequest{}).FieldByName("IsDefault"); found {
		t.Fatalf("UpdateModelRequest must not carry IsDefault")
	}
	// Ensure TenantAPIKey and CreateTenantAPIKeyRequest no longer carry deprecated Role.
	if _, found := reflect.TypeOf(TenantAPIKey{}).FieldByName("Role"); found {
		t.Fatalf("TenantAPIKey must not carry deprecated Role")
	}
	if _, found := reflect.TypeOf(CreateTenantAPIKeyRequest{}).FieldByName("Role"); found {
		t.Fatalf("CreateTenantAPIKeyRequest must not carry deprecated Role")
	}

	var putCredBody map[string]any
	var deletedField string
	storedDefaults := DefaultSettings{ChatModelID: "mdl-old", EmbeddingModelID: "emb-1"}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/models/mdl-1/credentials", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("method = %s, want PUT", r.Method)
		}
		if err := json.NewDecoder(r.Body).Decode(&putCredBody); err != nil {
			t.Errorf("decode credentials body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"success":true,"data":{"fields":`+
			`{"api_key":{"configured":true},"app_secret":{"configured":false}}}}`)
	})
	mux.HandleFunc("/api/v1/models/mdl-1/credentials/api_key", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("method = %s, want DELETE", r.Method)
		}
		deletedField = "api_key"
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/api/v1/tenants/kv/default-settings", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": storedDefaults})
		case http.MethodPut:
			if err := json.NewDecoder(r.Body).Decode(&storedDefaults); err != nil {
				t.Errorf("decode default-settings PUT: %v", err)
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": storedDefaults})
		default:
			t.Errorf("unexpected method %s on default-settings", r.Method)
		}
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := NewClient(srv.URL)
	apiKey := "sk-new-secret"
	credResp, err := c.UpdateModelCredentials(context.Background(), "mdl-1", &ModelCredentialsRequest{APIKey: &apiKey})
	if err != nil {
		t.Fatalf("UpdateModelCredentials: %v", err)
	}
	if putCredBody["api_key"] != "sk-new-secret" {
		t.Fatalf("unexpected credentials payload: %+v", putCredBody)
	}
	if !credResp.Fields["api_key"].Configured {
		t.Fatalf("expected api_key configured=true, got %+v", credResp)
	}

	if err := c.DeleteModelCredential(context.Background(), "mdl-1", "api_key"); err != nil {
		t.Fatalf("DeleteModelCredential: %v", err)
	}
	if deletedField != "api_key" {
		t.Fatalf("expected DELETE on api_key, got %q", deletedField)
	}

	curDefaults, err := c.GetDefaultSettings(context.Background())
	if err != nil {
		t.Fatalf("GetDefaultSettings: %v", err)
	}
	if id, ok := curDefaults.SlotModelID(ModelTypeKnowledgeQA); !ok || id != "mdl-old" {
		t.Fatalf("unexpected KnowledgeQA slot: id=%q ok=%v", id, ok)
	}
	if !curDefaults.SetSlotModelID(ModelTypeKnowledgeQA, "mdl-1") {
		t.Fatalf("SetSlotModelID(ModelTypeKnowledgeQA) returned false")
	}
	updatedDefaults, err := c.UpdateDefaultSettings(context.Background(), curDefaults)
	if err != nil {
		t.Fatalf("UpdateDefaultSettings: %v", err)
	}
	if updatedDefaults.ChatModelID != "mdl-1" || updatedDefaults.EmbeddingModelID != "emb-1" {
		t.Fatalf("unexpected updated DefaultSettings: %+v", updatedDefaults)
	}
}
