package handler

import (
	"github.com/gin-gonic/gin"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.uber.org/zap"

	"rctHubBackend/internal/domain"
	"rctHubBackend/internal/service"
	"rctHubBackend/pkg/response"
)

// BeatmapHandler exposes beatmap management endpoints (admin CRUD only).
type BeatmapHandler struct {
	svc *service.BeatmapService
	log *zap.Logger
}

func NewBeatmapHandler(svc *service.BeatmapService, log *zap.Logger) *BeatmapHandler {
	if log == nil {
		log = zap.NewNop()
	}
	return &BeatmapHandler{svc: svc, log: log}
}

// Create creates a new beatmap entry.
func (h *BeatmapHandler) Create(c *gin.Context) {
	var beatmap domain.Beatmap
	if err := bindJSON(c, &beatmap); err != nil {
		_ = c.Error(err)
		return
	}

	if err := h.svc.Create(c.Request.Context(), &beatmap); err != nil {
		h.log.Warn("beatmap operation failed", zap.String("op", "create"), zap.Error(err))
		_ = c.Error(err)
		return
	}
	h.log.Info("beatmap created", zap.Int64("osu_id", beatmap.OnlineID), zap.String("title", beatmap.Title))
	response.Created(c, beatmap)
}

// Patch applies a partial update to an existing beatmap.
func (h *BeatmapHandler) Patch(c *gin.Context) {
	id, err := bson.ObjectIDFromHex(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "invalid beatmap id")
		return
	}

	var patch service.BeatmapPatch
	if err := bindJSON(c, &patch); err != nil {
		_ = c.Error(err)
		return
	}

	beatmap, err := h.svc.Patch(c.Request.Context(), id, &patch)
	if err != nil {
		h.log.Warn("beatmap operation failed", zap.String("op", "patch"), zap.String("id", c.Param("id")), zap.Error(err))
		_ = c.Error(err)
		return
	}
	h.log.Info("beatmap patched", zap.String("id", c.Param("id")), zap.Int64("osu_id", beatmap.OnlineID))
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
		h.log.Warn("beatmap operation failed", zap.String("op", "delete"), zap.Error(err))
		_ = c.Error(err)
		return
	}
	h.log.Info("beatmap deleted", zap.String("id", c.Param("id")))
	response.NoContent(c)
}
