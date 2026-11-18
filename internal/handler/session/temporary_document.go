package session

import (
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"

	apperrors "github.com/Usir233/GENYEE/internal/errors"
	"github.com/Usir233/GENYEE/internal/filetransport"
	"github.com/Usir233/GENYEE/internal/logger"
	"github.com/Usir233/GENYEE/internal/types"
	secutils "github.com/Usir233/GENYEE/internal/utils"
	"github.com/gin-gonic/gin"
)

// isUploadTooLarge reports whether a multipart error is the body cap firing
// rather than a malformed or absent part. The parent handler package keeps the
// same check for its own routes; session is a subpackage of it, so the two
// cannot share the symbol without an import cycle.
func isUploadTooLarge(err error) bool {
	var tooLarge *http.MaxBytesError
	return errors.As(err, &tooLarge)
}

// UploadTemporaryDocument accepts one multipart file and immediately returns
// a session-scoped document ID. Parsing continues in the document worker.
// UploadTemporaryDocument godoc
// @Summary      上传会话临时附件
// @Tags         会话
// @Accept       mpfd
// @Produce      json
// @Param        id    path      string  true  "会话 ID"
// @Success      201   {object}  map[string]interface{}
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /sessions/{id}/attachments [post]
func (h *Handler) UploadTemporaryDocument(c *gin.Context) {
	ctx := c.Request.Context()
	if h.temporaryDocuments == nil {
		_ = c.Error(apperrors.NewServiceUnavailableError("temporary document storage is unavailable"))
		return
	}
	sessionID := sessionIDParam(c)
	// Uploading attaches content to the session, so use the strict owner scope:
	// a tenant admin may read an API-key session but must not add attachments.
	session, err := h.sessionService.GetOwnedSession(ctx, sessionID)
	if err != nil || session == nil {
		_ = c.Error(apperrors.NewNotFoundError("Session not found"))
		return
	}
	sessionTenantID := sessionTenantOrContext(c, session)
	maxBytes := secutils.GetMaxFileSizeMB()*1024*1024 + 1024*1024
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes)
	fileHeader, err := c.FormFile("file")
	if err != nil {
		// A capped body fails the multipart parse, so an oversized upload
		// arrives here rather than in any size check below. Report it as the
		// limit it hit: "invalid attachment upload" sends the caller looking for
		// a missing field when the file was sent and refused.
		if isUploadTooLarge(err) {
			_ = c.Error(apperrors.NewBadRequestError(fmt.Sprintf(
				"attachment exceeds the %d MB upload limit", secutils.GetMaxFileSizeMB())))
			return
		}
		_ = c.Error(apperrors.NewBadRequestError(fmt.Sprintf("invalid attachment upload: %v", err)))
		return
	}
	file, err := fileHeader.Open()
	if err != nil {
		_ = c.Error(apperrors.NewBadRequestError("failed to open attachment"))
		return
	}
	defer func() { _ = file.Close() }()

	sourceTenantID, parseErr := types.ParseAgentSourceTenantID(c.PostForm(types.AgentSourceTenantIDParam))
	if parseErr != nil {
		_ = c.Error(apperrors.NewBadRequestError(parseErr.Error()))
		return
	}
	agent, resourceTenantID, _ := h.resolveAgent(ctx, c, c.PostForm("agent_id"), sourceTenantID)
	if sourceTenantID != 0 && agent == nil {
		_ = c.Error(apperrors.NewNotFoundError("Shared agent not found"))
		return
	}
	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(fileHeader.Filename)), ".")
	options := types.TemporaryDocumentCreateOptions{ParserEngine: strings.TrimSpace(c.PostForm("parser_engine"))}
	if agent != nil {
		options.ResourceTenantID = resourceTenantID
		// A shared agent parses in its owner's workspace, with the parser
		// engines (and credentials) configured there: only the agent's own
		// rules may pick one, not the caller.
		if resourceTenantID != 0 && resourceTenantID != sessionTenantID {
			options.ParserEngine = ""
		}
		if len(agent.Config.SupportedFileTypes) > 0 && !containsFileType(agent.Config.SupportedFileTypes, ext) {
			_ = c.Error(apperrors.NewBadRequestError("file type is not supported by this agent"))
			return
		}
		if isAudioExtension(ext) {
			if !agent.Config.AudioUploadEnabled || agent.Config.ASRModelID == "" {
				_ = c.Error(apperrors.NewBadRequestError("audio upload is not enabled or no ASR model is configured"))
				return
			}
			options.ASRModelID = agent.Config.ASRModelID
		}
		// Resolve the parser engine from the agent's chat rules when the caller
		// did not pass an explicit engine. Tenant-level rules remain the final
		// fallback and are applied in the parse worker.
		if options.ParserEngine == "" || options.ParserEngine == "auto" {
			if engine := agent.Config.ResolveChatParserEngine(ext); engine != "" {
				options.ParserEngine = engine
			}
		}
		// Image understanding (caption/OCR) uses the agent's VLM model. Images
		// always benefit; scanned/image-only documents only OCR when the agent
		// enabled AttachmentImageUnderstanding (see parse worker threshold gate).
		if agent.Config.ImageUploadEnabled && agent.Config.VLMModelID != "" {
			options.VLMModelID = agent.Config.VLMModelID
			options.ImageUnderstanding = agent.Config.AttachmentImageUnderstanding
			options.OCRMaxPages = agent.Config.AttachmentOCRMaxPages
		}
	}
	document, err := h.temporaryDocuments.Create(
		ctx, sessionTenantID, sessionID,
		fileHeader.Filename, fileHeader.Header.Get("Content-Type"), fileHeader.Size, file, options,
	)
	if err != nil {
		_ = c.Error(apperrors.NewBadRequestError(err.Error()))
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"success": true, "data": document.ForClient()})
}

