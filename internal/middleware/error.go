package middleware

import (
	"errors"
	"net/http"

	"go.uber.org/zap"

	"rctHubBackend/pkg/errs"
	"rctHubBackend/pkg/response"

	"github.com/gin-gonic/gin"
)

// ErrorHandler converts domain errors into HTTP responses and logs them.
// The logger should be the runtime (main) logger — all HTTP errors are
// runtime-level concerns.
func ErrorHandler(log *zap.Logger) gin.HandlerFunc {
	if log == nil {
		log = zap.NewNop()
	}
	return func(c *gin.Context) {
		c.Next()
		if len(c.Errors) == 0 {
			return
		}

		err := c.Errors.Last().Err

		// Extract request-scoped fields for logging.
		reqPath := c.Request.URL.Path
		reqMethod := c.Request.Method

		var appErr *errs.AppError
		if errors.As(err, &appErr) {
			if appErr.Code == 0 {
				appErr.Code = http.StatusInternalServerError
			}
			logError(log, appErr.Code, reqMethod, reqPath, appErr)
			response.Error(c, appErr.Code, appErr.Error())
			return
		}

		switch {
		case errors.Is(err, errs.ErrNotFound):
			logError(log, http.StatusNotFound, reqMethod, reqPath, err)
			response.NotFound(c, err.Error())
		case errors.Is(err, errs.ErrAlreadyExists):
			logError(log, http.StatusConflict, reqMethod, reqPath, err)
			response.Error(c, http.StatusConflict, err.Error())
		case errors.Is(err, errs.ErrConflict):
			logError(log, http.StatusConflict, reqMethod, reqPath, err)
			response.Error(c, http.StatusConflict, err.Error())
		case errors.Is(err, errs.ErrInvalidInput):
			logError(log, http.StatusBadRequest, reqMethod, reqPath, err)
			response.BadRequest(c, err.Error())
		case errors.Is(err, errs.ErrUnauthorized):
			logError(log, http.StatusUnauthorized, reqMethod, reqPath, err)
			response.Unauthorized(c, err.Error())
		case errors.Is(err, errs.ErrForbidden):
			logError(log, http.StatusForbidden, reqMethod, reqPath, err)
			response.Forbidden(c, err.Error())
		case errors.Is(err, errs.ErrCacheSync):
			logError(log, http.StatusServiceUnavailable, reqMethod, reqPath, err)
			response.Error(c, http.StatusServiceUnavailable, errs.ErrCacheSync.Error())
		default:
			logError(log, http.StatusInternalServerError, reqMethod, reqPath, err)
			response.InternalError(c, "internal server error")
		}
	}
}

// logError logs an error with severity appropriate to the HTTP status code.
func logError(log *zap.Logger, status int, method, path string, err error) {
	fields := []zap.Field{
		zap.Int("status", status),
		zap.String("method", method),
		zap.String("path", path),
		zap.Error(err),
	}

	switch {
	case status >= 500:
		log.Error("request error", fields...)
	case status >= 400:
		log.Warn("request error", fields...)
	}
}
