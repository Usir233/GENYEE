package session

import (
	"errors"
	"net/http"

	apperrors "github.com/Usir233/GENYEE/internal/errors"
	"github.com/Usir233/GENYEE/internal/types"
	"github.com/gin-gonic/gin"
)

func sessionIDParam(c *gin.Context) string {
	if v := c.Param("session_id"); v != "" {
		return v
	}
	return c.Param("id")
}

func appErrorFromHTTPStatus(status int, message string) *apperrors.AppError {
	var code apperrors.ErrorCode
	switch status {
	case http.StatusBadRequest:
		code = apperrors.ErrBadRequest
	case http.StatusUnauthorized:
		code = apperrors.ErrUnauthorized
	case http.StatusForbidden:
		code = apperrors.ErrForbidden
	case http.StatusNotFound, http.StatusGone:
		code = apperrors.ErrNotFound
	case http.StatusMethodNotAllowed:
		code = apperrors.ErrMethodNotAllowed
	case http.StatusRequestTimeout, http.StatusGatewayTimeout:
		code = apperrors.ErrTimeout
	case http.StatusConflict:
		code = apperrors.ErrConflict
	case http.StatusRequestEntityTooLarge:
		code = apperrors.ErrRequestEntityTooLarge
	case http.StatusUnprocessableEntity:
		code = apperrors.ErrValidation
	case http.StatusTooManyRequests:
		code = apperrors.ErrTooManyRequests
	case http.StatusNotImplemented, http.StatusBadGateway, http.StatusServiceUnavailable:
		code = apperrors.ErrServiceUnavailable
	default:
		if status < 400 {
			status = http.StatusInternalServerError
		}
		code = apperrors.ErrInternalServer
	}
	return &apperrors.AppError{
		Code:     code,
		Message:  message,
		HTTPCode: status,
	}
}

func writeAppError(c *gin.Context, appErr *apperrors.AppError) {
	if appErr == nil {
		appErr = apperrors.NewInternalServerError("")
	}
	status := appErr.HTTPCode
	if status == 0 {
		status = http.StatusInternalServerError
	}
	_ = c.Error(appErr)
	c.JSON(status, gin.H{
		"success": false,
		"error": gin.H{
			"code":    appErr.Code,
			"message": appErr.Message,
			"details": appErr.Details,
		},
	})
}

func writeStatusError(c *gin.Context, status int, message string) {
	writeAppError(c, appErrorFromHTTPStatus(status, message))
}

// isRecordNotFound matches types.ErrRecordNotFound as well as lower-layer
// ORM "record not found" leaf sentinels without importing gorm in handlers.
func isRecordNotFound(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, types.ErrRecordNotFound) {
		return true
	}
	return hasRecordNotFoundLeaf(err)
}

func hasRecordNotFoundLeaf(err error) bool {
	if err == nil {
		return false
	}
	if multi, ok := err.(interface{ Unwrap() []error }); ok {
		children := multi.Unwrap()
		if len(children) > 0 {
			for _, child := range children {
				if hasRecordNotFoundLeaf(child) {
					return true
				}
			}
			return false
		}
	}
	if next := errors.Unwrap(err); next != nil {
		return hasRecordNotFoundLeaf(next)
	}
	return err.Error() == types.ErrRecordNotFound.Error()
}
