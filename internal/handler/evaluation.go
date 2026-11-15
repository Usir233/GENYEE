package handler

import (
	"context"
	stderrors "errors"
	"net/http"
	"strings"

	"github.com/Usir233/GENYEE/internal/application/repository"
	"github.com/Usir233/GENYEE/internal/errors"
	"github.com/Usir233/GENYEE/internal/logger"
	"github.com/Usir233/GENYEE/internal/types"
	"github.com/Usir233/GENYEE/internal/types/interfaces"
	secutils "github.com/Usir233/GENYEE/internal/utils"
	"github.com/gin-gonic/gin"
)

// EvaluationHandler handles evaluation related HTTP requests
type EvaluationHandler struct {
	evaluationService interfaces.EvaluationService // Service for evaluation operations
	kbService         interfaces.KnowledgeBaseService
}

// NewEvaluationHandler creates a new EvaluationHandler instance
func NewEvaluationHandler(
	evaluationService interfaces.EvaluationService,
	kbService interfaces.KnowledgeBaseService,
) *EvaluationHandler {
	return &EvaluationHandler{
		evaluationService: evaluationService,
		kbService:         kbService,
	}
}

func resolveEvaluationTenant(c *gin.Context) (context.Context, uint64, bool) {
	ctx := c.Request.Context()
	tenantID := c.GetUint64(string(types.TenantIDContextKey))
	if ctxTenantID, ok := types.TenantIDFromContext(ctx); ok && ctxTenantID != 0 {
		if tenantID == 0 {
			tenantID = ctxTenantID
		}
	} else if tenantID != 0 {
		ctx = context.WithValue(ctx, types.TenantIDContextKey, tenantID)
	}
	if tenantID == 0 {
		return ctx, 0, false
	}
	return ctx, tenantID, true
}

// EvaluationRequest contains parameters for evaluation request
type EvaluationRequest struct {
	DatasetID       string `json:"dataset_id"`        // ID of dataset to evaluate
	KnowledgeBaseID string `json:"knowledge_base_id"` // ID of knowledge base to use
	ChatModelID     string `json:"chat_id"`           // ID of chat model to use
	RerankModelID   string `json:"rerank_id"`         // ID of rerank model to use
}

// Evaluation godoc
// @Summary      执行评估
// @Description  对知识库进行评估测试
// @Tags         评估
// @Accept       json
// @Produce      json
// @Param        request  body      EvaluationRequest  true  "评估请求参数"
// @Success      200      {object}  handler.DataResponse{data=types.EvaluationDetail}  "评估任务"
// @Failure      400      {object}  errors.AppError         "请求参数错误"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /evaluation/ [post]
func (e *EvaluationHandler) Evaluation(c *gin.Context) {
	ctx := c.Request.Context()

	logger.Info(ctx, "Start processing evaluation request")

	limitJSONBody(c, skillSourceJSONMaxBytes)
	var request EvaluationRequest
	if err := c.ShouldBind(&request); err != nil {
		if isRequestBodyTooLarge(err) {
			c.Error(newRequestEntityTooLargeError("request body is too large"))
			return
		}
		logger.Error(ctx, "Failed to parse request parameters", err)
		c.Error(errors.NewBadRequestError("Invalid request parameters").WithDetails(err.Error()))
		return
	}

	var tenantID uint64
	var ok bool
	ctx, tenantID, ok = resolveEvaluationTenant(c)
	if !ok {
		logger.Error(ctx, "Failed to get tenant ID")
		c.Error(errors.NewUnauthorizedError("Unauthorized"))
		return
	}

	kbID := strings.TrimSpace(secutils.SanitizeForLog(request.KnowledgeBaseID))
	if err := requireTenantAPIKeyKnowledgeBase(ctx, kbID); err != nil {
		c.Error(err)
		return
	}
	if kbID != "" && e.kbService != nil {
		kb, err := e.kbService.GetKnowledgeBaseByID(ctx, kbID)
		if err != nil {
			if stderrors.Is(err, repository.ErrKnowledgeBaseNotFound) {
				c.Error(errors.NewNotFoundError("Knowledge base not found"))
				return
			}
			logger.ErrorWithFields(ctx, err, nil)
			c.Error(errors.NewInternalServerError(err.Error()))
			return
		}
		if kb == nil {
			c.Error(errors.NewNotFoundError("Knowledge base not found"))
			return
		}
		if kb.TenantID != tenantID {
			c.Error(errors.NewForbiddenError("No permission to access this knowledge base"))
			return
		}
	}

	logger.Infof(ctx, "Executing evaluation, tenant: %v, dataset: %s, knowledge_base: %s, chat: %s, rerank: %s",
		tenantID,
		secutils.SanitizeForLog(request.DatasetID),
		kbID,
		secutils.SanitizeForLog(request.ChatModelID),
		secutils.SanitizeForLog(request.RerankModelID),
	)

	task, err := e.evaluationService.Evaluation(ctx,
		secutils.SanitizeForLog(request.DatasetID),
		kbID,
		secutils.SanitizeForLog(request.ChatModelID),
		secutils.SanitizeForLog(request.RerankModelID),
	)
	if err != nil {
		if appErr, ok := errors.IsAppError(err); ok {
			c.Error(appErr)
			return
		}
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	logger.Infof(ctx, "Evaluation task created successfully")
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    task,
	})
}

// GetEvaluationRequest contains parameters for getting evaluation result
type GetEvaluationRequest struct {
	TaskID string `form:"task_id" binding:"required"` // ID of evaluation task
}

// GetEvaluationResult godoc
// @Summary      获取评估结果
// @Description  根据任务ID获取评估结果
// @Tags         评估
// @Accept       json
// @Produce      json
// @Param        task_id  query     string  true  "评估任务ID"
// @Success      200      {object}  handler.DataResponse{data=types.EvaluationDetail}  "评估结果"
// @Failure      400      {object}  errors.AppError         "请求参数错误"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /evaluation/ [get]
func (e *EvaluationHandler) GetEvaluationResult(c *gin.Context) {
	ctx := c.Request.Context()

	logger.Info(ctx, "Start retrieving evaluation result")

	var request GetEvaluationRequest
	if err := c.ShouldBind(&request); err != nil {
		logger.Error(ctx, "Failed to parse request parameters", err)
		c.Error(errors.NewBadRequestError("Invalid request parameters").WithDetails(err.Error()))
		return
	}

	var ok bool
	ctx, _, ok = resolveEvaluationTenant(c)
	if !ok {
		c.Error(errors.NewUnauthorizedError("Unauthorized"))
		return
	}

	taskID := strings.TrimSpace(secutils.SanitizeForLog(request.TaskID))
	if err := requireTaskProgressTenant(ctx, taskID); err != nil {
		c.Error(err)
		return
	}

	result, err := e.evaluationService.EvaluationResult(ctx, taskID)
	if err != nil {
		if appErr, ok := errors.IsAppError(err); ok {
			c.Error(appErr)
			return
		}
		if err.Error() == "task not found" || err.Error() == "tenant ID does not match" {
			c.Error(errors.NewNotFoundError("task not found"))
			return
		}
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	logger.Info(ctx, "Retrieved evaluation result successfully")
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    result,
	})
}