// ListTemporaryDocuments godoc
// @Summary      获取会话临时附件列表
// @Tags         会话
// @Produce      json
// @Param        id   path      string  true  "会话 ID"
// @Success      200  {object}  map[string]interface{}
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /sessions/{id}/attachments [get]
func (h *Handler) ListTemporaryDocuments(c *gin.Context) {
	ctx := c.Request.Context()
	if h.temporaryDocuments == nil {
		_ = c.Error(apperrors.NewServiceUnavailableError("temporary document storage is unavailable"))
		return
	}
	sessionID := sessionIDParam(c)
	session, err := h.sessionService.GetSession(ctx, sessionID)
	if err != nil || session == nil {
		_ = c.Error(apperrors.NewNotFoundError("Session not found"))
		return
	}
	documents, err := h.temporaryDocuments.List(ctx, sessionTenantOrContext(c, session), sessionID)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		_ = c.Error(apperrors.NewInternalServerError("Failed to list attachments"))
		return
	}
	for i := range documents {
		documents[i] = documents[i].ForClient()
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": documents})
}

// GetTemporaryDocument godoc
// @Summary      获取会话临时附件详情
// @Tags         会话
// @Produce      json
// @Param        id             path      string  true  "会话 ID"
// @Param        attachment_id  path      string  true  "附件 ID"
// @Success      200            {object}  map[string]interface{}
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /sessions/{id}/attachments/{attachment_id} [get]
func (h *Handler) GetTemporaryDocument(c *gin.Context) {
	ctx := c.Request.Context()
	if h.temporaryDocuments == nil {
		_ = c.Error(apperrors.NewServiceUnavailableError("temporary document storage is unavailable"))
		return
	}
	sessionID := sessionIDParam(c)
	session, err := h.sessionService.GetSession(ctx, sessionID)
	if err != nil || session == nil {
		_ = c.Error(apperrors.NewNotFoundError("Session not found"))
		return
	}
	document, err := h.temporaryDocuments.Get(
		ctx, sessionTenantOrContext(c, session), sessionID, c.Param("attachment_id"),
	)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		_ = c.Error(apperrors.NewInternalServerError("Failed to retrieve attachment"))
		return
	}
	if document == nil {
		_ = c.Error(apperrors.NewNotFoundError("Attachment not found"))
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": document.ForClient()})
}

