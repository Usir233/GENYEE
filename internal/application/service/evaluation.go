package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Usir233/GENYEE/internal/config"
	"github.com/Usir233/GENYEE/internal/logger"
	"github.com/Usir233/GENYEE/internal/types"
	"github.com/Usir233/GENYEE/internal/types/interfaces"
	"github.com/Usir233/GENYEE/internal/utils"
	"golang.org/x/sync/errgroup"
)

/*
corpus: pid -> content
queries: qid -> content
answers: aid -> content
qrels: qid -> pid
arels: qid -> aid
*/

// EvaluationService handles evaluation tasks for knowledge base and chat models
type EvaluationService struct {
	config               *config.Config                  // Application configuration
	dataset              interfaces.DatasetService       // Service for dataset operations
	knowledgeBaseService interfaces.KnowledgeBaseService // Service for knowledge base operations
	knowledgeService     interfaces.KnowledgeService     // Service for knowledge operations
	sessionService       interfaces.SessionService       // Service for chat sessions
	modelService         interfaces.ModelService         // Service for model operations

	evaluationMemoryStorage *evaluationMemoryStorage // In-memory + local JSON snapshot storage for evaluation tasks
}

func NewEvaluationService(
	config *config.Config,
	dataset interfaces.DatasetService,
	knowledgeBaseService interfaces.KnowledgeBaseService,
	knowledgeService interfaces.KnowledgeService,
	sessionService interfaces.SessionService,
	modelService interfaces.ModelService,
) interfaces.EvaluationService {
	evaluationMemoryStorage := newEvaluationMemoryStorage()
	return &EvaluationService{
		config:                  config,
		dataset:                 dataset,
		knowledgeBaseService:    knowledgeBaseService,
		knowledgeService:        knowledgeService,
		sessionService:          sessionService,
		modelService:            modelService,
		evaluationMemoryStorage: evaluationMemoryStorage,
	}
}

// evaluationMemoryStorage stores evaluation tasks in memory with thread-safe access and optional local JSON persistence
type evaluationMemoryStorage struct {
	store    map[string]*types.EvaluationDetail // Map of taskID to evaluation details
	mu       *sync.RWMutex                      // Read-write lock for concurrent access
	filePath string                             // Optional JSON snapshot file path
}

func resolveEvaluationStoragePath() string {
	if p := strings.TrimSpace(os.Getenv("EVALUATION_STORAGE_PATH")); p != "" {
		return p
	}
	if base := strings.TrimSpace(os.Getenv("LOCAL_STORAGE_BASE_DIR")); base != "" {
		return filepath.Join(base, "evaluation_tasks.json")
	}
	return filepath.Join("./data", "evaluation_tasks.json")
}

func newEvaluationMemoryStorage() *evaluationMemoryStorage {
	return newEvaluationMemoryStorageWithPath(resolveEvaluationStoragePath())
}

func newEvaluationMemoryStorageWithPath(filePath string) *evaluationMemoryStorage {
	res := &evaluationMemoryStorage{
		store:    make(map[string]*types.EvaluationDetail),
		mu:       &sync.RWMutex{},
		filePath: filePath,
	}
	res.loadFromDisk()
	return res
}

func (e *evaluationMemoryStorage) loadFromDisk() {
	if e.filePath == "" {
		return
	}
	raw, err := os.ReadFile(e.filePath)
	if err != nil {
		return
	}
	var loaded map[string]*types.EvaluationDetail
	if err := json.Unmarshal(raw, &loaded); err != nil {
		logger.Warnf(context.Background(), "Failed to parse evaluation storage file %s: %v", e.filePath, err)
		return
	}
	mutated := false
	for id, detail := range loaded {
		if detail == nil || detail.Task == nil {
			continue
		}
		if detail.Task.Status == types.EvaluationStatuePending || detail.Task.Status == types.EvaluationStatueRunning {
			detail.Task.Status = types.EvaluationStatueFailed
			if detail.Task.ErrMsg == "" {
				detail.Task.ErrMsg = "evaluation task interrupted by service restart"
			}
			mutated = true
		}
		e.store[id] = detail
	}
	if mutated {
		e.persistLocked()
	}
}

