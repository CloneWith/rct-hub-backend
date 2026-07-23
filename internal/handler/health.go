package handler

import (
	"context"
	"net/http"
	"time"

	"rctHubBackend/internal/database"
	"rctHubBackend/pkg/response"

	"github.com/gin-gonic/gin"
)

// HealthHandler exposes liveness/readiness probes.
type HealthHandler struct {
	db *database.DB
}

func NewHealthHandler(db *database.DB) *HealthHandler {
	return &HealthHandler{db: db}
}

func (h *HealthHandler) Check(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
	defer cancel()

	if err := h.db.Mongo.Ping(ctx, nil); err != nil {
		response.Error(c, http.StatusServiceUnavailable, "mongodb unavailable")
		return
	}
	if err := h.db.Redis.Ping(ctx).Err(); err != nil {
		response.Error(c, http.StatusServiceUnavailable, "redis unavailable")
		return
	}

	response.JSON(c, gin.H{
		"status":    "ok",
		"timestamp": time.Now().UTC(),
	})
}
