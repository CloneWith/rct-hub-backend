package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"go.mongodb.org/mongo-driver/v2/bson"

	"rctHubBackend/internal/domain"
	"rctHubBackend/internal/service"
	"rctHubBackend/pkg/paginate"
	"rctHubBackend/pkg/response"
)

// BeatmapHandler exposes beatmap management endpoints.
type BeatmapHandler struct {
	svc *service.BeatmapService
}

func NewBeatmapHandler(svc *service.BeatmapService) *BeatmapHandler {
	return &BeatmapHandler{svc: svc}
}

// List returns a paginated list of beatmaps.
func (h *BeatmapHandler) List(c *gin.Context) {
	var params paginate.Params
	if err := c.ShouldBindQuery(&params); err != nil {
		response.BadRequest(c, "invalid pagination params")
		return
	}

	result, err := h.svc.List(c.Request.Context(), params)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	response.JSON(c, result)
}

// Get returns a single beatmap by id.
func (h *BeatmapHandler) Get(c *gin.Context) {
	id, err := bson.ObjectIDFromHex(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "invalid beatmap id")
		return
	}

	beatmap, err := h.svc.Get(c.Request.Context(), id)
	if err != nil {
		response.Error(c, http.StatusNotFound, err.Error())
		return
	}
	response.JSON(c, beatmap)
}

// GetByOsuID returns a beatmap by osu! beatmap id.
func (h *BeatmapHandler) GetByOsuID(c *gin.Context) {
	osuID, err := strconv.ParseInt(c.Param("osu_id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "invalid osu id")
		return
	}

	beatmap, err := h.svc.GetByOsuID(c.Request.Context(), osuID)
	if err != nil {
		response.Error(c, http.StatusNotFound, err.Error())
		return
	}
	response.JSON(c, beatmap)
}

// Create creates a new beatmap entry.
func (h *BeatmapHandler) Create(c *gin.Context) {
	var beatmap domain.Beatmap
	if err := c.ShouldBindJSON(&beatmap); err != nil {
		response.BadRequest(c, "invalid request body")
		return
	}

	if err := h.svc.Create(c.Request.Context(), &beatmap); err != nil {
		response.Error(c, http.StatusConflict, err.Error())
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
		response.Error(c, http.StatusNotFound, err.Error())
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
		response.Error(c, http.StatusNotFound, err.Error())
		return
	}
	response.NoContent(c)
}