// PreviewTemporaryDocument godoc
// @Summary      预览会话临时附件内容
// @Tags         会话
// @Produce      octet-stream
// @Param        id             path      string  true  "会话 ID"
// @Param        attachment_id  path      string  true  "附件 ID"
// @Success      200            {string}  string
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /sessions/{id}/attachments/{attachment_id}/preview [get]
func (h *Handler) PreviewTemporaryDocument(c *gin.Context) {
	ctx := c.Request.Context()
	if h.temporaryDocuments == nil {
		_ = c.Error(apperrors.NewServiceUnavailableError("temporary document storage is unavailable"))
		return
	}
	sessionID := sessionIDParam(c)
	session, err := h.sessionService.GetSession(ctx, sessionID)
	if err != nil || session == nil {
		_ = c.Error(apperrors.NewNotFoundError("Session not found"))
		return
	}
	attachmentID := secutils.SanitizeForLog(c.Param("attachment_id"))
	if attachmentID == "" {
		_ = c.Error(apperrors.NewBadRequestError("Attachment ID cannot be empty"))
		return
	}
	file, filename, err := h.temporaryDocuments.OpenFile(
		ctx, sessionTenantOrContext(c, session), sessionID, attachmentID,
	)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "not found") {
			_ = c.Error(apperrors.NewNotFoundError("Attachment not found"))
			return
		}
		logger.ErrorWithFields(ctx, err, nil)
		_ = c.Error(apperrors.NewInternalServerError("Failed to retrieve attachment"))
		return
	}
	if err := filetransport.Serve(c.Writer, c.Request, file, filetransport.Options{
		Filename: filename, CacheControl: "private, no-store",
	}); err != nil {
		logger.Errorf(ctx, "Failed to stream attachment preview: %v", err)
	}
}

// DeleteTemporaryDocument godoc
// @Summary      删除会话临时附件
// @Tags         会话
// @Produce      json
// @Param        id             path      string  true  "会话 ID"
// @Param        attachment_id  path      string  true  "附件 ID"
// @Success      200            {object}  map[string]interface{}
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /sessions/{id}/attachments/{attachment_id} [delete]
func (h *Handler) DeleteTemporaryDocument(c *gin.Context) {
	ctx := c.Request.Context()
	if h.temporaryDocuments == nil {
		_ = c.Error(apperrors.NewServiceUnavailableError("temporary document storage is unavailable"))
		return
	}
	sessionID := sessionIDParam(c)
	// Deleting mutates the session's attachments, so use the strict owner scope:
	// a tenant admin may read an API-key session but must not remove attachments.
	session, err := h.sessionService.GetOwnedSession(ctx, sessionID)
	if err != nil || session == nil {
		_ = c.Error(apperrors.NewNotFoundError("Session not found"))
		return
	}
	if err := h.temporaryDocuments.Delete(
		ctx, sessionTenantOrContext(c, session), sessionID, c.Param("attachment_id"),
	); err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		_ = c.Error(apperrors.NewInternalServerError("Failed to delete attachment"))
		return
	}
	c.Status(http.StatusNoContent)
}

func sessionTenantOrContext(c *gin.Context, session *types.Session) uint64 {
	if session != nil && session.TenantID != 0 {
		return session.TenantID
	}
	tid, _ := tenantIDFromGinOrContext(c)
	return tid
}

func containsFileType(supported []string, ext string) bool {
	for _, item := range supported {
		if strings.TrimPrefix(strings.ToLower(strings.TrimSpace(item)), ".") == ext {
			return true
		}
	}
	return false
}

func isAudioExtension(ext string) bool {
	switch ext {
	case "mp3", "wav", "m4a", "flac", "ogg", "aac":
		return true
	default:
		return false
	}
}
