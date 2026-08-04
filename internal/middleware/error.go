package middleware

import (
	"errors"
	"net/http"

	"rctHubBackend/pkg/errs"
	"rctHubBackend/pkg/response"

	"github.com/gin-gonic/gin"
)

// ErrorHandler converts domain errors into HTTP responses.
func ErrorHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next()
		if len(c.Errors) == 0 {
			return
		}

		err := c.Errors.Last().Err
		var appErr *errs.AppError
		if errors.As(err, &appErr) {
			if appErr.Code == 0 {
				appErr.Code = http.StatusInternalServerError
			}
			response.Error(c, appErr.Code, appErr.Error())
			return
		}

		switch {
		case errors.Is(err, errs.ErrNotFound):
			response.NotFound(c, err.Error())
		case errors.Is(err, errs.ErrAlreadyExists):
			response.Error(c, http.StatusConflict, err.Error())
		case errors.Is(err, errs.ErrInvalidInput):
			response.BadRequest(c, err.Error())
		case errors.Is(err, errs.ErrUnauthorized):
			response.Unauthorized(c, err.Error())
		case errors.Is(err, errs.ErrForbidden):
			response.Forbidden(c, err.Error())
		case errors.Is(err, errs.ErrCacheSync):
			response.Error(c, http.StatusServiceUnavailable, errs.ErrCacheSync.Error())
		default:
			response.InternalError(c, "internal server error")
		}
	}
}