func (e *evaluationMemoryStorage) persistLocked() {
	if e.filePath == "" {
		return
	}
	dir := filepath.Dir(e.filePath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		logger.Warnf(context.Background(), "Failed to create evaluation storage dir %s: %v", dir, err)
		return
	}
	raw, err := json.MarshalIndent(e.store, "", "  ")
	if err != nil {
		logger.Warnf(context.Background(), "Failed to serialize evaluation tasks: %v", err)
		return
	}
	tmpPath := e.filePath + ".tmp"
	if err := os.WriteFile(tmpPath, raw, 0o600); err != nil {
		logger.Warnf(context.Background(), "Failed to write evaluation snapshot %s: %v", tmpPath, err)
		return
	}
	if err := os.Rename(tmpPath, e.filePath); err != nil {
		logger.Warnf(context.Background(), "Failed to commit evaluation snapshot %s: %v", e.filePath, err)
	}
}

func cloneEvaluationDetail(src *types.EvaluationDetail) *types.EvaluationDetail {
	if src == nil {
		return nil
	}
	dst := &types.EvaluationDetail{}
	if src.Task != nil {
		taskCopy := *src.Task
		dst.Task = &taskCopy
	}
	if src.Params != nil {
		dst.Params = src.Params.Clone()
	}
	if src.Metric != nil {
		metricCopy := *src.Metric
		dst.Metric = &metricCopy
	}
	return dst
}

func (e *evaluationMemoryStorage) register(params *types.EvaluationDetail) {
	e.mu.Lock()
	defer e.mu.Unlock()
	logger.Infof(context.Background(), "Registering evaluation task: %s", params.Task.ID)
	e.store[params.Task.ID] = cloneEvaluationDetail(params)
	e.persistLocked()
}

func (e *evaluationMemoryStorage) get(taskID string) (*types.EvaluationDetail, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	logger.Infof(context.Background(), "Getting evaluation task: %s", taskID)
	res, ok := e.store[taskID]
	if !ok {
		return nil, errors.New("task not found")
	}
	return cloneEvaluationDetail(res), nil
}

func (e *evaluationMemoryStorage) update(taskID string, fn func(params *types.EvaluationDetail)) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	params, ok := e.store[taskID]
	if !ok {
		return errors.New("task not found")
	}
	fn(params)
	e.persistLocked()
	return nil
}

func (e *EvaluationService) EvaluationResult(ctx context.Context, taskID string) (*types.EvaluationDetail, error) {
	logger.Info(ctx, "Start getting evaluation result")
	logger.Infof(ctx, "Task ID: %s", taskID)

	detail, err := e.evaluationMemoryStorage.get(taskID)
	if err != nil {
		logger.Errorf(ctx, "Failed to get evaluation task: %v", err)
		return nil, err
	}

	tenantID := types.MustTenantIDFromContext(ctx)
	logger.Infof(
		ctx,
		"Checking tenant ID match, task tenant ID: %d, current tenant ID: %d",
		detail.Task.TenantID, tenantID,
	)

	if tenantID != detail.Task.TenantID {
		logger.Error(ctx, "Tenant ID mismatch")
		return nil, errors.New("tenant ID does not match")
	}

	logger.Info(ctx, "Evaluation result retrieved successfully")
	return detail, nil
}

