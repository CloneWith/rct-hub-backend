package handler

import (
	"github.com/gin-gonic/gin"
	"go.mongodb.org/mongo-driver/v2/bson"

	"rctHubBackend/internal/domain"
	"rctHubBackend/internal/service"
	"rctHubBackend/pkg/response"
)

// BeatmapHandler exposes beatmap management endpoints (admin CRUD only).
type BeatmapHandler struct {
	svc *service.BeatmapService
}

func NewBeatmapHandler(svc *service.BeatmapService) *BeatmapHandler {
	return &BeatmapHandler{svc: svc}
}

// Create creates a new beatmap entry.
func (h *BeatmapHandler) Create(c *gin.Context) {
	var beatmap domain.Beatmap
	if err := c.ShouldBindJSON(&beatmap); err != nil {
		response.BadRequest(c, "invalid request body")
		return
	}

	if err := h.svc.Create(c.Request.Context(), &beatmap); err != nil {
		_ = c.Error(err)
		return
	}
	response.Created(c, beatmap)
}

// Update updates an existing beatmap.
func (h *BeatmapHandler) Update(c *gin.Context) {
	id, err := bson.ObjectIDFromHex(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "invalid beatmap id")
		return
	}

	var beatmap domain.Beatmap
	if err := c.ShouldBindJSON(&beatmap); err != nil {
		response.BadRequest(c, "invalid request body")
		return
	}
	beatmap.ID = id

	if err := h.svc.Update(c.Request.Context(), &beatmap); err != nil {
		_ = c.Error(err)
		return
	}
	response.JSON(c, beatmap)
}

// Delete removes a beatmap by id.
func (h *BeatmapHandler) Delete(c *gin.Context) {
	id, err := bson.ObjectIDFromHex(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "invalid beatmap id")
		return
	}

	if err := h.svc.Delete(c.Request.Context(), id); err != nil {
		_ = c.Error(err)
		return
	}
	response.NoContent(c)
}
