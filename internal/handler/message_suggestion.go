package handler

import (
	"errors"
	"net/http"
	"strings"

	apperrors "github.com/Usir233/GENYEE/internal/errors"
	"github.com/Usir233/GENYEE/internal/logger"
	"github.com/Usir233/GENYEE/internal/types"
	"github.com/Usir233/GENYEE/internal/types/interfaces"
	secutils "github.com/Usir233/GENYEE/internal/utils"
	"github.com/gin-gonic/gin"
)

// MessageSuggestionHandler represents MessageSuggestionHandler.
type MessageSuggestionHandler struct {
	service interfaces.MessageSuggestionService
}

// Keeps types imported so swag can resolve MessageSuggestionSet in comments.
var _ types.MessageSuggestionSet

// NewMessageSuggestionHandler provides HTTP handling for NewMessageSuggestionHandler.
func NewMessageSuggestionHandler(service interfaces.MessageSuggestionService) *MessageSuggestionHandler {
	return &MessageSuggestionHandler{service: service}
}

// EnsureMessageSuggestionsRequest represents EnsureMessageSuggestionsRequest.
type EnsureMessageSuggestionsRequest struct {
	Regenerate bool `json:"regenerate"`
}

// SuggestionEventRequest represents SuggestionEventRequest.
type SuggestionEventRequest struct {
	SuggestionSetID string `json:"suggestion_set_id" binding:"required"`
	QuestionID      string `json:"question_id"`
	EventType       string `json:"event_type" binding:"required"`
}

// Ensure godoc
// @Summary      确保生成回答后推荐问题
// @Description  对已完成的助手消息异步生成或重新生成推荐问题；相同配置快照会复用持久化结果
// @Tags         会话
// @Accept       json
// @Produce      json
// @Param        id          path  string  true   "会话 ID"
// @Param        message_id  path  string  true   "助手消息 ID"
// @Param        request     body  EnsureMessageSuggestionsRequest  false  "生成选项"
// @Success      200  {object}  handler.DataResponse{data=types.MessageSuggestionSet}
// @Success      202  {object}  handler.DataResponse{data=types.MessageSuggestionSet}
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /sessions/{id}/messages/{message_id}/suggestions [post]
func (h *MessageSuggestionHandler) Ensure(c *gin.Context) {
	var request EnsureMessageSuggestionsRequest
	if c.Request.ContentLength > 0 {
		if err := c.ShouldBindJSON(&request); err != nil {
			writeAppError(c, apperrors.NewBadRequestError("invalid request body"))
			return
		}
	}
	set, err := h.service.EnsureFollowUps(
		c.Request.Context(),
		messageSuggestionSessionID(c),
		secutils.SanitizeForLog(c.Param("message_id")),
		request.Regenerate,
	)
	if err != nil {
		h.writeError(c, err)
		return
	}
	status := http.StatusOK
	if set != nil && set.Status == "generating" {
		status = http.StatusAccepted
	}
	c.JSON(status, gin.H{"success": true, "data": set})
}

// Get godoc
// @Summary      获取回答后推荐问题
// @Tags         会话
// @Produce      json
// @Param        id          path  string  true  "会话 ID"
// @Param        message_id  path  string  true  "助手消息 ID"
// @Success      200  {object}  handler.DataResponse{data=types.MessageSuggestionSet}
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /sessions/{id}/messages/{message_id}/suggestions [get]
func (h *MessageSuggestionHandler) Get(c *gin.Context) {
	set, err := h.service.GetFollowUps(
		c.Request.Context(),
		messageSuggestionSessionID(c),
		secutils.SanitizeForLog(c.Param("message_id")),
	)
	if err != nil {
		h.writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": set})
}

func messageSuggestionSessionID(c *gin.Context) string {
	sessionID := c.Param("session_id")
	if sessionID == "" {
		sessionID = c.Param("id")
	}
	return secutils.SanitizeForLog(sessionID)
}

// RecordEvent godoc
// @Summary      上报推荐问题事件
// @Description  记录曝光、点击或关闭事件
// @Tags         会话
// @Accept       json
// @Param        id       path  string  true  "会话 ID"
// @Param        request  body  SuggestionEventRequest  true  "事件"
// @Success      204
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /sessions/{id}/suggestion-events [post]
func (h *MessageSuggestionHandler) RecordEvent(c *gin.Context) {
	var request SuggestionEventRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		writeAppError(c, apperrors.NewBadRequestError("invalid request body"))
		return
	}
	err := h.service.RecordEvent(
		c.Request.Context(),
		messageSuggestionSessionID(c),
		strings.TrimSpace(request.SuggestionSetID),
		strings.TrimSpace(request.QuestionID),
		strings.TrimSpace(request.EventType),
	)
	if err != nil {
		h.writeError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *MessageSuggestionHandler) writeError(c *gin.Context, err error) {
	switch {
	case isRecordNotFound(err):
		writeAppError(c, apperrors.NewNotFoundError("suggestions not found"))
	case errors.Is(err, apperrors.ErrSessionNotFound):
		writeAppError(c, apperrors.NewNotFoundError("session not found"))
	case strings.Contains(err.Error(), "completed assistant"):
		writeAppError(c, apperrors.NewBadRequestError(err.Error()))
	case strings.Contains(err.Error(), "invalid suggestion event"),
		strings.Contains(err.Error(), "requires question_id"),
		strings.Contains(err.Error(), "does not belong"),
		strings.Contains(err.Error(), "not allowed"):
		writeAppError(c, apperrors.NewBadRequestError(err.Error()))
	default:
		logger.Error(c.Request.Context(), "message suggestion operation failed", err)
		writeAppError(c, apperrors.NewInternalServerError("message suggestion operation failed"))
	}
}
