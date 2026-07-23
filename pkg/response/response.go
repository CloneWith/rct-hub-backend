package response

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// JSON writes a standard success response.
func JSON(c *gin.Context, data any) {
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    data,
	})
}

// Created writes a 201 response.
func Created(c *gin.Context, data any) {
	c.JSON(http.StatusCreated, gin.H{
		"success": true,
		"data":    data,
	})
}

// NoContent writes a 204 response.
func NoContent(c *gin.Context) {
	c.Status(http.StatusNoContent)
}

// Error writes a standard error response.
func Error(c *gin.Context, status int, message string, details ...any) {
	body := gin.H{
		"success": false,
		"error":   message,
	}
	if len(details) > 0 && details[0] != nil {
		body["details"] = details[0]
	}
	c.JSON(status, body)
}

// BadRequest shorthand.
func BadRequest(c *gin.Context, message string) {
	Error(c, http.StatusBadRequest, message)
}

// Unauthorized shorthand.
func Unauthorized(c *gin.Context, message string) {
	Error(c, http.StatusUnauthorized, message)
}

// Forbidden shorthand.
func Forbidden(c *gin.Context, message string) {
	Error(c, http.StatusForbidden, message)
}

// NotFound shorthand.
func NotFound(c *gin.Context, message string) {
	Error(c, http.StatusNotFound, message)
}

// InternalError shorthand.
func InternalError(c *gin.Context, message string) {
	Error(c, http.StatusInternalServerError, message)
}
