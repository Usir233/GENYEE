// Package client provides the implementation for interacting with the GENYEE API
// The Evaluation related interfaces are used for starting and retrieving model evaluation task results
// Evaluation tasks can be used to measure model performance and
// compare different embedding models, chat models, and reranking models
package client

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// EvaluationStatus matches the numeric task status returned by the server.
type EvaluationStatus int

// EvaluationStatus values reported by the evaluation service.
const (
	EvaluationStatusPending EvaluationStatus = iota
	EvaluationStatusRunning
	EvaluationStatusSuccess
	EvaluationStatusFailed
)

// EvaluationTask contains the persisted task state and completed item count.
type EvaluationTask struct {
	ID        string           `json:"id"`
	TenantID  uint64           `json:"tenant_id"`
	DatasetID string           `json:"dataset_id"`
	StartTime time.Time        `json:"start_time"`
	Status    EvaluationStatus `json:"status"`
	ErrorMsg  string           `json:"err_msg,omitempty"`
	Total     int              `json:"total,omitempty"`
	Finished  int              `json:"finished,omitempty"`
}

// EvaluationResult is the detail returned by both evaluation endpoints.
// Metric is absent until the first QA pair finishes. Params contains the
// server's pipeline configuration and is kept as JSON fields as it evolves.
type EvaluationResult struct {
	Task   *EvaluationTask         `json:"task"`
	Params map[string]interface{}  `json:"params"`
	Metric *EvaluationMetricResult `json:"metric,omitempty"`
}

// EvaluationMetricResult aggregates retrieval and generation metrics for an evaluation task.
type EvaluationMetricResult struct {
	RetrievalMetrics  EvaluationRetrievalMetrics  `json:"retrieval_metrics"`
	GenerationMetrics EvaluationGenerationMetrics `json:"generation_metrics"`
}

// EvaluationRetrievalMetrics holds IR evaluation metrics.
type EvaluationRetrievalMetrics struct {
	Precision float64 `json:"precision"`
	Recall    float64 `json:"recall"`
	NDCG3     float64 `json:"ndcg3"`
	NDCG10    float64 `json:"ndcg10"`
	MRR       float64 `json:"mrr"`
	MAP       float64 `json:"map"`
}

// EvaluationGenerationMetrics holds text generation evaluation metrics.
type EvaluationGenerationMetrics struct {
	BLEU1  float64 `json:"bleu1"`
	BLEU2  float64 `json:"bleu2"`
	BLEU4  float64 `json:"bleu4"`
	ROUGE1 float64 `json:"rouge1"`
	ROUGE2 float64 `json:"rouge2"`
	ROUGEL float64 `json:"rougel"`
}

// EvaluationRequest represents an evaluation request
// Parameters used to start a new evaluation task
type EvaluationRequest struct {
	DatasetID       string `json:"dataset_id"`        // Dataset ID to evaluate
	KnowledgeBaseID string `json:"knowledge_base_id"` // Source knowledge base for embedding and summary settings
	ChatModelID     string `json:"chat_id"`           // Chat model ID
	RerankModelID   string `json:"rerank_id"`         // Reranking model ID
}

// EvaluationTaskResponse represents an evaluation task response
// API response structure for evaluation tasks
type EvaluationTaskResponse struct {
	Success bool             `json:"success"` // Whether operation was successful
	Data    EvaluationResult `json:"data"`    // Evaluation task detail
}

// EvaluationResultResponse represents an evaluation result response
// API response structure for evaluation results
type EvaluationResultResponse struct {
	Success bool             `json:"success"` // Whether operation was successful
	Data    EvaluationResult `json:"data"`    // Evaluation result data
}

// StartEvaluation starts an evaluation task
// Creates and starts a new evaluation task based on provided parameters
// Parameters:
//   - ctx: Context, used for passing request context information such as deadline, cancellation signals, etc.
//   - request: Evaluation request parameters, including dataset ID and model IDs
//
// Returns:
//   - *EvaluationTask: Created evaluation task information
//   - error: Error information if the request fails
func (c *Client) StartEvaluation(ctx context.Context, request *EvaluationRequest) (*EvaluationTask, error) {
	resp, err := c.doRequest(ctx, http.MethodPost, "/api/v1/evaluation", request, nil)
	if err != nil {
		return nil, err
	}

	var response EvaluationTaskResponse
	if err := parseResponse(resp, &response); err != nil {
		return nil, err
	}

	if response.Data.Task == nil || response.Data.Task.ID == "" {
		return nil, fmt.Errorf("evaluation response missing task ID")
	}
	return response.Data.Task, nil
}

// GetEvaluationResult retrieves evaluation results
// Retrieves detailed results for an evaluation task by task ID
// Parameters:
//   - ctx: Context, used for passing request context information
//   - taskID: Evaluation task ID, used to identify the specific evaluation task to query
//
// Returns:
//   - *EvaluationResult: Detailed evaluation task results
//   - error: Error information if the request fails
func (c *Client) GetEvaluationResult(ctx context.Context, taskID string) (*EvaluationResult, error) {
	queryParams := url.Values{}
	queryParams.Add("task_id", taskID)

	resp, err := c.doRequest(ctx, http.MethodGet, "/api/v1/evaluation", nil, queryParams)
	if err != nil {
		return nil, err
	}

	var response EvaluationResultResponse
	if err := parseResponse(resp, &response); err != nil {
		return nil, err
	}
	if response.Data.Task == nil || response.Data.Task.ID == "" {
		return nil, fmt.Errorf("evaluation response missing task ID")
	}

	return &response.Data, nil
}