// Evaluation starts a new evaluation task with given parameters
// datasetID: ID of the dataset to evaluate against
// knowledgeBaseID: ID of the knowledge base to use (empty to create new)
// chatModelID: ID of the chat model to evaluate
// rerankModelID: ID of the rerank model to evaluate
func (e *EvaluationService) Evaluation(ctx context.Context,
	datasetID string, knowledgeBaseID string, chatModelID string, rerankModelID string,
) (*types.EvaluationDetail, error) {
	logger.Info(ctx, "Start evaluation")
	logger.Infof(ctx, "Dataset ID: %s, Knowledge Base ID: %s, Chat Model ID: %s, Rerank Model ID: %s",
		datasetID, knowledgeBaseID, chatModelID, rerankModelID)

	// Get tenant ID from context for multi-tenancy support
	tenantID := types.MustTenantIDFromContext(ctx)
	logger.Infof(ctx, "Tenant ID: %d", tenantID)

	// Handle knowledge base creation if not provided
	if knowledgeBaseID == "" {
		logger.Info(ctx, "No knowledge base ID provided, creating new knowledge base")
		// Create new knowledge base with default evaluation settings
		// 获取默认的嵌入模型和LLM模型
		models, err := e.modelService.ListModels(ctx)
		if err != nil {
			logger.Errorf(ctx, "Failed to list models: %v", err)
			return nil, err
		}

		embeddingModel := types.PickDefaultModel(models, types.ModelTypeEmbedding)
		llmModel := types.PickChatModel(models, "")
		if embeddingModel == nil || llmModel == nil {
			return nil, fmt.Errorf("no default models found for evaluation")
		}
		embeddingModelID, llmModelID := embeddingModel.ID, llmModel.ID

		kb, err := e.knowledgeBaseService.CreateKnowledgeBase(ctx, &types.KnowledgeBase{
			Name:             "evaluation",
			Description:      "evaluation",
			EmbeddingModelID: embeddingModelID,
			SummaryModelID:   llmModelID,
		})
		if err != nil {
			logger.Errorf(ctx, "Failed to create knowledge base: %v", err)
			return nil, err
		}
		knowledgeBaseID = kb.ID
		logger.Infof(ctx, "Created new knowledge base with ID: %s", knowledgeBaseID)
	} else {
		logger.Infof(ctx, "Using existing knowledge base ID: %s", knowledgeBaseID)
		// Create evaluation-specific knowledge base based on existing one
		kb, err := e.knowledgeBaseService.GetKnowledgeBaseByID(ctx, knowledgeBaseID)
		if err != nil {
			logger.Errorf(ctx, "Failed to get knowledge base: %v", err)
			return nil, err
		}

		kb, err = e.knowledgeBaseService.CreateKnowledgeBase(ctx, &types.KnowledgeBase{
			Name:             "evaluation",
			Description:      "evaluation",
			EmbeddingModelID: kb.EmbeddingModelID,
			SummaryModelID:   kb.SummaryModelID,
		})
		if err != nil {
			logger.Errorf(ctx, "Failed to create knowledge base: %v", err)
			return nil, err
		}
		knowledgeBaseID = kb.ID
		logger.Infof(ctx, "Created new knowledge base with ID: %s based on existing one", knowledgeBaseID)
	}
	// Until the background task takes ownership, failures in model resolution
	// or task registration must still remove the temporary knowledge base.
	backgroundStarted := false
	defer func() {
		if !backgroundStarted {
			if err := e.cleanupEvaluation(ctx, knowledgeBaseID, ""); err != nil {
				logger.Errorf(ctx, "Failed to clean up unstarted evaluation: %v", err)
			}
		}
	}()

	// Set default values for optional parameters
	if datasetID == "" {
		datasetID = "default"
		logger.Info(ctx, "Using default dataset")
	}

	if rerankModelID == "" {
		// 获取默认的重排模型
		models, err := e.modelService.ListModels(ctx)
		if err == nil {
			if model := types.PickDefaultModel(models, types.ModelTypeRerank); model != nil {
				rerankModelID = model.ID
			}
		}
		if rerankModelID == "" {
			logger.Warnf(ctx, "No rerank model found, skipping rerank")
		} else {
			logger.Infof(ctx, "Using default rerank model: %s", rerankModelID)
		}
	}

	if chatModelID == "" {
		// 获取默认的LLM模型
		models, err := e.modelService.ListModels(ctx)
		if err == nil {
			if model := types.PickChatModel(models, ""); model != nil {
				chatModelID = model.ID
			}
		}
		if chatModelID == "" {
			return nil, fmt.Errorf("no default chat model found")
		}
		logger.Infof(ctx, "Using default chat model: %s", chatModelID)
	}

	// Create evaluation task with unique ID
	logger.Info(ctx, "Creating evaluation task")
	taskID := utils.GenerateTaskID("evaluation", tenantID, datasetID)
	logger.Infof(ctx, "Generated task ID: %s", taskID)

	// Prepare evaluation detail with all parameters
	detail := &types.EvaluationDetail{
		Task: &types.EvaluationTask{
			ID:        taskID,
			TenantID:  tenantID,
			DatasetID: datasetID,
			Status:    types.EvaluationStatuePending,
			StartTime: time.Now(),
		},
		Params: &types.ChatManage{
			PipelineRequest: types.PipelineRequest{
				VectorThreshold:  e.config.Conversation.VectorThreshold,
				KeywordThreshold: e.config.Conversation.KeywordThreshold,
				EmbeddingTopK:    e.config.Conversation.EmbeddingTopK,
				MaxRounds:        e.config.Conversation.MaxRounds,
				RerankModelID:    rerankModelID,
				RerankTopK:       e.config.Conversation.RerankTopK,
				RerankThreshold:  e.config.Conversation.RerankThreshold,
				ChatModelID:      chatModelID,
				SummaryConfig: types.SummaryConfig{
					MaxTokens:           e.config.Conversation.Summary.MaxTokens,
					RepeatPenalty:       e.config.Conversation.Summary.RepeatPenalty,
					TopK:                e.config.Conversation.Summary.TopK,
					TopP:                e.config.Conversation.Summary.TopP,
					Prompt:              e.config.Conversation.Summary.Prompt,
					ContextTemplate:     e.config.Conversation.Summary.ContextTemplate,
					FrequencyPenalty:    e.config.Conversation.Summary.FrequencyPenalty,
					PresencePenalty:     e.config.Conversation.Summary.PresencePenalty,
					NoMatchPrefix:       e.config.Conversation.Summary.NoMatchPrefix,
					Temperature:         e.config.Conversation.Summary.Temperature,
					Seed:                e.config.Conversation.Summary.Seed,
					MaxCompletionTokens: e.config.Conversation.Summary.MaxCompletionTokens,
				},
				FallbackResponse:    e.config.Conversation.FallbackResponse,
				RewritePromptSystem: e.config.Conversation.RewritePromptSystem,
				RewritePromptUser:   e.config.Conversation.RewritePromptUser,
			},
		},
	}

	// Store evaluation task in memory storage
	logger.Info(ctx, "Registering evaluation task")
	e.evaluationMemoryStorage.register(detail)

	// Start evaluation in background goroutine
	logger.Info(ctx, "Starting evaluation in background")
	newCtx := logger.CloneContext(ctx)
	backgroundStarted = true
	go e.runEvaluation(newCtx, detail, knowledgeBaseID)

	logger.Infof(ctx, "Evaluation task created successfully, task ID: %s", taskID)
	return detail, nil
}

