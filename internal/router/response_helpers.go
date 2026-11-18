package router

import (
	"net/http"

	apperrors "github.com/Usir233/GENYEE/internal/errors"
	"github.com/gin-gonic/gin"
)

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

func writeRouterAppError(c *gin.Context, appErr *apperrors.AppError) {
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

func writeRouterStatusError(c *gin.Context, status int, message string) {
	writeRouterAppError(c, appErrorFromHTTPStatus(status, message))
}
