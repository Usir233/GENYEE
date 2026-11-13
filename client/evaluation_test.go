package client

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// This is the EvaluationDetail envelope emitted by handler.EvaluationHandler,
// including numeric status and nested task/params/metric objects.
const evaluationWireResponse = `{"success":true,"data":{"task":{"id":"evaluation-1","tenant_id":7,"dataset_id":"custom","start_time":"2026-11-13T00:00:00Z","status":2,"total":4,"finished":4},"params":{"chat_model_id":"chat-model","rerank_model_id":"rerank-model"},"metric":{"retrieval_metrics":{"precision":0.5,"recall":1,"ndcg3":0.6309297535714574,"ndcg10":0.6309297535714574,"mrr":0.5,"map":0.5},"generation_metrics":{"bleu1":0.8,"bleu2":0.7,"bleu4":0.6,"rouge1":0.5,"rouge2":0.4,"rougel":0.3}}}}`

func TestEvaluationClientWireContract(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/evaluation" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		if r.Method == http.MethodPost {
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if body["knowledge_base_id"] != "source-kb" {
				t.Errorf("knowledge_base_id = %v, want source-kb", body["knowledge_base_id"])
			}
			if _, exists := body["embedding_id"]; exists {
				t.Error("server does not accept embedding_id for evaluation")
			}
		} else if r.Method != http.MethodGet || r.URL.Query().Get("task_id") != "evaluation-1" {
			t.Errorf("unexpected result request %s %s", r.Method, r.URL.String())
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, evaluationWireResponse)
	}))
	defer server.Close()
	c := NewClient(server.URL)
	var request EvaluationRequest
	if err := json.Unmarshal([]byte(`{"dataset_id":"custom","knowledge_base_id":"source-kb","chat_id":"chat-model","rerank_id":"rerank-model"}`), &request); err != nil {
		t.Fatal(err)
	}
	task, err := c.StartEvaluation(context.Background(), &request)
	if err != nil {
		t.Fatal(err)
	}
	if task.ID != "evaluation-1" {
		t.Errorf("started task ID = %q, want evaluation-1", task.ID)
	}
	result, err := c.GetEvaluationResult(context.Background(), "evaluation-1")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got["task"] == nil || got["metric"] == nil || got["params"] == nil {
		t.Errorf("evaluation detail lost nested task, metric or params: %s", raw)
	}
	if task.Status != EvaluationStatusSuccess || task.TenantID != 7 || task.Total != 4 || task.Finished != 4 || task.StartTime.IsZero() {
		t.Errorf("task state was not preserved: %+v", task)
	}
	if result.Metric == nil || result.Metric.RetrievalMetrics.MRR != .5 || result.Metric.GenerationMetrics.ROUGEL != .3 {
		t.Errorf("metrics were not preserved: %+v", result.Metric)
	}
	if result.Params["chat_model_id"] != "chat-model" || result.Params["rerank_model_id"] != "rerank-model" {
		t.Errorf("pipeline params were not preserved: %+v", result.Params)
	}
}

func TestEvaluationClientTaskStates(t *testing.T) {
	for _, status := range []EvaluationStatus{EvaluationStatusPending, EvaluationStatusRunning, EvaluationStatusSuccess, EvaluationStatusFailed} {
		t.Run(string(rune('0'+status)), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]any{
					"task": map[string]any{"id": "task", "status": int(status), "err_msg": "failure reason"},
				}})
			}))
			defer server.Close()
			got, err := NewClient(server.URL).GetEvaluationResult(context.Background(), "task")
			if err != nil {
				t.Fatal(err)
			}
			if got.Task.Status != status || got.Task.ErrorMsg != "failure reason" || got.Metric != nil {
				t.Errorf("unexpected task detail: %+v", got)
			}
		})
	}
}

func TestEvaluationClientRejectsMissingTaskID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"success":true,"data":{}}`)
	}))
	defer server.Close()
	c := NewClient(server.URL)
	_, startErr := c.StartEvaluation(context.Background(), &EvaluationRequest{})
	_, getErr := c.GetEvaluationResult(context.Background(), "task")
	for _, err := range []error{startErr, getErr} {
		if err == nil || !strings.Contains(err.Error(), "missing task ID") {
			t.Errorf("expected missing task error, got %v", err)
		}
	}
}