func (e *EvaluationService) runEvaluation(ctx context.Context, detail *types.EvaluationDetail, knowledgeBaseID string) {
	var runErr error
	defer func() {
		if r := recover(); r != nil {
			runErr = fmt.Errorf("panic in background evaluation: %v", r)
		}
		_ = e.evaluationMemoryStorage.update(detail.Task.ID, func(params *types.EvaluationDetail) {
			if runErr != nil {
				params.Task.Status = types.EvaluationStatueFailed
				params.Task.ErrMsg = runErr.Error()
			} else {
				params.Task.Status = types.EvaluationStatueSuccess
				params.Task.ErrMsg = ""
			}
		})
		if runErr != nil {
			logger.Errorf(ctx, "Evaluation task failed: %v, task ID: %s", runErr, detail.Task.ID)
		}
	}()
	_ = e.evaluationMemoryStorage.update(detail.Task.ID, func(params *types.EvaluationDetail) {
		params.Task.Status = types.EvaluationStatueRunning
	})
	runErr = e.EvalDataset(ctx, detail, knowledgeBaseID)
}

func (e *EvaluationService) cleanupEvaluation(ctx context.Context, knowledgeBaseID, knowledgeID string) error {
	// Failed/cancelled work must not cancel cleanup. Keep the caller's scope,
	// but bound cleanup independently so shutdown cannot wait forever.
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
	defer cancel()
	var cleanupErr error
	if knowledgeID != "" {
		if err := deleteReferencedKnowledge(cleanupCtx, e.knowledgeService, knowledgeBaseID, []string{knowledgeID}); err != nil {
			cleanupErr = fmt.Errorf("delete evaluation knowledge %s: %w", knowledgeID, err)
		}
	}
	if err := e.knowledgeBaseService.DeleteKnowledgeBase(cleanupCtx, knowledgeBaseID); err != nil {
		cleanupErr = errors.Join(cleanupErr, fmt.Errorf("delete evaluation knowledge base %s: %w", knowledgeBaseID, err))
	}
	return cleanupErr
}

