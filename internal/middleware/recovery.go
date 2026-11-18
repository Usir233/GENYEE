package middleware

import (
	"fmt"
	"net/http"
	"runtime/debug"

	"github.com/Usir233/GENYEE/internal/logger"
	"github.com/Usir233/GENYEE/internal/types"
	secutils "github.com/Usir233/GENYEE/internal/utils"
	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
)

// Recovery is a middleware that recovers from panics without leaking internal
// panic strings or stack details to external callers.
func Recovery() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if err := recover(); err != nil {
				ctx := c.Request.Context()
				requestID, ok := c.Get(types.RequestIDContextKey.String())
				if !ok {
					requestID, _ = c.Get("RequestID")
				}
				reqIDStr := ""
				if requestID != nil {
					reqIDStr = secutils.SanitizeForLog(fmt.Sprint(requestID))
				}

				stacktrace := debug.Stack()
				logger.ErrorWithFields(ctx, fmt.Errorf("panic: %v", err), logrus.Fields{
					"request_id": reqIDStr,
					"stacktrace": string(stacktrace),
				})

				if c.Writer.Written() {
					c.Abort()
					return
				}
				abortWithStatusError(c, http.StatusInternalServerError, "Internal server error")
			}
		}()

		c.Next()
	}
}
