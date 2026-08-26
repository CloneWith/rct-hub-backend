package handler

import (
	"github.com/gin-gonic/gin"
	"go.mongodb.org/mongo-driver/v2/bson"

	"rctHubBackend/internal/domain"
	"rctHubBackend/internal/service"
	"rctHubBackend/pkg/response"
)

// mappoolCreateRequest is the wire format for creating a mappool. The name
// is required; entries may be supplied wholesale.
type mappoolCreateRequest struct {
	Name        *string                `json:"name"`
	Description *string                `json:"description"`
	Entries     *[]domain.MappoolEntry `json:"entries"`
}

// MappoolHandler exposes mappool management endpoints (admin CRUD only).
type MappoolHandler struct {
	svc *service.MappoolService
}

func NewMappoolHandler(svc *service.MappoolService) *MappoolHandler {
	return &MappoolHandler{svc: svc}
}

// Create creates a new mappool (admin only).
func (h *MappoolHandler) Create(c *gin.Context) {
	var req mappoolCreateRequest
	if err := bindJSON(c, &req); err != nil {
		_ = c.Error(err)
		return
	}

	name := ""
	if req.Name != nil {
		name = *req.Name
	}
	pool := &domain.Mappool{
		Name:        name,
		Description: req.Description,
	}
	if req.Entries != nil {
		pool.Entries = *req.Entries
	}
	if err := h.svc.Create(c.Request.Context(), pool); err != nil {
		_ = c.Error(err)
		return
	}
	response.Created(c, pool)
}

// Patch applies a partial update to an existing mappool (admin only). A
// present entries array replaces the whole list.
func (h *MappoolHandler) Patch(c *gin.Context) {
	id, err := bson.ObjectIDFromHex(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "invalid mappool id")
		return
	}

	var patch service.MappoolPatch
	if err := bindJSON(c, &patch); err != nil {
		_ = c.Error(err)
		return
	}

	pool, err := h.svc.Patch(c.Request.Context(), id, &patch)
	if err != nil {
		_ = c.Error(err)
		return
	}
	response.JSON(c, pool)
}

// Delete removes a mappool (admin only). Mappools referenced by rooms are
// rejected.
func (h *MappoolHandler) Delete(c *gin.Context) {
	id, err := bson.ObjectIDFromHex(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "invalid mappool id")
		return
	}

	if err := h.svc.Delete(c.Request.Context(), id); err != nil {
		_ = c.Error(err)
		return
	}
	response.NoContent(c)
}