// EvalDataset performs the actual evaluation of a dataset
// Processes each QA pair in parallel and records metrics
func (e *EvaluationService) EvalDataset(ctx context.Context, detail *types.EvaluationDetail, knowledgeBaseID string) (err error) {
	logger.Info(ctx, "Start evaluating dataset")
	logger.Infof(ctx, "Task ID: %s, Dataset ID: %s", detail.Task.ID, detail.Task.DatasetID)

	var createdKnowledgeID string
	// Setup cleanup of temporary resources immediately upon entry
	defer func() {
		err = errors.Join(err, e.cleanupEvaluation(ctx, knowledgeBaseID, createdKnowledgeID))
	}()
	if err := ctx.Err(); err != nil {
		return err
	}

	// Retrieve dataset from storage
	dataset, err := e.dataset.GetDatasetByID(ctx, detail.Task.DatasetID)
	if err != nil {
		logger.Errorf(ctx, "Failed to get dataset: %v", err)
		return err
	}
	logger.Infof(ctx, "Dataset retrieved successfully with %d QA pairs", len(dataset))
	if len(dataset) == 0 {
		return errors.New("evaluation dataset contains no QA pairs")
	}

	// Update total QA pairs count in task details
	e.evaluationMemoryStorage.update(detail.Task.ID, func(params *types.EvaluationDetail) {
		params.Task.Total = len(dataset)
		logger.Infof(ctx, "Updated task total to %d QA pairs", params.Task.Total)
	})

	// Extract and organize passages from dataset
	passages, err := getPassageList(dataset)
	if err != nil {
		return err
	}
	logger.Infof(ctx, "Creating knowledge from %d passages", len(passages))

	// Create knowledge base from passages (sync: wait for indexing to complete before querying)
	knowledge, err := e.knowledgeService.CreateKnowledgeFromPassageSync(ctx, knowledgeBaseID, passages, "")
	if err != nil {
		logger.Errorf(ctx, "Failed to create knowledge from passages: %v", err)
		return err
	}
	createdKnowledgeID = knowledge.ID
	logger.Infof(ctx, "Knowledge created and indexed successfully, ID: %s", knowledge.ID)

	// Initialize parallel evaluation metrics
	var finished int
	g, workerCtx := errgroup.WithContext(ctx)
	metricHook := NewHookMetric(len(dataset))

	// Set worker limit based on available CPUs
	g.SetLimit(max(runtime.GOMAXPROCS(0)-1, 1))
	logger.Infof(ctx, "Starting evaluation with %d parallel workers", max(runtime.GOMAXPROCS(0)-1, 1))

	// Process each QA pair in parallel
	for i, qaPair := range dataset {
		if workerCtx.Err() != nil {
			break
		}
		qaPair := qaPair
		i := i
		g.Go(func() (workerErr error) {
			// recover only protects its own goroutine: the task-level guard
			// cannot catch a panic raised by an errgroup worker.
			defer func() {
				if r := recover(); r != nil {
					workerErr = fmt.Errorf("panic evaluating QA pair %d: %v", i, r)
				}
			}()
			if err := workerCtx.Err(); err != nil {
				return err
			}
			logger.Infof(ctx, "Processing QA pair %d, question: %s", i, qaPair.Question)

			// Prepare chat management parameters for this QA pair
			chatManage := detail.Params.Clone()
			chatManage.Query = qaPair.Question
			chatManage.RewriteQuery = qaPair.Question
			// Set knowledge base ID and search targets for this evaluation
			chatManage.KnowledgeBaseIDs = []string{knowledgeBaseID}
			chatManage.SearchTargets = types.SearchTargets{
				&types.SearchTarget{
					Type:            types.SearchTargetTypeKnowledgeBase,
					KnowledgeBaseID: knowledgeBaseID,
				},
			}

			// Execute knowledge QA pipeline
			logger.Infof(ctx, "Running knowledge QA for question: %s", qaPair.Question)
			err := e.sessionService.KnowledgeQAByEvent(workerCtx, chatManage, types.Pipline["rag"])
			if err != nil {
				logger.Errorf(ctx, "Failed to process question %d: %v", i, err)
				return err
			}
			if err := workerCtx.Err(); err != nil {
				return err
			}

			// Record evaluation metrics
			logger.Infof(ctx, "Recording metrics for QA pair %d", i)
			metricHook.recordInit(i)
			metricHook.recordQaPair(i, qaPair)
			metricHook.recordSearchResult(i, chatManage.SearchResult)
			metricHook.recordRerankResult(i, chatManage.RerankResult)
			metricHook.recordChatResponse(i, chatManage.ChatResponse)
			metricHook.recordFinish(i)

			// Update progress metrics atomically under storage lock
			_ = e.evaluationMemoryStorage.update(detail.Task.ID, func(params *types.EvaluationDetail) {
				finished += 1
				params.Metric = metricHook.MetricResult()
				params.Task.Finished = finished
				logger.Infof(ctx, "Updated task progress: %d/%d completed", finished, params.Task.Total)
			})
			return nil
		})
	}

	// Wait for all parallel evaluations to complete
	logger.Info(ctx, "Waiting for all evaluation tasks to complete")
	if err := g.Wait(); err != nil {
		logger.Errorf(ctx, "Evaluation error: %v", err)
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	// Final update of evaluation metrics
	_ = e.evaluationMemoryStorage.update(detail.Task.ID, func(params *types.EvaluationDetail) {
		params.Metric = metricHook.MetricResult()
		params.Task.Finished = finished
	})

	logger.Infof(ctx, "Dataset evaluation completed successfully, task ID: %s", detail.Task.ID)
	return nil
}

// getPassageList extracts and organizes passages from QA pairs
// Passage IDs are identifiers, not array offsets. Custom datasets can use
// sparse or large IDs; metrics associate retrieval results by content.
func getPassageList(dataset []*types.QAPair) ([]string, error) {
	pIDMap := make(map[int]string)
	for i, qaPair := range dataset {
		if qaPair == nil || len(qaPair.PIDs) != len(qaPair.Passages) {
			return nil, fmt.Errorf("invalid passages in QA pair %d", i)
		}
		for i := 0; i < len(qaPair.PIDs); i++ {
			pIDMap[qaPair.PIDs[i]] = qaPair.Passages[i]
		}
	}
	ids := make([]int, 0, len(pIDMap))
	for id := range pIDMap {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	passages := make([]string, 0, len(ids))
	for _, id := range ids {
		passages = append(passages, pIDMap[id])
	}
	return passages, nil
}
