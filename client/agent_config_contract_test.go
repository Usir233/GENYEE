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

func TestAgentReadModifyWritePreservesOperationalConfig(t *testing.T) {
	// These fields exist in types.CustomAgentConfig. PUT replaces the entire config,
	// so losing them on GET changes permissions, memory and execution behavior.
	const config = `{"model_id":"model","access_level":"read_only","memory_enabled":false,"sandbox_config_id":"sandbox-1","mcp_auth_wait_timeout":45,"chat_parser_engine_rules":[{"file_types":["pdf"],"engine":"docreader"}],"attachment_image_understanding":true,"attachment_ocr_max_pages":4,"attachment_parse_wait_timeout_sec":90,"intent_prompts":{"greeting":"Hello"}}`
	var want map[string]any
	if err := json.Unmarshal([]byte(config), &want); err != nil {
		t.Fatal(err)
	}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/agents/agent-1" {
			t.Errorf("path=%s", r.URL.Path)
		}
		if r.Method == http.MethodPut {
			var req struct {
				Name   string         `json:"name"`
				Config map[string]any `json:"config"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Error(err)
			}
			if req.Name != "Renamed" {
				t.Errorf("name=%s", req.Name)
			}
			for key, value := range want {
				if !reflect.DeepEqual(req.Config[key], value) {
					t.Errorf("config.%s changed from %#v to %#v", key, value, req.Config[key])
				}
			}
		} else if r.Method != http.MethodGet {
			t.Errorf("method=%s", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"success":true,"data":{"id":"agent-1","name":"Original","config":`+config+`}}`)
	}))
	defer s.Close()
	c := NewClient(s.URL)
	got, err := c.GetAgent(context.Background(), "agent-1")
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.UpdateAgent(context.Background(), "agent-1", &UpdateAgentRequest{Name: "Renamed", Config: got.Config})
	if err != nil {
		t.Fatal(err)
	}
}
